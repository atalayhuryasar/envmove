package syncer

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/crypt"
	"github.com/atalayhuryasar/envmove/internal/state"
)

// RotateOptions controls how far a rotation reaches.
type RotateOptions struct {
	// AllRecipients replaces the entire list. This is the setting that matters when a
	// key is believed to be compromised, because keeping the old key as a recipient
	// would leave it able to read everything written from now on.
	AllRecipients bool
}

type RotateResult struct {
	NewRecipient string
	Dropped      []string
	Kept         []string
	Reencrypted  bool
}

// Rotate gives this machine a fresh private key and rewrites the current snapshot under
// the new recipient set.
//
// Snapshots already sitting in git history stay encrypted to the keys they were written
// with. That belongs to history rather than to the tool, so it is reported rather than
// implied away: rotating limits future exposure, it does not retroactively protect what
// has already been committed.
func (s *Syncer) Rotate(opts RotateOptions) (RotateResult, error) {
	var res RotateResult

	if s.repo.RefCommit(s.Ref()) == "" {
		return res, fmt.Errorf("this repository has no published state, there is nothing to rotate")
	}

	fresh, err := crypt.GenerateIdentity()
	if err != nil {
		return res, err
	}
	newRecipient := fresh.Recipient().String()
	res.NewRecipient = newRecipient

	var recipients []string
	if opts.AllRecipients {
		res.Dropped = append(res.Dropped, s.cfg.Recipients...)
		recipients = []string{newRecipient}
	} else {
		for _, r := range s.cfg.Recipients {
			if r == s.holder {
				res.Dropped = append(res.Dropped, r)
				continue
			}
			recipients = append(recipients, r)
		}
		recipients = append(recipients, newRecipient)
	}
	sort.Strings(recipients)
	res.Kept = recipients
	s.cfg.Recipients = recipients

	newCipher, err := crypt.New(recipients, []age.Identity{fresh})
	if err != nil {
		return res, err
	}

	// Re-encrypt the snapshot already on the branch, so the new key takes effect now
	// rather than at the next edit.
	existing, err := s.readSnapshotWith(s.cipher, s.Ref())
	if err != nil {
		return res, err
	}
	if existing == nil {
		return res, fmt.Errorf("this repository has no state")
	}
	existing.Recipients = recipients
	plain, err := existing.Marshal()
	if err != nil {
		return res, err
	}
	ciphertext, err := newCipher.Encrypt(plain)
	if err != nil {
		return res, err
	}
	if _, err := s.repo.CommitBlob(s.Ref(), config.BlobPath, ciphertext,
		fmt.Sprintf("envmove: key rotated (%d recipients)", len(recipients))); err != nil {
		return res, err
	}
	res.Reencrypted = true

	// The stored key is swapped only after the snapshot exists, so a failure cannot
	// leave the machine able to write but unable to read.
	if s.storeKey != nil {
		if err := s.storeKey(fresh.String()); err != nil {
			return res, err
		}
	}
	s.cipher = newCipher
	s.holder = newRecipient

	if s.repo.HasRemote(Remote) && !s.internalPush() {
		if err := s.repo.PushRef(Remote, s.Ref()); err != nil {
			return res, err
		}
	}
	return res, nil
}

// RestorePoint is one recoverable snapshot on the envmove branch.
type RestorePoint struct {
	Commit  string
	When    string
	Message string
}

// RestorePoints lists past snapshots newest first, so restore can offer a choice instead
// of asking anyone to remember a commit hash.
func (s *Syncer) RestorePoints(limit int) ([]RestorePoint, error) {
	entries, err := s.repo.LogRef(s.Ref(), limit)
	if err != nil {
		return nil, err
	}
	out := make([]RestorePoint, 0, len(entries))
	for _, e := range entries {
		out = append(out, RestorePoint{Commit: e.Commit, When: e.When, Message: e.Message})
	}
	return out, nil
}

