package syncer

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/crypt"
	"github.com/atalayhuryasar/envmove/internal/gitx"
)

// harness is two working copies sharing one bare remote, standing in for two
// machines. Keys live in memory so the test never touches the keychain.
type harness struct {
	base   string
	remote string
	dirA   string
	dirB   string
	// keyA and keyB stand in for the per-machine recipients. Public keys are shared
	// immediately, which is what a real second machine does after setup commits.
	keyA, keyB *age.X25519Identity
}

var allFiles = []string{".env", "HANDOVER.md"}

func newHarness(t *testing.T) *harness {
	t.Helper()
	base := t.TempDir()
	h := &harness{
		base:   base,
		remote: filepath.Join(base, "remote.git"),
		dirA:   filepath.Join(base, "A"),
		dirB:   filepath.Join(base, "B"),
		keyA:   newKey(t),
		keyB:   newKey(t),
	}
	git(t, "", "init", "--quiet", "--bare", h.remote)

	// Both machines start from the same published config naming both recipients.
	h.seed(t)
	return h
}

func newKey(t *testing.T) *age.X25519Identity {
	t.Helper()
	id, err := crypt.GenerateIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (h *harness) seed(t *testing.T) {
	t.Helper()
	git(t, "", "clone", "--quiet", h.remote, h.dirA)
	writeFile(t, h.dirA, ".gitignore", ".env\nHANDOVER.md\n")
	writeFile(t, h.dirA, "main.py", "print(1)\n")
	git(t, h.dirA, "add", "-A")
	git(t, h.dirA, "commit", "--quiet", "-m", "init")
	git(t, h.dirA, "push", "--quiet", "-u", "origin", "HEAD:refs/heads/main")
}

func (h *harness) syncer(t *testing.T, dir string, id *age.X25519Identity, recipients ...*age.X25519Recipient) *Syncer {
	t.Helper()
	repo, err := gitx.OpenAt(dir)
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, len(recipients))
	for i, r := range recipients {
		keys[i] = r.String()
	}
	cipher, err := crypt.New(keys, []age.Identity{id})
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Schema:     config.SchemaVersion,
		Branch:     config.DefaultBranch,
		Paths:      allFiles,
		Recipients: keys,
	}
	s := New(repo, cfg, cipher)
	// Mirror the real CLI, which tells the syncer which recipient belongs to this
	// machine so that a rotation can recognise and replace its own entry.
	s.holder = id.Recipient().String()
	return s
}

// A is the machine that already has state.
func (h *harness) A(t *testing.T) *Syncer {
	return h.syncer(t, h.dirA, h.keyA, h.keyA.Recipient(), h.keyB.Recipient())
}

// B is a freshly set up machine that knows both recipients but has nothing yet.
func (h *harness) B(t *testing.T) *Syncer {
	return h.syncer(t, h.dirB, h.keyB, h.keyA.Recipient(), h.keyB.Recipient())
}

func TestPullDeliversFilesToSecondMachine(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "yarim\n")
	if _, _, err := h.A(t).Push(); err != nil {
		t.Fatal(err)
	}

	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	if _, _, err := h.A(t).Push(); err != nil {
		t.Fatal(err)
	}

	d, written, err := h.B(t).Pull()
	if err != nil {
		t.Fatal(err)
	}
	if len(written) != 2 {
		t.Fatalf("expected 2 files, got %d: %v", len(written), written)
	}
	if len(d.Conflicts) != 0 {
		t.Fatalf("the first pull must not report conflicts, got %v", d.Conflicts)
	}
	if got := readFile(t, h.dirB, ".env"); got != "SECRET=1\n" {
		t.Errorf(".env = %q", got)
	}
	if got := readFile(t, h.dirB, "HANDOVER.md"); got != "yarim\n" {
		t.Errorf("HANDOVER.md = %q", got)
	}
}

func TestUnrelatedLocalEditIsOutgoingNotConflict(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	h.A(t).Push()
	h.B(t).Pull()

	// B changes only .env. HANDOVER.md is untouched on both sides and must not be
	// reported as a disagreement.
	writeFile(t, h.dirB, ".env", "SECRET=2\n")
	d, _, err := h.B(t).Push()
	if err != nil {
		t.Fatalf("a one-sided change must not count as a conflict: %v", err)
	}
	if len(d.Conflicts) != 0 {
		t.Errorf("no conflicts were expected: %v", d.Conflicts)
	}
	if len(d.Outgoing) != 1 || d.Outgoing[0] != ".env" {
		t.Errorf("Outgoing = %v, beklenen [.env]", d.Outgoing)
	}
}

