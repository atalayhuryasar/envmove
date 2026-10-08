package context

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/atalayhuryasar/envmove/internal/syncer"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestBriefReadsHandoverAndTasks(t *testing.T) {
	root := t.TempDir()
	write(t, root, "HANDOVER.md", "# Handover\n\nretry mantigi yarim\n")
	write(t, root, "TODO.md", "# Todo\n- [ ] bir\n- [x] iki\n- [ ] uc\n")

	b := Build(root, &syncer.Diff{})

	if !strings.Contains(b.WhereLeft, "yarim") {
		t.Errorf("WhereLeft = %q", b.WhereLeft)
	}
	if len(b.OpenTasks) != 2 {
		t.Errorf("OpenTasks = %v, two unfinished tasks were expected", b.OpenTasks)
	}
}

func TestTasksAreNotDuplicatedAcrossFiles(t *testing.T) {
	root := t.TempDir()
	// macOS filesystems are case-insensitive, so these glob patterns can resolve to
	// the same file. A task repeated in two planning files is still one task.
	write(t, root, "TODO.md", "- [ ] retry\n")
	write(t, root, "PLAN.md", "- [ ] retry\n- [ ] baska\n")

	b := Build(root, &syncer.Diff{})
	seen := map[string]int{}
	for _, task := range b.OpenTasks {
		seen[task]++
	}
	for task, n := range seen {
		if n > 1 {
			t.Errorf("task %q listed %d times", task, n)
		}
	}
	if len(b.OpenTasks) != 2 {
		t.Errorf("OpenTasks = %v, two distinct tasks were expected", b.OpenTasks)
	}
}

func TestMarkdownOmitsAppliedIncomingFiles(t *testing.T) {
	root := t.TempDir()
	write(t, root, "HANDOVER.md", "bir sey\n")

	// Incoming files have already been written to disk by the time the briefing runs.
	// Reporting them as a remaining difference would be describing a state that is over.
	d := &syncer.Diff{Changes: []syncer.Change{
		{Path: ".env", Kind: syncer.Incoming},
		{Path: "TODO.md", Kind: syncer.Outgoing},
	}}
	md := Build(root, d).Markdown(time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC))

	if strings.Contains(md, ".env") {
		t.Errorf("an incoming file that was already applied is shown as a difference:\n%s", md)
	}
	if !strings.Contains(md, "TODO.md") {
		t.Errorf("a file waiting to be pushed is missing from the briefing:\n%s", md)
	}
	if !strings.Contains(md, "1 files waiting to be pushed") {
		t.Errorf("the number of pending files is missing:\n%s", md)
	}
}

func TestOversizedHandoverIsTruncatedHonestly(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("satir\n", 2000)
	write(t, root, "HANDOVER.md", long)

	md := Build(root, &syncer.Diff{}).Markdown(time.Now())
	if len(md) > maxBriefFileBytes+600 {
		t.Errorf("briefing is too long: %d bytes", len(md))
	}
	if !strings.Contains(md, "truncated") {
		t.Error("truncated content must be marked, not cut silently")
	}
}

func TestConflictsAreSurfaced(t *testing.T) {
	root := t.TempDir()
	write(t, root, "HANDOVER.md", "x\n")
	d := &syncer.Diff{Conflicts: []string{".env"}}
	md := Build(root, d).Markdown(time.Now())
	if !strings.Contains(md, "conflicts") || !strings.Contains(md, ".env") {
		t.Errorf("conflicts are missing from the briefing:\n%s", md)
	}
}

func TestEmptyRepoStillProducesABriefing(t *testing.T) {
	root := t.TempDir()
	md := Build(root, &syncer.Diff{}).Markdown(time.Now())
	if !strings.Contains(md, "session briefing") {
		t.Errorf("a briefing should be produced even for an empty repo:\n%s", md)
	}
}

// A snapshot that cannot be decrypted leaves Pull with nothing to report. That happens
// on a machine that has just joined, which is exactly the session that must not crash.
func TestNilDiffStillProducesABriefing(t *testing.T) {
	root := t.TempDir()
	write(t, root, "HANDOVER.md", "x\n")

	md := Build(root, nil).Markdown(time.Now())
	if !strings.Contains(md, "session briefing") {
		t.Errorf("nil diff should still produce a briefing:\\n%s", md)
	}
}