// ResolveRef turns what the user typed into a commit on the envmove branch.
//
// Relative ages are accepted because "go back to yesterday" is the question people
// actually have, and it is the question a hash cannot answer.
func (s *Syncer) ResolveRef(ref string) (string, error) {
	if s.repo.RefCommit(s.Ref()) == "" {
		return "", fmt.Errorf("this repository has no published state")
	}
	if ref == "" {
		return s.repo.RefCommit(s.Ref()), nil
	}
	if commit := s.repo.RefCommit(ref); commit != "" {
		return commit, nil
	}
	if d, ok := parseAge(ref); ok {
		since := refAt(ref, d)
		entries, err := s.repo.LogRef(s.Ref(), 200)
		if err != nil {
			return "", err
		}
		for _, e := range entries {
			if e.Timestamp.Before(since) {
				continue
			}
			return e.Commit, nil
		}
		return "", fmt.Errorf("no snapshot from around %s", ref)
	}
	return "", fmt.Errorf("commit not found: %s", ref)
}

func parseAge(ref string) (time.Duration, bool) {
	if len(ref) < 2 {
		return 0, false
	}
	unit := ref[len(ref)-1]
	var multiplier time.Duration
	switch unit {
	case 'h':
		multiplier = time.Hour
	case 'd':
		multiplier = 24 * time.Hour
	case 'w':
		multiplier = 7 * 24 * time.Hour
	default:
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSuffix(ref, string(unit)))
	if err != nil || n <= 0 {
		return 0, false
	}
	return time.Duration(n) * multiplier, true
}

func refAt(ref string, d time.Duration) time.Time {
	return time.Now().Add(-d)
}

// Restore rewrites the working tree from a past snapshot.
//
// It refuses while unsynced local changes would be lost, unless forced. The whole point
// of a restore is to recover from a mess, and quietly deleting today's work while doing
// it would be the worst possible outcome.
func (s *Syncer) Restore(commit string, force bool) ([]string, error) {
	st, err := s.readSnapshotWith(s.cipher, commit)
	if err != nil {
		return nil, err
	}
	if st == nil {
		return nil, fmt.Errorf("%s contains no state", commit)
	}

	local, err := s.Collect()
	if err != nil {
		return nil, err
	}

	// Only work that is not already on the branch counts as something to lose. The
	// usual reason to restore is that the current state is wrong, and that state is
	// sitting in the snapshot list one entry up. Demanding --force for that would make
	// the safety net unusable exactly when it is needed.
	current, err := s.readSnapshotWith(s.cipher, s.Ref())
	if err != nil {
		return nil, err
	}

	var lost []string
	for path, e := range local.Files {
		if target, ok := st.Files[path]; ok && target.SHA256 == e.SHA256 {
			continue // already matches what we are restoring
		}
		if current != nil {
			if published, ok := current.Files[path]; ok && published.SHA256 == e.SHA256 {
				continue // identical content is already published, nothing is lost
			}
		}
		lost = append(lost, path)
	}
	sort.Strings(lost)
	if len(lost) > 0 && !force {
		return lost, fmt.Errorf("going back to %s would discard work that was never pushed: %s\n"+
			"  to go ahead:  envmove restore %s --force", shortCommit(commit), strings.Join(lost, ", "), shortCommit(commit))
	}

	written, err := s.Apply(st.Files)
	if err != nil {
		return written, err
	}
	if err := s.SaveBaseline(st); err != nil {
		return written, err
	}
	return written, nil
}

func (s *Syncer) readSnapshotWith(c *crypt.Cipher, ref string) (*state.State, error) {
	if s.repo.RefCommit(ref) == "" {
		return nil, nil
	}
	blob, err := s.repo.ReadBlob(ref, config.BlobPath)
	if err != nil {
		return nil, err
	}
	plain, err := c.Decrypt(blob)
	if err != nil {
		return nil, err
	}
	return state.Unmarshal(plain)
}

func (s *Syncer) internalPush() bool {
	return os.Getenv(EnvNoPush) != "" || os.Getenv(EnvInternal) != ""
}

func shortCommit(commit string) string {
	if len(commit) > 7 {
		return commit[:7]
	}
	return commit
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
