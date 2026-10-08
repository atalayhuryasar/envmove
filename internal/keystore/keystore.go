// Package keystore holds the machine's private key.
//
// Only one small piece of envmove is platform-specific: the place the key is kept
// between runs. Everything else — state, merging, detection, the agent briefing — is
// ordinary code and is exercised by tests that run anywhere.
//
// envmove ships for macOS only. This boundary exists so that staying that way is a
// decision rather than a refactor: a second implementation is one package away, and
// nothing else has to know.
package keystore

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Store keeps a secret for a named account.
type Store interface {
	// Name identifies the backing store in user facing messages.
	Name() string
	// Save writes the secret, replacing any existing value.
	Save(account string, secret []byte) error
	// Load returns the secret and whether one existed. A missing entry is not an
	// error, because "this machine has not been set up yet" is ordinary.
	Load(account string) ([]byte, bool, error)
	// Describe reports where the key is kept, for doctor.
	Describe() string
}

// Account identifies one repository's key on this machine.
//
// The absolute repository path keeps two checkouts of the same repository on a single
// machine independent of each other.
func Account(repoRoot string) string {
	return "repo:" + filepath.Clean(repoRoot)
}

// Open returns the store for this platform.
func Open() (Store, error) {
	if _, err := os.Stat("/System/Library/CoreServices/SystemVersion.plist"); err != nil {
		return nil, fmt.Errorf("envmove runs on macOS only")
	}
	return &macOSKeychain{}, nil
}

// Memory keeps secrets in process, for tests. It is deliberately not persistent: a
// store that quietly wrote plaintext to disk would be a hole in a tool whose whole job
// is not to do that.
type Memory struct {
	values map[string][]byte
}

func NewMemory() *Memory { return &Memory{values: map[string][]byte{}} }

func (m *Memory) Name() string { return "bellek (test)" }

func (m *Memory) Save(account string, secret []byte) error {
	if m.values == nil {
		m.values = map[string][]byte{}
	}
	m.values[account] = append([]byte(nil), secret...)
	return nil
}

func (m *Memory) Load(account string) ([]byte, bool, error) {
	v, ok := m.values[account]
	return v, ok, nil
}

func (m *Memory) Describe() string { return "test store, not persistent" }

// FallbackPath is where the key lands when the keychain is unavailable, which happens
// in headless sessions with no login keychain unlocked. The file is written 0600 and
// doctor says so out loud.
func FallbackPath(repoRoot string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", "envmove", "keys", sanitise(repoRoot)+".key")
}

// LoadFallback reads the plaintext fallback file if one exists.
func LoadFallback(repoRoot string) ([]byte, bool) {
	raw, err := os.ReadFile(FallbackPath(repoRoot))
	if err != nil {
		return nil, false
	}
	return raw, true
}

// SaveFallback writes the plaintext fallback file with owner-only permissions.
func SaveFallback(repoRoot string, secret []byte) error {
	path := FallbackPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, secret, 0o600)
}

// FallbackExists reports whether a plaintext fallback file is present, which doctor
// treats as a warning.
func FallbackExists(repoRoot string) bool {
	_, err := os.Stat(FallbackPath(repoRoot))
	return err == nil
}

// TrimPassphrase removes surrounding whitespace and nothing else.
//
// An earlier version also folded case and stripped dashes, which would have been
// friendlier to type but is a real weakness: folding makes "secret" and "SECRET" the
// same key, quietly shrinking the search space. Being forgiving about a passphrase is
// the wrong place to trade strength for convenience.
func TrimPassphrase(s string) string {
	return strings.TrimSpace(s)
}

func sanitise(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}