func TestSameFileChangedOnBothSidesConflicts(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	h.A(t).Push()
	h.B(t).Pull()

	writeFile(t, h.dirA, ".env", "SECRET=one\n")
	writeFile(t, h.dirB, ".env", "SECRET=two\n")

	if _, _, err := h.A(t).Push(); err != nil {
		t.Fatal(err)
	}
	d, _, err := h.B(t).Pull()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Conflicts) != 1 || d.Conflicts[0] != ".env" {
		t.Fatalf("Conflicts = %v, beklenen [.env]", d.Conflicts)
	}
	// The local file must be left exactly as it was.
	if got := readFile(t, h.dirB, ".env"); got != "SECRET=two\n" {
		t.Errorf("the local file was overwritten during a conflict: %q", got)
	}
	// And pushing must be refused rather than silently discarding a side.
	if _, _, err := h.B(t).Push(); err == nil {
		t.Error("a push should have been refused while a conflict exists")
	}
}

func TestPathInContentIsRewrittenPerMachine(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "ROOT="+h.dirA+"\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	h.A(t).Push()
	h.B(t).Pull()

	// B must see its own path, not the one A wrote.
	want := "ROOT=" + mustEval(t, h.dirB) + "\n"
	if got := readFile(t, h.dirB, ".env"); got != want {
		t.Errorf("B .env = %q, beklenen %q", got, want)
	}
	// B's push may be a no-op: after contraction both sides hold the same placeholder.
	// What matters is that A keeps a path that is valid on A.
	h.B(t).Push()
	h.A(t).Pull()
	gotA := strings.TrimSpace(strings.TrimPrefix(readFile(t, h.dirA, ".env"), "ROOT="))
	if resolved := mustEval(t, gotA); resolved != mustEval(t, h.dirA) {
		t.Errorf("A .env yolu = %q (resolved %q), beklenen %q", gotA, resolved, mustEval(t, h.dirA))
	}
}

func TestDeletionIsOutgoing(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()
	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	h.A(t).Push()
	h.B(t).Pull()

	if err := os.Remove(filepath.Join(h.dirB, "HANDOVER.md")); err != nil {
		t.Fatal(err)
	}
	d, _, err := h.B(t).Push()
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Conflicts) != 0 {
		t.Fatalf("a deletion must not be a conflict: %v", d.Conflicts)
	}
	if len(d.Outgoing) != 1 || d.Outgoing[0] != "HANDOVER.md" {
		t.Errorf("Outgoing = %v, beklenen [HANDOVER.md]", d.Outgoing)
	}

	h.A(t).Pull()
	if _, err := os.Stat(filepath.Join(h.dirA, "HANDOVER.md")); !os.IsNotExist(err) {
		t.Error("the deletion did not reach the other side")
	}
}

func TestSnapshotIsEncryptedOnTheBranch(t *testing.T) {
	h := newHarness(t)
	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	writeFile(t, h.dirA, ".env", "SUPERSECRET=hunter2\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	repo, err := gitx.OpenAt(h.dirA)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := repo.ReadBlob("refs/heads/"+config.DefaultBranch, config.BlobPath)
	if err != nil {
		t.Fatal(err)
	}
	if containsBytes(blob, []byte("SUPERSECRET")) || containsBytes(blob, []byte("hunter2")) {
		t.Error("the snapshot shows its content in plaintext")
	}
	// A machine that holds none of the recipient keys must not be able to read it.
	// It is bound to A's repository, which is the one that actually has the branch.
	outsiderKey := newKey(t)
	outsider := h.syncer(t, h.dirA, outsiderKey, outsiderKey.Recipient())
	if _, err := outsider.ReadSnapshot("refs/heads/" + config.DefaultBranch); err == nil {
		t.Error("a machine without the key could read the snapshot")
	}
	// And machine B, which is a listed recipient, must succeed.
	if _, err := h.B(t).ReadSnapshot("refs/heads/" + config.DefaultBranch); err != nil {
		t.Errorf("a listed recipient could not read: %v", err)
	}
}

// ---------------------------------------------------------------- helpers

func git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@envmove.local",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@envmove.local",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	abs := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, dir, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func mustEval(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	return resolved
}

func containsBytes(haystack, needle []byte) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) &&
		bytesIndex(haystack, needle) >= 0
}

func bytesIndex(h, n []byte) int {
	for i := 0; i+len(n) <= len(h); i++ {
		match := true
		for j := range n {
			if h[i+j] != n[j] {
				match = false
				break
			}
		}
		if match {
			return i
		}
	}
	return -1
}

// cryptWith builds a Cipher from one identity, used by tests that need to pose as a
// machine holding a particular key.
func cryptWith(id *age.X25519Identity, recipients []*age.X25519Recipient) (*Syncer, error) {
	keys := make([]string, len(recipients))
	for i, r := range recipients {
		keys[i] = r.String()
	}
	c, err := crypt.New(keys, []age.Identity{id})
	if err != nil {
		return nil, err
	}
	return &Syncer{cipher: c}, nil
}
