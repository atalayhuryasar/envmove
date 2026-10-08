// Package detect finds the files worth syncing: the ones git tracks nowhere because
// they are gitignored, but which still describe the project.
//
// Detection is automatic on purpose. There is no configuration language; `envmove
// setup` shows what it found and lets the user say yes or no, and `envmove add` is
// the only supported way to add a path later.
package detect

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/atalayhuryasar/envmove/internal/gitx"
)

type Class string

const (
	Sync  Class = "sync"
	Local Class = "local"
)

type Candidate struct {
	Path   string // relative to the repository root
	Class  Class
	Reason string
}

// matchPath supports a leading "dir/**" form for directories; everything else uses
// shell glob semantics, where * does not cross a path separator.
func matchPath(rel, pattern string) bool {
	if rest, ok := strings.CutSuffix(pattern, "/**"); ok {
		return rel == rest || strings.HasPrefix(rel, rest+"/")
	}
	ok, err := filepath.Match(pattern, rel)
	return err == nil && ok
}

func matchAny(rel string, patterns []string) (string, bool) {
	for _, p := range patterns {
		if matchPath(rel, p) {
			return p, true
		}
	}
	return "", false
}

var secretPatterns = []string{
	".env", ".env.*", "*.env",
	"*.pem", "*.key", "*.p12", "*.pfx",
	"credentials.json", "credentials.*",
	".npmrc", ".netrc", ".pypirc",
	"*.secret", "secrets.json",
}

// Files that belong to the machine rather than the project. Agent hooks are written
// into .claude/settings.local.json precisely because that file is never committed, so
// syncing it would carry a path that only resolves on the machine that wrote it.
var localOnly = []string{
	".claude/settings.local.json",
	".cursor/rules/envmove.mdc",
	"**/settings.local.json",
}

func isLocalOnly(rel string) bool {
	for _, pattern := range localOnly {
		if strings.HasSuffix(pattern, "**") {
			base := strings.TrimSuffix(pattern, "**")
			if strings.HasSuffix(rel, strings.TrimPrefix(base, "/")) {
				return true
			}
			continue
		}
		if rel == pattern {
			return true
		}
	}
	return false
}

var agentPatterns = []string{
	"AGENTS.md", "CLAUDE.md", "GEMINI.md",
	".aider.conf.yml", ".aider*",
	"HANDOVER*", "*HANDOVER*.md",
	"*ROADMAP*", "*PLAN*.md", "*PROGRESS*", "*NOTES*.md",
	"TODO*", "TASKS*",
	".claude/**", ".cursor/**", ".opencode/**",
	"notes/**", "tasks/**", "decisions/**",
}

// Ignored directories that we still want to descend into. Everything else that
// ls-files collapsed (node_modules/, .venv/, dist/) is skipped wholesale, which is
// what keeps scanning fast.
var expandDirs = []string{
	".claude", ".cursor", ".opencode", "notes", "tasks", "decisions",
}

var internalPrefixes = []string{".envmove", ".git/"}

// Scan classifies every gitignored path in the repository.
func Scan(repo *gitx.Repo) ([]Candidate, error) {
	ignored, err := repo.IgnoredPaths()
	if err != nil {
		return nil, err
	}

	var out []Candidate
	seen := map[string]bool{}

	add := func(rel string) {
		if seen[rel] {
			return
		}
		seen[rel] = true
		if c, ok := classify(rel); ok {
			out = append(out, c)
		}
	}

	for _, p := range ignored {
		if isInternal(p) {
			continue
		}
		if strings.HasSuffix(p, "/") {
			dir := strings.TrimSuffix(p, "/")
			_, descend := matchAny(dir, expandDirs)
			if !descend {
				continue // collapsed build artifact; nothing in here is ours
			}
			files, err := walkFiles(repo.Root, dir)
			if err != nil {
				return nil, err
			}
			for _, f := range files {
				add(f)
			}
			continue
		}
		add(p)
	}
	return out, nil
}

func isInternal(rel string) bool {
	for _, prefix := range internalPrefixes {
		if rel == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func classify(rel string) (Candidate, bool) {
	if isLocalOnly(rel) {
		return Candidate{Path: rel, Class: Local, Reason: "machine-local, not carried"}, false
	}
	if pattern, ok := matchAny(rel, secretPatterns); ok {
		return Candidate{Path: rel, Class: Sync, Reason: "secret — " + pattern}, true
	}
	if pattern, ok := matchAny(rel, agentPatterns); ok {
		return Candidate{Path: rel, Class: Sync, Reason: "agent state — " + pattern}, true
	}
	return Candidate{Path: rel, Class: Local, Reason: "leftover, no rule matched"}, false
}

func walkFiles(root, relDir string) ([]string, error) {
	base := filepath.Join(root, relDir)
	var files []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err // unreadable entry: skip rather than abort the whole scan
		}
		if d.IsDir() {
			if path != base && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		r, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		files = append(files, filepath.ToSlash(r))
		return nil
	})
	return files, err
}

func skipDir(name string) bool {
	return name == "node_modules" || (strings.HasPrefix(name, ".") && name != "." && name != "..")
}
