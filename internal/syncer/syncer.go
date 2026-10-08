// Package syncer moves the snapshot between the working tree, the envmove branch and
// the remote.
//
// Merge policy for v0 is deliberately conservative and easy to explain: a remote file
// is written only when the local copy is absent or identical. When both sides changed
// the same file, envmove reports a conflict instead of guessing. That is the right
// trade while usage is sequential, and it is what Faz 2 replaces with real merging.
package syncer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/crypt"
	"github.com/atalayhuryasar/envmove/internal/gitx"
	"github.com/atalayhuryasar/envmove/internal/state"
)

// ErrConflicts marks the one condition that may block a code push: two sides changed
// the same file and envmove cannot tell which one is right. Everything else warns and
// lets the push through, because a context tool must never make shipping code
// impossible.
var ErrConflicts = errors.New("conflicts")

const Remote = "origin"

// EnvNoPush is set by the pre-push hook. Without it the hook's own sync would publish
// the envmove branch, git push would re-enter the hook, and the two would recurse.
const EnvNoPush = "ENVMOVE_NO_PUSH"

// EnvInternal marks a push as envmove's own, so a repository hook does not recurse.
const EnvInternal = "ENVMOVE_INTERNAL"

type Syncer struct {
	repo   *gitx.Repo
	cfg    *config.Config
	cipher *crypt.Cipher
	home   string
	roots  []string

	// holder is this machine's own recipient key, which is what lets a rotation
	// recognise and replace its own entry in the recipient list.
	holder   string
	storeKey func(identity string) error
}

// WithIdentity records how to identify and persist this machine's key. It is separate
// from New so that the keychain stays outside this package.
func (s *Syncer) WithIdentity(holder string, store func(identity string) error) *Syncer {
	s.holder = holder
	s.storeKey = store
	return s
}

func New(repo *gitx.Repo, cfg *config.Config, cipher *crypt.Cipher) *Syncer {
	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	return &Syncer{repo: repo, cfg: cfg, cipher: cipher, home: home, roots: repoRoots(repo.Root)}
}

// repoRoots collects every spelling of the repository path this machine might use.
//
// git reports a fully resolved top-level directory, but paths that reach us from a
// shell, an environment variable or another tool may still be spelled through a
// symlink. Both are recorded so path substitution works either way.
func repoRoots(root string) []string {
	roots := []string{root}
	add := func(p string) {
		if p == "" || !filepath.IsAbs(p) || !sameDir(p, root) {
			return
		}
		for _, existing := range roots {
			if existing == p {
				return
			}
		}
		roots = append(roots, p)
	}
	add(os.Getenv("PWD"))
	if wd, err := os.Getwd(); err == nil {
		add(wd)
	}
	return roots
}

// sameDir reports whether two paths name the same directory once symlinks are
// resolved. Candidate spellings that point somewhere else are not repo roots.
func sameDir(a, b string) bool {
	ra, err := filepath.EvalSymlinks(a)
	if err != nil {
		ra = a
	}
	rb, err := filepath.EvalSymlinks(b)
	if err != nil {
		rb = b
	}
	return ra == rb
}

// Change kinds, in the language the CLI prints them in.
const (
	Incoming  = "gelen"
	Outgoing  = "giden"
	Conflict  = "conflict"
	SameState = "same"
)

type Change struct {
	Path string
	Kind string
}

type Diff struct {
	Changes   []Change
	Incoming  map[string]state.Entry
	Outgoing  []string
	Conflicts []string
	// Removals are files the other side deleted while this machine still has them.
	Removals []string
}

func (s *Syncer) Ref() string { return "refs/heads/" + s.cfg.Branch }

