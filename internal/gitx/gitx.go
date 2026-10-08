// Package gitx wraps the git plumbing commands envmove needs.
//
// Everything here uses plumbing (hash-object, commit-tree, update-ref) rather than
// porcelain so that writing a snapshot never touches the working tree, never needs a
// checkout, and never disturbs the user's current branch.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

type Repo struct {
	Root string
}

// Open returns the repository containing the current directory.
func Open() (*Repo, error) {
	return OpenAt("")
}

// OpenAt returns the repository rooted at dir, or the one containing the current
// directory when dir is empty.
func OpenAt(dir string) (*Repo, error) {
	cmd := exec.Command("git", "rev-parse", "--show-toplevel")
	if dir != "" {
		cmd.Dir = dir
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("not inside a git repository (git: %s)", strings.TrimSpace(stderr.String()))
	}
	return &Repo{Root: strings.TrimSpace(string(out))}, nil
}

func (r *Repo) git(args ...string) (string, error) {
	out, _, err := r.exec(nil, args...)
	return out, err
}

func (r *Repo) gitBytes(args ...string) ([]byte, error) {
	out, _, err := r.exec(nil, args...)
	return []byte(out), err
}

func (r *Repo) gitEnv(env []string, args ...string) (string, error) {
	out, _, err := r.exec(env, args...)
	return out, err
}

