package syncer

import (
	"strings"

	"filippo.io/age"

	"github.com/atalayhuryasar/envmove/internal/config"
	"testing"
	"time"
)

func TestRestoreGoesBackToAnEarlierSnapshot(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=good\n")
	writeFile(t, h.dirA, "HANDOVER.md", "v1\n")
	h.A(t).Push()

	writeFile(t, h.dirA, ".env", "SECRET=broken\n")
	writeFile(t, h.dirA, "HANDOVER.md", "v2 broken\n")
	h.A(t).Push()

	points, err := h.A(t).RestorePoints(5)
	if err != nil {
		t.Fatal(err)
	}
	if len(points) < 2 {
		t.Fatalf("snapshot count = %d, at least 2 were expected", len(points))
	}

	// Going back must not need --force: the state being overwritten is already on the
	// branch, so nothing is actually lost.
	if _, err := h.A(t).Restore(points[1].Commit, false); err != nil {
		t.Fatalf("restore should not have required --force: %v", err)
	}
	if got := readFile(t, h.dirA, ".env"); got != "SECRET=good\n" {
		t.Errorf(".env = %q, the earlier state was expected", got)
	}
	if got := readFile(t, h.dirA, "HANDOVER.md"); got != "v1\n" {
		t.Errorf("HANDOVER.md = %q", got)
	}
}

func TestRestoreRefusesToDiscardUnpushedWork(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=one\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	points, err := h.A(t).RestorePoints(5)
	if err != nil {
		t.Fatal(err)
	}

	// Written after the last push, so it exists nowhere else.
	writeFile(t, h.dirA, ".env", "SECRET=never-pushed\n")

	lost, err := h.A(t).Restore(points[0].Commit, false)
	if err == nil {
		t.Fatal("unpushed work must not be overwritten")
	}
	if len(lost) != 1 || lost[0] != ".env" {
		t.Errorf("lost = %v, [.env] was expected", lost)
	}
	if !strings.Contains(err.Error(), "never pushed") {
		t.Errorf("the error should say what is protected: %v", err)
	}
	if got := readFile(t, h.dirA, ".env"); got != "SECRET=never-pushed\n" {
		t.Errorf("the file was overwritten: %q", got)
	}

	// With --force it goes through.
	if _, err := h.A(t).Restore(points[0].Commit, true); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, h.dirA, ".env"); got != "SECRET=one\n" {
		t.Errorf(".env after --force = %q", got)
	}
}

func TestResolveRefAcceptsRelativeAge(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	s := h.A(t)
	head, err := s.ResolveRef("")
	if err != nil {
		t.Fatal(err)
	}
	if head == "" {
		t.Fatal("an empty ref should give the current commit")
	}
	if _, err := s.ResolveRef("1h"); err != nil {
		t.Errorf("1h should have resolved: %v", err)
	}
	if _, err := s.ResolveRef("not-a-ref"); err == nil {
		t.Error("an invalid ref should have failed")
	}
}

func TestRotateInvalidatesTheOldKey(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=kept\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	if _, _, err := h.A(t).Push(); err != nil {
		t.Fatal(err)
	}
	ref := "refs/heads/" + config.DefaultBranch

	oldRecipient := h.keyA.Recipient().String()
	res, err := h.A(t).Rotate(RotateOptions{})
	if err != nil {
		t.Fatal(err)
	}

	// The identity that was in use must no longer open anything written from now on.
	stale, err := cryptWith(h.keyA, []*age.X25519Recipient{h.keyA.Recipient()})
	if err != nil {
		t.Fatal(err)
	}
	stale.repo = h.A(t).repo
	stale.cfg = h.A(t).cfg
	if _, err := stale.ReadSnapshot(ref); err == nil {
		t.Error("the rotated key can still read the snapshot")
	}

	// Its recipient entry is gone, the new one is present, the other machine's is kept.
	if len(res.Dropped) != 1 || res.Dropped[0] != oldRecipient {
		t.Errorf("Dropped = %v, the old recipient %s was expected", res.Dropped, oldRecipient[:12])
	}
	if len(res.Kept) != 2 {
		t.Errorf("Kept = %v, the other machine plus the new key were expected", res.Kept)
	}
	if !containsString(res.Kept, res.NewRecipient) || !containsString(res.Kept, h.keyB.Recipient().String()) {
		t.Errorf("Kept = %v, should contain the new key and B", res.Kept)
	}
}

func TestRotateAllDropsEveryOtherRecipient(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=kept\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	res, err := h.A(t).Rotate(RotateOptions{AllRecipients: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Dropped) != 2 {
		t.Errorf("Dropped = %v, two old recipients were expected", res.Dropped)
	}
	if len(res.Kept) != 1 || res.Kept[0] != res.NewRecipient {
		t.Errorf("Kept = %v, only the new key was expected", res.Kept)
	}

	// A machine that used to be able to read is shut out. It is pointed at this
	// repository on purpose: the other clone has never fetched the branch, so reading
	// there would succeed vacuously.
	excluded := h.syncer(t, h.dirA, h.keyB, h.keyB.Recipient())
	if _, err := excluded.ReadSnapshot("refs/heads/" + config.DefaultBranch); err == nil {
		t.Error("a machine dropped from the list can still read")
	}
}

func TestNewKeyFromRotationCanReadTheReencryptedSnapshot(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=kept\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	// The CLI persists the identity through this callback; capturing it here stands in
	// for that, and lets the test check the key that was actually stored.
	var stored string
	var storedErr error
	s := h.A(t).WithIdentity("", func(identity string) error {
		stored = identity
		return nil
	})
	if _, err := s.Rotate(RotateOptions{AllRecipients: true}); err != nil {
		t.Fatal(err)
	}
	_ = storedErr
	if stored == "" {
		t.Fatal("the new key was not stored")
	}

	ids, err := age.ParseIdentities(strings.NewReader(stored))
	if err != nil || len(ids) == 0 {
		t.Fatalf("could not read the stored key: %v", err)
	}
	fresh := h.syncer(t, h.dirA, ids[0].(*age.X25519Identity), ids[0].(*age.X25519Identity).Recipient())
	st, err := fresh.ReadSnapshot("refs/heads/" + config.DefaultBranch)
	if err != nil {
		t.Fatalf("the new key should read the snapshot: %v", err)
	}
	if string(st.Files[".env"].Content) != "SECRET=kept\n" {
		t.Errorf("content corrupted: %q", st.Files[".env"].Content)
	}
}

func TestParseAge(t *testing.T) {
	if d, ok := parseAge("3h"); !ok || d != 3*time.Hour {
		t.Errorf("parseAge(3h) = %v, %v", d, ok)
	}
	if d, ok := parseAge("2d"); !ok || d != 48*time.Hour {
		t.Errorf("parseAge(2d) = %v, %v", d, ok)
	}
	for _, bad := range []string{"", "h", "0d", "-1h", "abc", "10"} {
		if _, ok := parseAge(bad); ok {
			t.Errorf("parseAge(%q) kabul edildi, reddedilmeliydi", bad)
		}
	}
}