// Collect reads every configured path from disk into a fresh snapshot, contracting
// machine-specific absolute paths so the snapshot stays portable.
func (s *Syncer) Collect() (*state.State, error) {
	st := &state.State{Version: state.Version, Files: map[string]state.Entry{}}
	for _, rel := range s.cfg.Paths {
		abs := filepath.Join(s.repo.Root, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue // deleted locally, or matched a directory: nothing to capture
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		if state.IsText(data) {
			data = []byte(state.Contract(string(data), s.roots, s.home))
		}
		st.Files[rel] = state.Entry{
			Mode:    "0644",
			SHA256:  state.Sum(data),
			Content: data,
		}
	}
	return st, nil
}

// Apply writes incoming entries to the working tree, expanding placeholders back to
// this machine's paths. Files stay plaintext so ordinary tooling keeps working.
func (s *Syncer) Apply(entries map[string]state.Entry) ([]string, error) {
	written := make([]string, 0, len(entries))
	for rel, e := range entries {
		abs := filepath.Join(s.repo.Root, rel)
		data := e.Content
		if state.IsText(data) {
			data = []byte(state.Expand(string(data), s.roots, s.home))
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return written, err
		}
		if err := os.WriteFile(abs, data, 0o644); err != nil {
			return written, err
		}
		written = append(written, rel)
	}
	return written, nil
}

// ReadSnapshot decrypts the snapshot stored at ref. A missing ref yields a nil
// snapshot and no error: nothing has ever been pushed yet.
func (s *Syncer) ReadSnapshot(ref string) (*state.State, error) {
	if s.repo.RefCommit(ref) == "" {
		return nil, nil
	}
	blob, err := s.repo.ReadBlob(ref, config.BlobPath)
	if err != nil {
		return nil, err
	}
	plain, err := s.cipher.Decrypt(blob)
	if err != nil {
		return nil, fmt.Errorf(
			"this snapshot cannot be decrypted with this machine's key.\n"+
				"    Usually this machine was just added: once envmove.toml is committed and\n"+
				"    pushed, the other machines re-encrypt on their next run.\n"+
				"    Then try again.\n"+
				"    underlying cause: %w", err)
	}
	return state.Unmarshal(plain)
}

// baselinePath records the last snapshot that was in sync on this machine.
//
// It is what makes a three-way comparison possible: without knowing the state we
// started from, "I changed this file" and "we both changed this file" are
// indistinguishable, and every local edit would look like a conflict.
const baselinePath = ".envmove/baseline.age"

// Publish pushes local changes and nothing else, skipping the network entirely when
// there is nothing to send.
//
// This exists because the fast path cannot live inside Push: sync calls Pull first, and
// Pull fetches, so by the time Push got to check there had already been a round trip.
// The check has to come before any network call, which means it belongs at the level the
// hooks call.
//
// A commit that touched only code now costs no network at all. Measured on a slow
// connection that is the difference between fourteen seconds and a fraction of one,
// which is the difference between a tool you tolerate and one you route around with
// --no-verify.
func (s *Syncer) Publish() (*Diff, bool, error) {
	if s.repo.RefCommit(s.Ref()) != "" && !s.localDiffersFromBaseline() {
		return &Diff{}, false, nil
	}
	return s.Push()
}

// localDiffersFromBaseline reports whether any carried file has changed since the last
// sync, judged entirely from local state.
func (s *Syncer) localDiffersFromBaseline() bool {
	base := s.LoadBaseline()
	if base == nil {
		return true // never synced here, so assume there is something to say
	}
	local, err := s.Collect()
	if err != nil {
		return true
	}
	if len(local.Files) != len(base.Files) {
		return true
	}
	for path, entry := range local.Files {
		if previous, ok := base.Files[path]; !ok || previous.SHA256 != entry.SHA256 {
			return true
		}
	}
	return false
}

// LoadBaseline returns the last synced snapshot, or nil when there is none.
func (s *Syncer) LoadBaseline() *state.State {
	raw, err := os.ReadFile(filepath.Join(s.repo.Root, baselinePath))
	if err != nil {
		return nil
	}
	plain, err := s.cipher.Decrypt(raw)
	if err != nil {
		return nil
	}
	st, err := state.Unmarshal(plain)
	if err != nil {
		return nil
	}
	return st
}

// SaveBaseline stores the snapshot both sides have just agreed on. It is encrypted
// because it holds the same secrets as the snapshot itself, even though it never
// leaves the machine and is not in git.
func (s *Syncer) SaveBaseline(st *state.State) error {
	abs := filepath.Join(s.repo.Root, baselinePath)
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return err
	}
	plain, err := st.Marshal()
	if err != nil {
		return err
	}
	ciphertext, err := s.cipher.Encrypt(plain)
	if err != nil {
		return err
	}
	return os.WriteFile(abs, ciphertext, 0o600)
}