func (r *Repo) exec(env []string, args ...string) (string, []byte, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Root
	if env != nil {
		cmd.Env = append(os.Environ(), env...)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", stderr.Bytes(), fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return stdout.String(), stderr.Bytes(), nil
}

// ---------------------------------------------------------------- references

// RefCommit returns the commit a ref points to, or "" when the ref does not exist.
// CurrentRef returns the ref of the branch currently checked out.
func (r *Repo) CurrentRef() string {
	if out, err := r.git("symbolic-ref", "-q", "HEAD"); err == nil {
		return strings.TrimSpace(out)
	}
	return ""
}

// RefCommit returns the commit a ref points to, or "" when the ref does not exist.
func (r *Repo) RefCommit(ref string) string {
	out, err := r.git("rev-parse", "--verify", "--quiet", ref+"^{commit}")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// UpdateRef moves ref to newSHA, asserting it currently points at oldSHA.
// An empty oldSHA means "must not exist yet".
func (r *Repo) UpdateRef(ref, newSHA, oldSHA string) error {
	args := []string{"update-ref", ref, newSHA}
	if oldSHA != "" {
		args = append(args, oldSHA)
	}
	_, err := r.git(args...)
	return err
}

// ReadBlob returns the contents of path at ref.
func (r *Repo) ReadBlob(ref, path string) ([]byte, error) {
	return r.gitBytes("show", ref+":"+path)
}

// ---------------------------------------------------------------- committing

// CommitBlob writes data as path on ref and returns the new commit SHA.
//
// It builds a tree through a temporary index file, so the user's working tree, index
// and current branch are never touched. This is what lets envmove commit to its own
// branch without asking anyone to check anything out.
func (r *Repo) CommitBlob(ref, path string, data []byte, message string) (string, error) {
	blobSHA, err := r.writeBlob(data)
	if err != nil {
		return "", err
	}

	indexFile, err := os.CreateTemp("", "envmove-index")
	if err != nil {
		return "", err
	}
	indexPath := indexFile.Name()
	indexFile.Close()
	defer os.Remove(indexPath)

	env := []string{"GIT_INDEX_FILE=" + indexPath}
	if _, err := r.gitEnv(env, "read-tree", "--empty"); err != nil {
		return "", err
	}
	if _, err := r.gitEnv(env, "update-index", "--add", "--cacheinfo", "100644,"+blobSHA+","+path); err != nil {
		return "", err
	}
	tree, err := r.gitEnv(env, "write-tree")
	if err != nil {
		return "", err
	}
	tree = strings.TrimSpace(tree)

	parent := r.RefCommit(ref)
	args := []string{"commit-tree", tree, "-m", message}
	if parent != "" {
		args = append(args, "-p", parent)
	}

	commitEnv, err := r.committerEnv()
	if err != nil {
		return "", err
	}
	commit, err := r.gitEnv(commitEnv, args...)
	if err != nil {
		return "", err
	}
	commit = strings.TrimSpace(commit)

	if err := r.UpdateRef(ref, commit, parent); err != nil {
		return "", err
	}
	return commit, nil
}

func (r *Repo) writeBlob(data []byte) (string, error) {
	cmd := exec.Command("git", "hash-object", "-w", "--stdin")
	cmd.Dir = r.Root
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git hash-object: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// committerEnv supplies an identity when the user has no git user.name/user.email
// configured, so that a fresh clone can still commit snapshots.
func (r *Repo) committerEnv() ([]string, error) {
	name, _ := r.git("config", "user.name")
	email, _ := r.git("config", "user.email")
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(email)
	if name == "" {
		name = "envmove"
	}
	if email == "" {
		email = "envmove@localhost"
	}
	return []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + email,
	}, nil
}

// ---------------------------------------------------------------- remotes

// StageOnly stages exactly one path and nothing else, leaving the rest of the index
// and working tree exactly as it was.
func (r *Repo) StageOnly(path string) error {
	_, err := r.git("add", "--", path)
	return err
}

// CommitPath commits exactly one path and nothing else.
//
// Naming the path is not decoration: `git commit --only` refuses to run without one,
// and it is the flag that keeps envmove's commit from sweeping in whatever else
// happened to be staged in the repository.
func (r *Repo) CommitPath(message, path string) (string, error) {
	out, err := r.git("commit", "--only", "-m", message, "--", path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// CurrentBranch returns the checked out branch name, or "" when detached.
func (r *Repo) CurrentBranch() string {
	out, err := r.git("rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return ""
	}
	name := strings.TrimSpace(out)
	if name == "HEAD" {
		return "" // detached
	}
	return name
}

// PushBranch publishes the current branch.
//
// Hooks are skipped deliberately. The repository's pre-push hook blocks on state
// conflicts, which is right for an ordinary code push but wrong here: publishing a
// config change is often the very step that resolves a conflict, so letting the hook
// veto it would be a deadlock.
func (r *Repo) PushBranch(remote string) error {
	branch := r.CurrentBranch()
	if branch == "" {
		return fmt.Errorf("cannot push from a detached HEAD")
	}
	_, err := r.git("push", "--quiet", "--no-verify", remote, "HEAD:refs/heads/"+branch)
	return err
}

// IsTracked reports whether git is already carrying this path.
//
// envmove only touches files git already knows about. Adding a new .env.example to
// someone's repository uninvited is not a helpful thing for a sync tool to do.
func (r *Repo) IsTracked(path string) bool {
	out, err := r.git("ls-files", "--error-unmatch", "--", path)
	return err == nil && strings.TrimSpace(out) != ""
}

// LogEntry is one commit in a ref's history.
type LogEntry struct {
	Commit    string
	When      string
	Message   string
	Timestamp time.Time
}

// LogRef lists commits on a ref, newest first.
func (r *Repo) LogRef(ref string, limit int) ([]LogEntry, error) {
	if limit <= 0 {
		limit = 20
	}
	// A machine readable separator keeps parsing out of the message text.
	out, err := r.git("log", "--format=%H%x1f%cI%x1f%s%x1e", "-n", fmt.Sprint(limit), ref)
	if err != nil {
		return nil, err
	}
	var entries []LogEntry
	for _, chunk := range strings.Split(out, "\x1e") {
		chunk = strings.TrimSpace(chunk)
		if chunk == "" {
			continue
		}
		fields := strings.Split(chunk, "\x1f")
		if len(fields) < 3 {
			continue
		}
		ts, _ := time.Parse(time.RFC3339, fields[1])
		entries = append(entries, LogEntry{
			Commit:    strings.TrimSpace(fields[0]),
			When:      fields[1],
			Message:   strings.TrimSpace(fields[2]),
			Timestamp: ts,
		})
	}
	return entries, nil
}

func (r *Repo) HasRemote(name string) bool {
	out, err := r.git("remote")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == name {
			return true
		}
	}
	return false
}

func (r *Repo) RemoteURL(name string) string {
	out, err := r.git("remote", "get-url", name)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

// FetchRef fetches ref from the remote into the same local ref.
//
// A remote that does not have the ref yet is not an error: it simply means nothing
// has been pushed. That distinction matters because the check is made before every
// push, and treating "not there yet" as a failure would block the very first push.
func (r *Repo) FetchRef(remote, ref string) error {
	exists, err := r.RemoteHasRef(remote, ref)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	_, err = r.git("fetch", "--quiet", remote, ref+":"+ref)
	return err
}

// RemoteHasRef reports whether the remote already publishes ref.
func (r *Repo) RemoteHasRef(remote, ref string) (bool, error) {
	out, err := r.git("ls-remote", "--heads", remote, ref)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// PushRef publishes ref to the remote.
//
// ENVMOVE_INTERNAL marks the push as ours so the repository's own pre-push hook
// becomes a no-op. Without it, envmove's push re-entered the hook, the hook synced,
// and the two recursed.
func (r *Repo) PushRef(remote, ref string) error {
	_, err := r.gitEnv([]string{"ENVMOVE_INTERNAL=1"}, "push", "--quiet", remote, ref+":"+ref)
	return err
}

// ---------------------------------------------------------------- inspection

// IgnoredPaths lists what git is neither tracking nor including per .gitignore.
//
// --directory collapses ignored directories (node_modules/) into a single entry so
// that scanning a repo with a large dependency tree stays fast.
func (r *Repo) IgnoredPaths() ([]string, error) {
	out, err := r.git("ls-files", "--others", "--ignored", "--exclude-standard", "--directory")
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			paths = append(paths, line)
		}
	}
	return paths, nil
}
