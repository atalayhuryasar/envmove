// Package envfile renders a .env.example from a real .env.
//
// For an open source project the most useful thing envmove can do is not carry
// secrets, it is keep .env.example honest. A newcomer copies it, fills in two
// variables and runs. When it drifts, they spend an hour on a connection error instead
// of building something.
package envfile

import (
	"sort"
	"strings"
)

type Kind int

const (
	KindBlank Kind = iota
	KindComment
	KindPair
)

type Entry struct {
	Kind  Kind
	Text  string // the original line, for comments and blanks
	Key   string
	Value string
	HasEq bool
}

// Parse reads a dotenv file into ordered entries.
//
// It is deliberately not a full dotenv implementation: quotes, interpolation and
// multi-line values are all irrelevant to producing an example file, and pretending
// to understand them would risk mangling a line that should have been passed through.
func Parse(content string) []Entry {
	lines := strings.Split(content, "\n")
	out := make([]Entry, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == "":
			out = append(out, Entry{Kind: KindBlank, Text: line})
		case strings.HasPrefix(trimmed, "#"):
			out = append(out, Entry{Kind: KindComment, Text: line})
		default:
			key, value, hasEq := splitPair(trimmed)
			if key == "" {
				out = append(out, Entry{Kind: KindBlank, Text: line})
				continue
			}
			out = append(out, Entry{Kind: KindPair, Key: key, Value: value, HasEq: hasEq})
		}
	}
	return out
}

func splitPair(line string) (key, value string, hasEq bool) {
	idx := strings.Index(line, "=")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	// Strip a trailing comment that is clearly outside the value. A '#' inside quotes
	// or one that has no space before it is left alone, because guessing here would
	// corrupt keys like PASSWORD=abc#123.
	if len(value) > 1 && !strings.HasPrefix(value, `"`) && !strings.HasPrefix(value, `'`) {
		if h := strings.Index(value, " #"); h >= 0 {
			value = strings.TrimSpace(value[:h])
		}
	}
	return key, value, true
}

// Keys returns the variable names defined in a dotenv file, in order of appearance.
func Keys(content string) []string {
	var keys []string
	seen := map[string]bool{}
	for _, e := range Parse(content) {
		if e.Kind == KindPair && !seen[e.Key] {
			seen[e.Key] = true
			keys = append(keys, e.Key)
		}
	}
	return keys
}

// Result reports what changed, so the caller can tell the user rather than silently
// rewriting a committed file.
type Result struct {
	Content string
	Added   []string
	Removed []string
}

// Build renders the example file.
//
// Three rules, in this order of importance:
//
//   - Values are never copied from the real file. They are either kept from the
//     existing example or left empty.
//   - Comments are never copied from the real file either. A comment reading
//     "# the prod master password" in .env would become published documentation.
//   - Comments and placeholder values already in the example survive, because those
//     were written for the public by a human who knew what they were doing.
func Build(real, previous string) Result {
	realKeys := Keys(real)
	realSet := map[string]bool{}
	for _, k := range realKeys {
		realSet[k] = true
	}

	prevEntries := Parse(previous)
	kept := map[string]bool{}
	var lines []string

	for _, e := range prevEntries {
		switch e.Kind {
		case KindBlank:
			// Dropped for now; spacing is rebuilt below.
		case KindComment:
			lines = append(lines, e.Text)
		case KindPair:
			if !realSet[e.Key] {
				// The variable is gone from .env, so its comment block goes with it
				// rather than left dangling above an unrelated key.
				trimDanglingComment(&lines)
				continue
			}
			kept[e.Key] = true
			lines = append(lines, e.Key+"="+e.Value)
		}
	}

	var added []string
	for _, k := range realKeys {
		if kept[k] {
			continue
		}
		added = append(added, k)
		if len(lines) > 0 && strings.TrimSpace(lastLine(lines)) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, k+"=")
	}

	trimDanglingComment(&lines)
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}

	var removed []string
	for _, e := range prevEntries {
		if e.Kind == KindPair && !realSet[e.Key] {
			removed = append(removed, e.Key)
		}
	}

	content := strings.Join(lines, "\n")
	if content != "" {
		content += "\n"
	}
	return Result{Content: content, Added: added, Removed: removed}
}

// trimDanglingComment removes a trailing comment block with no key under it, which is
// what is left behind when the variable a comment described has been deleted.
func trimDanglingComment(lines *[]string) {
	pending := 0
	for _, l := range *lines {
		if strings.TrimSpace(l) == "" || strings.HasPrefix(strings.TrimSpace(l), "#") {
			pending++
			continue
		}
		pending = 0
	}
	if pending > 0 {
		*lines = (*lines)[:len(*lines)-pending]
	}
}

func lastLine(lines []string) string { return lines[len(lines)-1] }

// Missing lists keys the example does not document.
func Missing(example, real string) []string {
	have := map[string]bool{}
	for _, k := range Keys(example) {
		have[k] = true
	}
	var out []string
	for _, k := range Keys(real) {
		if !have[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}
