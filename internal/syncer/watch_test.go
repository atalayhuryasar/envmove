package syncer

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A commit that touched only code must not cause a snapshot commit.
//
// This is the invariant behind the fast path, and it is testable without a network: if
// nothing local changed, Push has nothing to publish and must not write a commit. An
// empty commit on every keystroke-turn would be worse than no fast path at all.
func TestPushWithNoLocalChangesWritesNoCommit(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")

	s := h.A(t)
	if _, _, err := s.Push(); err != nil {
		t.Fatal(err)
	}
	repo := h.A(t).repo
	before := repo.RefCommit(s.Ref())
	if before == "" {
		t.Fatal("first push should have created the branch")
	}

	// Nothing changed in the working tree, which is what a code-only commit looks like.
	if s.localDiffersFromBaseline() {
		t.Error("nothing changed locally, so nothing should need publishing")
	}
	_, published, err := s.Push()
	if err != nil {
		t.Fatal(err)
	}
	if published {
		t.Error("a push with nothing to publish should not publish")
	}
	if after := repo.RefCommit(s.Ref()); after != before {
		t.Errorf("the snapshot branch moved with nothing to publish: %v -> %v", before, after)
	}
}

// A local edit must still be published, otherwise the fast path is just a bug.
func TestPushWithLocalChangesDoesPublish(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "SECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	writeFile(t, h.dirA, ".env", "SECRET=2\n")
	s := h.A(t)
	if !s.localDiffersFromBaseline() {
		t.Fatal("a changed file must be seen as needing publication")
	}
	if _, _, err := s.Push(); err != nil {
		t.Fatal(err)
	}

	fresh, err := h.A(t).ReadSnapshot(s.Ref())
	if err != nil {
		t.Fatal(err)
	}
	if got := string(fresh.Files[".env"].Content); !strings.Contains(got, "SECRET=2") {
		t.Errorf("the change did not reach the snapshot: %q", got)
	}
}

// The baseline has to survive the round trip through disk, or every sync looks like a
// local change and the fast path can never engage. This is the trap: content is
// contracted on the way into the snapshot and expanded on the way back out, and
// contract(expand(x)) is not automatically x.
func TestBaselineSurvivesTheRoundTrip(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "ROOT="+h.dirA+"\nSECRET=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	h.A(t).Push()

	// A second machine pulls it, which rewrites its working tree from the snapshot.
	git(t, h.base, "clone", "--quiet", h.remote, h.dirB)
	h.A(t).Push()
	if _, _, err := h.B(t).Pull(); err != nil {
		t.Fatal(err)
	}

	s := h.B(t)
	if s.localDiffersFromBaseline() {
		baseline := s.LoadBaseline()
		local, _ := s.Collect()
		for path, entry := range local.Files {
			if b, ok := baseline.Files[path]; ok && b.SHA256 != entry.SHA256 {
				t.Errorf("%s: baseline digest %s, working tree %s", path, b.SHA256, entry.SHA256)
				t.Logf("  baseline: %q", b.Content)
				t.Logf("  local:    %q", entry.Content)
			}
		}
		t.Fatal("a freshly pulled machine should look unchanged, otherwise every sync re-publishes")
	}
}

// Watch publishes after a quiet period. The stamps it compares are local, so this needs
// no network and no clock of its own beyond the debounce.
func TestStampsSeeALocalEdit(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "A=1\n")
	s := h.A(t)

	before := s.stamps()
	if len(before) != 1 {
		t.Fatalf("stamps = %v", before)
	}
	writeFile(t, h.dirA, ".env", "A=2\n")
	after := s.stamps()

	if sameStamps(before, after) {
		t.Error("an edit should change the stamps")
	}
	if !sameStamps(after, s.stamps()) {
		t.Error("an untouched file must keep its stamps, otherwise watch would never settle")
	}
}

func TestStampsIgnoreMissingFiles(t *testing.T) {
	h := newHarness(t)
	writeFile(t, h.dirA, ".env", "A=1\n")
	writeFile(t, h.dirA, "HANDOVER.md", "a\n")
	s := h.A(t)

	if err := os.Remove(filepath.Join(h.dirA, "HANDOVER.md")); err != nil {
		t.Fatal(err)
	}
	if got := s.stamps(); len(got) != 1 {
		t.Errorf("a deleted file should drop out of the stamps, got %v", got)
	}
}