// Compare is a three-way comparison of the working tree against a snapshot, using
// the baseline as the common ancestor. That ancestor is what lets it tell an edit
// apart from a genuine disagreement.
func (s *Syncer) Compare(snapshot *state.State) (*Diff, error) {
	local, err := s.Collect()
	if err != nil {
		return nil, err
	}
	base := s.LoadBaseline()

	d := &Diff{Incoming: map[string]state.Entry{}}
	if snapshot == nil {
		snapshot = &state.State{Files: map[string]state.Entry{}}
	}

	baseHas := func(p string) (string, bool) {
		if base == nil {
			return "", false
		}
		e, ok := base.Files[p]
		return e.SHA256, ok
	}

	for path, remote := range snapshot.Files {
		loc, localExists := local.Files[path]
		baseSum, baseExists := baseHas(path)

		switch {
		case localExists && loc.SHA256 == remote.SHA256:
			d.Changes = append(d.Changes, Change{path, SameState})

		case !localExists && !baseExists:
			// Neither here nor in any baseline: the file simply has not arrived on this
			// machine yet. There is nothing local to lose, so take theirs.
			d.Changes = append(d.Changes, Change{path, Incoming})
			d.Incoming[path] = remote

		case !localExists:
			// The baseline holds it, so this side removed it. If their copy is untouched,
			// the deletion is ours to publish; if they also changed it, we cannot know
			// whether they meant to keep it.
			if baseSum == remote.SHA256 {
				d.Changes = append(d.Changes, Change{path, Outgoing})
				d.Outgoing = append(d.Outgoing, path)
			} else {
				d.Changes = append(d.Changes, Change{path, Conflict})
				d.Conflicts = append(d.Conflicts, path)
			}

		case baseExists && baseSum == loc.SHA256:
			// Untouched here, changed there: safe to take theirs.
			d.Changes = append(d.Changes, Change{path, Incoming})
			d.Incoming[path] = remote

		case baseExists && baseSum == remote.SHA256:
			// Changed here, untouched there: ours to publish.
			d.Changes = append(d.Changes, Change{path, Outgoing})
			d.Outgoing = append(d.Outgoing, path)

		default:
			d.Changes = append(d.Changes, Change{path, Conflict})
			d.Conflicts = append(d.Conflicts, path)
		}
	}

	for path := range local.Files {
		if _, remoteExists := snapshot.Files[path]; remoteExists {
			continue
		}
		if _, baseExists := baseHas(path); baseExists {
			continue // already reported above as an outgoing deletion
		}
		d.Changes = append(d.Changes, Change{path, Outgoing})
		d.Outgoing = append(d.Outgoing, path)
	}

	// Files this machine knows about, still has on disk, but that the snapshot no
	// longer lists: the other side removed them and that has to reach us too,
	// otherwise a deletion would only ever travel one way.
	if base != nil {
		for path := range base.Files {
			if _, stillRemote := snapshot.Files[path]; stillRemote {
				continue
			}
			if _, stillLocal := local.Files[path]; !stillLocal {
				continue
			}
			d.Changes = append(d.Changes, Change{path, Incoming})
			d.Removals = append(d.Removals, path)
		}
	}
	return d, nil
}

