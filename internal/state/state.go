// Package state defines the snapshot: the single encrypted blob that carries every
// synced file between machines.
//
// A snapshot is atomic by design. Either the whole set moves or none of it does, so a
// half-synced workspace is not representable.
package state

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"
)

const Version = 1

// Placeholders stored in place of absolute machine paths. Without them a snapshot
// taken on a laptop would carry /Users/atalayhuryasar/... and land useless on a
// second machine.
const (
	homeVar = "~"
	repoVar = "${REPO}"
)

type State struct {
	Version int              `json:"version"`
	Files   map[string]Entry `json:"files"`

	// Recipients records who this snapshot was encrypted to. When the config gains a
	// new machine, push notices the mismatch and re-encrypts, which is what lets a
	// newly added machine read the history that predates it.
	Recipients []string `json:"recipients,omitempty"`
}

// SameRecipients reports whether the snapshot was encrypted to exactly this set.
func (s *State) SameRecipients(keys []string) bool {
	if len(s.Recipients) != len(keys) {
		return false
	}
	have := append([]string{}, s.Recipients...)
	want := append([]string{}, keys...)
	sort.Strings(have)
	sort.Strings(want)
	for i := range have {
		if have[i] != want[i] {
			return false
		}
	}
	return true
}

type Entry struct {
	Mode    string `json:"mode"`
	SHA256  string `json:"sha256"`
	Content []byte `json:"content"`
}

// Sum is the content digest used for change detection.
func Sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// Marshal serialises and compresses. Compression happens before encryption so that
// the ciphertext stays small; age does not compress for us.
func (s *State) Marshal() ([]byte, error) {
	if s.Version == 0 {
		s.Version = Version
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func Unmarshal(data []byte) (*State, error) {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("snapshot is corrupt: %w", err)
	}
	raw, err := io.ReadAll(zr)
	if err != nil {
		return nil, fmt.Errorf("snapshot is corrupt: %w", err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("snapshot is corrupt: %w", err)
	}
	if s.Version != Version {
		return nil, fmt.Errorf("snapshot version %d is not supported by this build", s.Version)
	}
	if s.Files == nil {
		s.Files = map[string]Entry{}
	}
	return &s, nil
}

// IsText reports whether content can safely have paths substituted inside it.
func IsText(b []byte) bool {
	if !utf8.Valid(b) {
		return false
	}
	return !bytes.ContainsRune(b, 0)
}

// Contract replaces machine-specific absolute paths with portable placeholders.
//
// roots holds every spelling of the repository path that this machine might produce.
// A project under /tmp or any symlinked directory is reachable as both /tmp/x and
// /private/tmp/x on macOS, and a value written by hand or by another tool will use one
// or the other. Matching only the resolved form silently fails to substitute, and the
// snapshot then carries a path that is wrong on every other machine.
// Contract rewrites this machine's paths into portable placeholders.
//
// It operates on whole file contents, not on single paths: a .env line reads
// REPO_ROOT=/Users/me/dev/app, so the path appears partway through a line. Matching is
// boundary aware so that /Users/me/dev/app-backup is never mistaken for a child of
// /Users/me/dev/app.
func Contract(s string, roots []string, home string) string {
	if out := contractInto(s, roots, home); out != s {
		return out
	}
	// Fallback for values spelled through a symlink: ROOT=/tmp/x when the repository
	// root is /private/tmp/x. Each path-like token in each line is retried after
	// resolving the directory that contains it.
	if out := contractTokens(s, roots, home); out != s {
		return out
	}
	return s
}

func contractTokens(s string, roots []string, home string) string {
	lines := strings.Split(s, "\n")
	changed := false
	for i, line := range lines {
		if out := contractLine(line, roots, home); out != line {
			lines[i] = out
			changed = true
		}
	}
	if !changed {
		return s
	}
	return strings.Join(lines, "\n")
}

func contractLine(line string, roots []string, home string) string {
	var b strings.Builder
	for i := 0; i < len(line); {
		if line[i] == '/' && (i == 0 || !isPathChar(line[i-1])) {
			j := i
			for j < len(line) && isPathChar(line[j]) {
				j++
			}
			token := line[i:j]
			if resolved := resolveSymlinkedDir(token); resolved != token {
				if out := contractInto(resolved, roots, home); out != resolved {
					b.WriteString(out)
					i = j
					continue
				}
			}
		}
		b.WriteByte(line[i])
		i++
	}
	return b.String()
}

func isPathChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '/', '.', '-', '_', '@', '+', '~':
		return true
	}
	return false
}

func contractInto(s string, roots []string, home string) string {
	out := s
	for _, root := range roots {
		out = replaceAtBoundary(out, root, repoVar)
	}
	if home != "" {
		out = replaceAtBoundary(out, home, homeVar)
	}
	return out
}

// Expand is the inverse of Contract, applied on the machine that reads the snapshot.
// The first root is the resolved repository path and is the one written back.
func Expand(s string, roots []string, home string) string {
	out := s
	if len(roots) > 0 && roots[0] != "" {
		out = strings.ReplaceAll(out, repoVar, roots[0])
	}
	if home != "" {
		out = expandTilde(out, home)
	}
	return out
}

// replaceAtBoundary replaces every occurrence of from that is followed by a path
// boundary, so that a directory is not matched inside the name of a longer one.
func replaceAtBoundary(s, from, to string) string {
	if from == "" {
		return s
	}
	var b strings.Builder
	for {
		i := strings.Index(s, from)
		if i < 0 {
			break
		}
		rest := s[i+len(from):]
		if isPathBoundary(rest) {
			b.WriteString(s[:i])
			b.WriteString(to)
		} else {
			b.WriteString(s[:i+len(from)])
		}
		s = rest
	}
	b.WriteString(s)
	return b.String()
}

// expandTilde expands ~ only where it introduces a path. Replacing every tilde would
// mangle ordinary text such as "roughly 5~10".
func expandTilde(s, home string) string {
	var b strings.Builder
	for {
		i := strings.IndexByte(s, '~')
		if i < 0 {
			break
		}
		atStart := i == 0 || !isAlnum(s[i-1])
		rest := s[i+1:]
		if atStart && (rest == "" || rest[0] == '/') {
			b.WriteString(s[:i])
			b.WriteString(home)
		} else {
			b.WriteString(s[:i+1])
		}
		s = rest
	}
	b.WriteString(s)
	return b.String()
}

func isPathBoundary(rest string) bool {
	if rest == "" {
		return true
	}
	switch c := rest[0]; c {
	case '/', ' ', '\t', '\n', '\r', '"', '\'', '=', ',', ';', ':', ')':
		return true
	}
	return false
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func resolveSymlinkedDir(s string) string {
	dir := filepath.Dir(s)
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return s
	}
	return filepath.Join(resolved, filepath.Base(s))
}
