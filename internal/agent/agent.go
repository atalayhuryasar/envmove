// Package agent installs envmove into AI coding tools so that they sync without being
// asked.
//
// Two mechanisms are used deliberately. Where the tool supports lifecycle hooks we wire
// real hooks, which are deterministic and cannot be forgotten. Where it does not, we
// write an instruction the agent reads. The instruction layer is weaker, and pretending
// otherwise would be the wrong kind of optimistic.
package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// binaryName is what hooks invoke.
//
// Unlike the git hooks, these run inside a terminal-launched agent, where the user's
// PATH is present. Using the bare name also means a synced config file stays valid on
// the other machine, where an absolute path from this one would be wrong.
const binaryName = "envmove"

type Kind string

const (
	Claude Kind = "claude"
	Cursor Kind = "cursor"
	Codex  Kind = "codex"
)

// Detect reports which tools this repository looks like it uses.
func Detect(root string) []Kind {
	var found []Kind
	if isDir(filepath.Join(root, ".claude")) {
		found = append(found, Claude)
	}
	if isDir(filepath.Join(root, ".cursor")) {
		found = append(found, Cursor)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			found = append(found, Codex)
			break
		}
	}
	return found
}

func isDir(p string) bool {
	info, err := os.Stat(p)
	return err == nil && info.IsDir()
}

// Install wires envmove into one tool and reports where it wrote.
func Install(root string, kind Kind) (string, error) {
	switch kind {
	case Claude:
		return installClaude(root)
	case Cursor:
		return installCursor(root)
	case Codex:
		return installAgentsMD(root)
	}
	return "", fmt.Errorf("unknown agent: %s", kind)
}

func claudeHooks() map[string]any {
	return map[string]any{
		"SessionStart": []any{
			map[string]any{"hooks": []any{hookCommand(binaryName + " hook session-start")}},
		},
		"Stop": []any{
			map[string]any{"hooks": []any{hookCommand(binaryName + " hook session-end")}},
		},
	}
}

func hookCommand(command string) map[string]any {
	return map[string]any{"type": "command", "command": command}
}

// installClaude merges the hooks into .claude/settings.local.json.
//
// The local file is the right target on purpose: it is per machine by design and never
// committed, so a hook pointing at this machine's binary can never be synced somewhere
// it does not resolve.
func installClaude(root string) (string, error) {
	dir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "settings.local.json")

	settings := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		// A file we cannot parse is left untouched rather than clobbered: it may hold
		// settings the user cares about more than ours.
		if err := json.Unmarshal(raw, &settings); err != nil {
			return "", fmt.Errorf("could not read %s, left alone: %w", path, err)
		}
	}

	existing, _ := settings["hooks"].(map[string]any)
	if existing == nil {
		existing = map[string]any{}
	}
	for event, value := range claudeHooks() {
		if _, already := existing[event]; already {
			// Never replace hooks the user already configured for that event.
			continue
		}
		existing[event] = value
	}
	settings["hooks"] = existing

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", err
	}
	return path, os.WriteFile(path, append(out, '\n'), 0o644)
}

const cursorRule = "---\n" +
	"description: envmove — project state sync\n" +
	"alwaysApply: true\n" +
	"---\n" +
	"\n" +
	"This repository uses envmove to carry the project context that git refuses to carry:\n" +
	".env, HANDOVER.md, TODO.md and similar files.\n" +
	"\n" +
	"Run these yourself, without being asked:\n" +
	"\n" +
	"- At the start of a session: `envmove`\n" +
	"  It pulls the latest state from the other machine and prints a briefing of where\n" +
	"  the work was left. Read it before doing anything else.\n" +
	"- At the end of a session: `envmove`\n" +
	"  It pushes whatever changed. If you skip this, the work is lost to the next machine.\n" +
	"\n" +
	"Never invent the state of this project. If you do not know where the work was left,\n" +
	"run `envmove` first.\n"

func installCursor(root string) (string, error) {
	dir := filepath.Join(root, ".cursor", "rules")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "envmove.mdc")
	return path, os.WriteFile(path, []byte(cursorRule), 0o644)
}

const agentsSection = "\n## envmove\n\nThis repository uses envmove to carry the project context git will not carry: `.env`,\n`HANDOVER.md`, `TODO.md` and similar files. They are encrypted and live on\nthe `envmove` branch.\n\nRun `envmove` yourself:\n\n- **Start of session:** `envmove` pulls the latest state from the other machine and\n  prints a briefing. Read it first; it says where the work was left.\n- **End of session:** `envmove` pushes what changed.\n\nIf you do not know the state of this project, run `envmove` rather than guessing.\n"

func installAgentsMD(root string) (string, error) {
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "GEMINI.md"} {
		path := filepath.Join(root, name)
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		if strings.Contains(string(raw), "## envmove") {
			return path, nil // already documented
		}
		merged := strings.TrimRight(string(raw), "\n") + "\n" + agentsSection
		return path, os.WriteFile(path, []byte(merged), 0o644)
	}
	path := filepath.Join(root, "AGENTS.md")
	return path, os.WriteFile(path, []byte(strings.TrimLeft(agentsSection, "\n")), 0o644)
}