// Status fetches and reports without touching the working tree.
func (s *Syncer) Status() (*Diff, error) {
	if s.repo.HasRemote(Remote) {
		if err := s.repo.FetchRef(Remote, s.Ref()); err != nil {
			return nil, err
		}
	}
	snapshot, err := s.ReadSnapshot(s.Ref())
	if err != nil {
		return nil, err
	}
	return s.Compare(snapshot)
}

// Pull fetches, writes everything that is safe to write, and reports the rest.
func (s *Syncer) Pull() (*Diff, []string, error) {
	if s.repo.HasRemote(Remote) {
		if err := s.repo.FetchRef(Remote, s.Ref()); err != nil {
			return nil, nil, err
		}
	}
	snapshot, err := s.ReadSnapshot(s.Ref())
	if err != nil {
		return nil, nil, err
	}
	d, err := s.Compare(snapshot)
	if err != nil {
		return nil, nil, err
	}
	written, err := s.Apply(d.Incoming)
	if err != nil {
		return d, written, err
	}
	for _, path := range d.Removals {
		if err := os.Remove(filepath.Join(s.repo.Root, path)); err == nil {
			written = append(written, "− "+path)
		}
	}
	// Everything incoming was written and everything removed is gone, so the working
	// tree now matches the snapshot.
	if len(written) > 0 {
		if err := s.SaveBaseline(snapshot); err != nil {
			return d, written, err
		}
	}
	return d, written, nil
}

// Push commits the current working tree to the envmove branch and publishes it.
//
// It refuses to run while conflicts are outstanding, because overwriting a remote
// snapshot would silently discard whichever side envmove did not understand.
func (s *Syncer) Push() (*Diff, bool, error) {
	if s.repo.HasRemote(Remote) {
		if err := s.repo.FetchRef(Remote, s.Ref()); err != nil {
			return nil, false, err
		}
	}

	remote, err := s.ReadSnapshot(s.Ref())
	if err != nil {
		return nil, false, err
	}
	d, err := s.Compare(remote)
	if err != nil {
		return nil, false, err
	}
	if len(d.Conflicts) > 0 {
		return d, false, fmt.Errorf("%d files conflict: %w", len(d.Conflicts), ErrConflicts)
	}

	local, err := s.Collect()
	if err != nil {
		return nil, false, err
	}
	local.Recipients = s.cfg.Recipients

	changed := 0
	for _, c := range d.Changes {
		if c.Kind != SameState {
			changed++
		}
	}

	// A newly added machine changes the recipient set even when no file changed.
	// Re-encrypting here is what lets that machine read the history it missed.
	recipientsStale := remote != nil && !remote.SameRecipients(s.cfg.Recipients)

	if changed == 0 && !recipientsStale && s.repo.RefCommit(s.Ref()) != "" {
		return d, false, nil // already identical, do not create an empty commit
	}

	plain, err := local.Marshal()
	if err != nil {
		return nil, false, err
	}
	ciphertext, err := s.cipher.Encrypt(plain)
	if err != nil {
		return nil, false, err
	}

	msg := fmt.Sprintf("envmove: %d files updated", changed)
	if recipientsStale {
		msg += fmt.Sprintf(" (%d recipients)", len(s.cfg.Recipients))
	}
	if _, err := s.repo.CommitBlob(s.Ref(), config.BlobPath, ciphertext, msg); err != nil {
		return d, false, err
	}

	published := false
	if s.repo.HasRemote(Remote) && os.Getenv(EnvNoPush) == "" {
		if err := s.repo.PushRef(Remote, s.Ref()); err != nil {
			return d, false, err
		}
		published = true
	}

	// Only record the new baseline once the commit exists, so a failed push leaves
	// this machine still reporting its changes as outgoing.
	if s.repo.RefCommit(s.Ref()) != "" {
		if err := s.SaveBaseline(local); err != nil {
			return d, published, err
		}
	}
	return d, published, nil
}
