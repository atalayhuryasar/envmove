package detect

import (
	"os"
	"path/filepath"
	"testing"
)

// The whole design rests on this: the default is to carry. A file that matches no
// pattern at all still travels, because git already decided it is not part of the
// project and the previous model silently dropped it.
func TestUnmatchedGitignoredFilesStillTravel(t *testing.T) {
	root := t.TempDir()
	write(t, root, "some-tool/config.local", "a=1")
	write(t, root, "weird/name.dat", "x")

	for _, rel := range []string{"some-tool/config.local", "weird/name.dat"} {
		got := classify(root, rel, &ignoreList{})
		if got.Class != Carry {
			t.Errorf("classify(%q) = skip (%s), a gitignored file should travel", rel, got.Reason)
		}
	}
}

// A file that looks like nothing in particular is still useful to label as state.
func TestCarryReasonExplainsWhatItGuessed(t *testing.T) {
	root := t.TempDir()
	write(t, root, ".env", "A=1")
	write(t, root, ".superpowers/state.json", "{}")
	write(t, root, "local/config.yml", "a: 1")

	tests := map[string]string{
		".env":                    "secret",
		".superpowers/state.json": "agent state",
		"local/config.yml":        "local project state",
	}
	for rel, want := range tests {
		if got := classify(root, rel, &ignoreList{}); got.Reason != want {
			t.Errorf("classify(%q).Reason = %q, beklenen %q", rel, got.Reason, want)
		}
	}
}

// Subtracting junk is the entire safety argument for a permissive default, so every
// rule in it gets checked.
func TestJunkIsExcluded(t *testing.T) {
	root := t.TempDir()
	junk := []string{
		"node_modules/react/index.js",
		".next/server/app.js",
		"build/out.js",
		"dist/bundle.js",
		"target/debug/app",
		".venv/lib/python3.12/os.py",
		"__pycache__/x.pyc",
		".turbo/cache/x",
		"coverage/lcov.info",
		".DS_Store",
		"tsconfig.tsbuildinfo",
		"next-env.d.ts",
		"app.log",
		"debug.dump",
		"data.sqlite3",
		".vercel/project.json",
		".claude/settings.local.json",
	}
	for _, rel := range junk {
		write(t, root, rel, "x")
		got := classify(root, rel, &ignoreList{})
		if got.Class != Skip {
			t.Errorf("classify(%q) = carry, should be junk (%s)", rel, got.Reason)
		} else if got.Reason == "" {
			t.Errorf("classify(%q) was skipped without a reason", rel)
		}
	}
}

// The baseline is the one thing that must never travel, and the reason is correctness
// rather than tidiness: the receiving machine would end up comparing against another
// machine's ancestor.
func TestOwnStateIsNeverCarried(t *testing.T) {
	root := t.TempDir()
	for _, rel := range []string{".envmove/baseline.age", ".envmove/anything"} {
		write(t, root, rel, "x")
		got := classify(root, rel, &ignoreList{})
		if got.Class != Skip {
			t.Errorf("%q must never travel", rel)
		}
		if got.Reason != "envmove's own state" {
			t.Errorf("%v sebebi = %q", rel, got.Reason)
		}
	}
}

// A permissive default has to stop somewhere, and silently skipping a 400 MB local
// database while reporting success would be the worst possible outcome.
func TestOversizedFilesAreSkippedAndExplained(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "big.bin")
	if err := os.WriteFile(path, make([]byte, MaxFileBytes+1), 0o644); err != nil {
		t.Fatal(err)
	}
	got := classify(root, "big.bin", &ignoreList{})
	if got.Class != Skip {
		t.Fatal("oversized file should not travel")
	}
	if got.Size <= MaxFileBytes {
		t.Errorf("boyut bildirilmedi: %d", got.Size)
	}
}

func TestEnvmoveignoreIsADenyList(t *testing.T) {
	root := t.TempDir()
	write(t, root, "keep.conf", "a=1")
	write(t, root, "secret-but-mine.key", "x")
	write(t, root, ".envmoveignore", "# comment\n\nsecret-but-mine.key\nvendor-thing/\n")

	list, err := loadIgnoreList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !list.matches("secret-but-mine.key") {
		t.Error("the listed file did not match")
	}
	if !list.matches("vendor-thing/inner/file") {
		t.Error("a directory entry must match files inside it")
	}
	if list.matches("keep.conf") {
		t.Error("an unlisted file must not match")
	}
	if got := classify(root, "secret-but-mine.key", list); got.Class != Skip {
		t.Errorf("envmoveignore was ignored, the file still travels: %v", got)
	}
	// The same file travels when it is not denied.
	if got := classify(root, "secret-but-mine.key", &ignoreList{}); got.Class != Carry {
		t.Errorf("without the deny list the file should travel: %v", got)
	}
}

func TestMatchPath(t *testing.T) {
	tests := []struct {
		rel, pattern string
		want         bool
	}{
		{".env", ".env", true},
		{".env.local", ".env.*", true},
		{"notes/plan.md", "notes/**", true},
		{"notesX/a.md", "notes/**", false},
		// * must not cross a separator, or .env.* would swallow src/.env.prod
		{"src/main.go", "*.env", false},
		{".claude/settings.local.json", ".claude/settings.local.json", true},
		{"nested/.claude/settings.local.json", "**/settings.local.json", true},
	}
	for _, tc := range tests {
		if got := matchPath(tc.rel, tc.pattern); got != tc.want {
			t.Errorf("matchPath(%q, %q) = %v, beklenen %v", tc.rel, tc.pattern, got, tc.want)
		}
	}
}

// Segment matching is what keeps a project named "build-tools" from being treated as a
// build directory.
func TestJunkDirsMatchWholeSegmentsOnly(t *testing.T) {
	root := t.TempDir()
	write(t, root, "build-tools/package.json", "{}")
	write(t, root, "build/out.js", "x")

	if got := classify(root, "build-tools/package.json", &ignoreList{}); got.Class != Carry {
		t.Errorf("build-tools/ must not count as junk: %v", got)
	}
	if got := classify(root, "build/out.js", &ignoreList{}); got.Class != Skip {
		t.Errorf("build/ must count as junk: %v", got)
	}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
