package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/atalayhuryasar/envmove/internal/envfile"
	"github.com/atalayhuryasar/envmove/internal/gitx"
)

// exampleNames are the filenames treated as the published template for .env.
var exampleNames = []string{".env.example", "env.example", ".env.sample", ".env.template"}

// refreshExample keeps .env.example in step with .env.
//
// It only ever rewrites a file git is already tracking, and only rewrites it when the
// content actually differs, so a clean repository does not produce a diff on every
// single sync.
func refreshExample(repo *gitx.Repo) (envfile.Result, bool, error) {
	var result envfile.Result
	const changed = false

	realPath := filepath.Join(repo.Root, ".env")
	real, err := os.ReadFile(realPath)
	if err != nil {
		return result, false, nil
	}

	for _, name := range exampleNames {
		examplePath := filepath.Join(repo.Root, name)
		previous, err := os.ReadFile(examplePath)
		if err != nil {
			continue
		}
		if !repo.IsTracked(name) {
			continue
		}
		result = envfile.Build(string(real), string(previous))
		if result.Content == string(previous) {
			return result, false, nil
		}
		if err := os.WriteFile(examplePath, []byte(result.Content), 0o644); err != nil {
			return result, false, err
		}
		return result, true, nil
	}
	return result, changed, nil
}

func reportExample(result envfile.Result, changed bool, quiet bool) {
	if !quiet && changed {
		if len(result.Added) > 0 {
			fmt.Printf("· %s updated (+%d: %s)\n", exampleLabel(result), len(result.Added), join(result.Added))
		}
		if len(result.Removed) > 0 {
			fmt.Printf("· %s updated (−%d: %s)\n", exampleLabel(result), len(result.Removed), join(result.Removed))
		}
	}
}

func exampleLabel(envfile.Result) string { return ".env.example" }

func join(items []string) string {
	const limit = 4
	if len(items) <= limit {
		out := ""
		for i, s := range items {
			if i > 0 {
				out += ", "
			}
			out += s
		}
		return out
	}
	out := ""
	for _, s := range items[:limit] {
		out += s + ", "
	}
	return out + fmt.Sprintf("+%d", len(items)-limit)
}

// suggestExample reports a missing example file rather than creating one.
//
// It deliberately does not ask git whether .env is tracked: .env is gitignored by
// definition, so that check would make the suggestion impossible to trigger.
func suggestExample(repo *gitx.Repo) string {
	realPath := filepath.Join(repo.Root, ".env")
	if _, err := os.Stat(realPath); err != nil {
		return ""
	}
	for _, name := range exampleNames {
		if _, err := os.Stat(filepath.Join(repo.Root, name)); err == nil {
			return ""
		}
	}
	keys := envfile.Keys(readAll(realPath))
	if len(keys) == 0 {
		return ""
	}
	return fmt.Sprintf(".env has %d variables but there is no example file — nobody will know how to set it up", len(keys))
}

func readAll(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}
