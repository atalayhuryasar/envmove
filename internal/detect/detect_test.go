package detect

import "testing"

func TestMatchPath(t *testing.T) {
	tests := []struct {
		rel, pattern string
		want         bool
	}{
		{".env", ".env", true},
		{".env.local", ".env.*", true},
		{"HANDOVER.md", "HANDOVER*", true},
		{"notes/plan.md", "notes/**", true},
		{"notes", "notes/**", true},
		{"notes/deep/a.md", "notes/**", true},
		{"notesX/a.md", "notes/**", false},
		// * must not cross a path separator, or .env.* would swallow src/.env.prod
		{"src/main.go", "*.env", false},
		{".env.prod", "*.env", false},
	}
	for _, tc := range tests {
		if got := matchPath(tc.rel, tc.pattern); got != tc.want {
			t.Errorf("matchPath(%q, %q) = %v, beklenen %v", tc.rel, tc.pattern, got, tc.want)
		}
	}
}

func TestClassify(t *testing.T) {
	tests := []struct {
		rel  string
		sync bool
	}{
		{".env", true},
		{".env.production", true},
		{"server.pem", true},
		{"AGENTS.md", true},
		{"HANDOVER.md", true},
		{"TODO.md", true},
		{".claude/settings.json", true},
		{".cursor/rules/x.mdc", true},
		{"notes/today.md", true},
		// Machine-local files must never travel, or a hook path would be carried to a
		// machine where it does not resolve.
		{".claude/settings.local.json", false},
		{"main.py", false},
		{"envmove.toml", false},
		{"package.json", false},
	}
	for _, tc := range tests {
		got, ok := classify(tc.rel)
		if ok != tc.sync {
			kind := "sync"
			if !ok {
				kind = "local"
			}
			t.Errorf("classify(%q) = %s, beklenen %s", tc.rel, kind, map[bool]string{true: "sync", false: "local"}[tc.sync])
		}
		_ = got
	}
}

func TestInternalPathsAreSkipped(t *testing.T) {
	if !isInternal(".envmove/baseline.age") {
		t.Error("the baseline is envmove's own state and must not be scanned")
	}
	if isInternal(".env") {
		t.Error(".env must not count as internal")
	}
}

func TestSkipDir(t *testing.T) {
	if !skipDir("node_modules") {
		t.Error("node_modules must be skipped")
	}
	if !skipDir(".venv") {
		t.Error("dot directories must be skipped")
	}
	if skipDir("src") || skipDir("app") {
		t.Error("ordinary directories must not be skipped")
	}
}
