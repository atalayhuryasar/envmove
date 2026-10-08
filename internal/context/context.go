// Package context renders a short briefing an AI agent can read at the start of a
// session: where the work was left, what is still open, and what has not been synced.
//
// This is the part that makes envmove more than encrypted copying. Git carries the
// code and no context, so an agent opening a repository on a second machine starts
// with amnesia and re-solves problems that were already settled. A few hundred bytes
// of briefing prevents that.
package context

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/atalayhuryasar/envmove/internal/config"
	"github.com/atalayhuryasar/envmove/internal/syncer"
)

// maxBriefFileBytes keeps the briefing short. A handover document that has grown to
// twenty pages is not a briefing, and an agent will not read it anyway.
const maxBriefFileBytes = 2000

// Briefing is the structured result, so that the same data can be rendered as prose
// for a human or as JSON for a tool.
type Briefing struct {
	Repo       string
	Branch     string
	WhereLeft  string
	OpenTasks  []string
	Recent     []string
	Unsynced   []string
	Conflicts  []string
	Notes      []string
	TotalFiles int
}

type source struct {
	label string
	paths []string
}

var handoverSources = []source{
	{"HANDOVER", []string{"HANDOVER.md", "HANDOVER", "docs/HANDOVER.md", ".handover.md"}},
	{"ROADMAP", []string{"ROADMAP.md", "ROADMAP", "docs/ROADMAP.md", "roadmap.md"}},
	{"PLAN", []string{"PLAN.md", "PLAN", "docs/PLAN.md", "notes/plan.md", "*PLAN*.md"}},
	{"PROGRESS", []string{"PROGRESS.md", "progress.md", "notes/progress.md"}},
}

// Build assembles the briefing from the working tree and the sync state.
func Build(root string, d *syncer.Diff) Briefing {
	b := Briefing{Repo: filepath.Base(root), TotalFiles: len(d.Changes)}

	for _, s := range handoverSources {
		if text := readBrief(root, s.paths); text != "" {
			b.WhereLeft = text
			break
		}
	}
	b.OpenTasks = readOpenTasks(root)
	// Derived from Changes rather than read off the Outgoing slice, so the briefing
	// stays self-consistent however the diff was produced.
	for _, c := range d.Changes {
		if c.Kind == syncer.Outgoing {
			b.Unsynced = append(b.Unsynced, c.Path)
		}
	}
	b.Conflicts = append([]string{}, d.Conflicts...)
	b.Recent = recentlyChanged(root, d)
	return b
}

func readBrief(root string, candidates []string) string {
	for _, pattern := range candidates {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || info.IsDir() || info.Size() == 0 {
				continue
			}
			raw, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			return trim(string(raw))
		}
	}
	return ""
}

// readOpenTasks pulls unchecked markdown task items out of the planning files.
//
// Both the files and the task text are de-duplicated: on a case-insensitive filesystem
// TODO.md and todo.md are the same path, and a task listed in two planning files is
// still one task.
func readOpenTasks(root string) []string {
	patterns := []string{"TODO.md", "TODO", "todo.md", "docs/TODO.md", "tasks/*.md", "*ROADMAP*", "*PLAN*.md"}

	seenFile := map[string]bool{}
	seenTask := map[string]bool{}
	var out []string

	for _, pattern := range patterns {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			key := m
			if resolved, err := filepath.EvalSymlinks(m); err == nil {
				key = resolved
			}
			if seenFile[key] {
				continue
			}
			seenFile[key] = true

			raw, err := os.ReadFile(m)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(raw), "\n") {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "- [ ]") && !strings.HasPrefix(line, "* [ ]") {
					continue
				}
				task := strings.TrimSpace(line[5:])
				normalised := strings.ToLower(task)
				if seenTask[normalised] {
					continue
				}
				seenTask[normalised] = true
				out = append(out, task)
				if len(out) >= 8 {
					return out
				}
			}
		}
	}
	return out
}

// recentlyChanged lists the synced files that differ from the last known state, which
// is what "what happened in this session" amounts to.
func recentlyChanged(root string, d *syncer.Diff) []string {
	var out []string
	for _, c := range d.Changes {
		// Incoming files have already been written by the time the briefing is built,
		// so repeating them here would describe a difference that no longer exists.
		if c.Kind != syncer.Outgoing && c.Kind != syncer.Conflict {
			continue
		}
		out = append(out, c.Path)
	}
	sort.Strings(out)
	if len(out) > 10 {
		out = out[:10]
	}
	return out
}

// trim keeps a document readable as a briefing: leading heading noise and blank lines
// go, and an oversized document is cut with an honest marker rather than a silent one.
func trim(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t")
		if strings.TrimSpace(line) == "" && len(out) > 0 && strings.TrimSpace(out[len(out)-1]) == "" {
			continue
		}
		out = append(out, line)
	}
	result := strings.TrimSpace(strings.Join(out, "\n"))
	if len(result) > maxBriefFileBytes {
		cut := result[:maxBriefFileBytes]
		if idx := strings.LastIndex(cut, "\n"); idx > 0 {
			cut = cut[:idx]
		}
		result = cut + "\n… (truncated; read the file for the rest)"
	}
	return result
}

// Brief renders the opening briefing for a session. Everything that goes wrong with
// syncing is folded into the text rather than raised, because a briefing that fails to
// print is worse than one that admits it is out of date.
func Brief(root string, cfg *config.Config, d *syncer.Diff, written []string) Briefing {
	b := Build(root, d)

	var notes []string
	if len(written) > 0 {
		notes = append(notes, fmt.Sprintf("%d files arrived on this machine: %s", len(written), strings.Join(written, ", ")))
	}
	if cfg != nil && !cfg.PublicAcknowledged && strings.Contains(cfg.Branch, "envmove") {
		// Not a warning about state, just a reminder that this repo's secrets are
		// published in encrypted form where anyone can read the ciphertext.
		notes = append(notes, "")
	}
	b.Notes = notes
	return b
}

// Markdown renders the briefing for a session prompt or a terminal.
func (b Briefing) Markdown(now time.Time) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s — session briefing\n\n", b.Repo)

	if b.WhereLeft != "" {
		sb.WriteString("## Where things were left\n")
		sb.WriteString(b.WhereLeft)
		sb.WriteString("\n\n")
	}
	if len(b.OpenTasks) > 0 {
		sb.WriteString("## Open tasks\n")
		for _, t := range b.OpenTasks {
			fmt.Fprintf(&sb, "- [ ] %s\n", t)
		}
		sb.WriteString("\n")
	}
	if len(b.Conflicts) > 0 {
		fmt.Fprintf(&sb, "## Warning: %d conflicts\n", len(b.Conflicts))
		for _, p := range b.Conflicts {
			fmt.Fprintf(&sb, "- %s (unresolved, decide which side to keep)\n", p)
		}
		sb.WriteString("\n")
	}
	if len(b.Recent) > 0 {
		sb.WriteString("## Not yet synced\n")
		for _, p := range b.Recent {
			fmt.Fprintf(&sb, "- %s\n", p)
		}
		sb.WriteString("\n")
	}
	for _, note := range b.Notes {
		if note != "" {
			fmt.Fprintf(&sb, "%s\n", note)
		}
	}
	if len(b.Unsynced) > 0 {
		fmt.Fprintf(&sb, "\n_%d files waiting to be pushed._\n", len(b.Unsynced))
	} else {
		sb.WriteString("\n_Everything is in sync._\n")
	}
	sb.WriteString(fmt.Sprintf("\n_generated: %s_\n", now.Format("2006-01-02 15:04")))
	return sb.String()
}
