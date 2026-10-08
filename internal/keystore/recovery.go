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

// RecoveryPath is where the passphrase-wrapped key lives for a repository.
func RecoveryPath(repoRoot string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "."
	}
	return filepath.Join(home, ".config", recoveryDir, sanitise(repoRoot)+".agekey")
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
