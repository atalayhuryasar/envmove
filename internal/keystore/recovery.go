package keystore

import (
	"os"
	"path/filepath"
)

// The recovery file is a copy of the machine's private age key, encrypted with a
// passphrase instead of the login keychain.
//
// It exists for one reason: the login keychain is a single point of failure. A
// reinstalled macOS, a wiped laptop or a forgotten login password would otherwise mean
// every secret the repository ever carried is gone for good, with no way back.
//
// It is plain filesystem, not keychain, so it works the same wherever envmove runs.

const recoveryDir = "envmove/recovery"

// EnvRecoveryDir overrides where recovery copies are kept.
//
// It exists for the test suite, and the reason it exists is a mistake that was actually
// made: the end-to-end scripts cleared ~/.config/envmove/recovery with a wildcard, which
// deleted the recovery copy belonging to every real repository on the machine. That file
// is the only way back after a keychain loss, so a test suite quietly removing it is the
// same class of problem as a test writing `git config --global` — reaching outside the
// sandbox and destroying something the machine owner depends on.
//
// Pointing this at a temporary directory means the suite cannot do it again.
func RecoveryRoot() string {
	if dir := os.Getenv("ENVMOVE_RECOVERY_DIR"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", recoveryDir)
}

// RecoveryPath is where the passphrase-wrapped key lives for a repository.
func RecoveryPath(repoRoot string) string {
	return filepath.Join(RecoveryRoot(), sanitise(repoRoot)+".agekey")
}

// WriteRecovery stores the wrapped key with owner-only permissions.
func WriteRecovery(repoRoot string, wrapped []byte) error {
	path := RecoveryPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, wrapped, 0o600)
}

// ReadRecovery returns the wrapped key, reporting whether one exists.
func ReadRecovery(repoRoot string) ([]byte, bool) {
	raw, err := os.ReadFile(RecoveryPath(repoRoot))
	if err != nil {
		return nil, false
	}
	return raw, true
}

// RecoveryExists reports whether this repository has a recovery file.
func RecoveryExists(repoRoot string) bool {
	_, ok := ReadRecovery(repoRoot)
	return ok
}

// RemoveRecovery deletes the recovery file.
func RemoveRecovery(repoRoot string) error {
	err := os.Remove(RecoveryPath(repoRoot))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
