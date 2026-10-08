package keystore

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// service is the keychain service name envmove stores its items under.
const service = "envmove"

// macOSKeychain stores secrets in the login keychain via the `security` tool that
// ships with the system, which avoids a cgo dependency on the Security framework.
type macOSKeychain struct{}

func (m *macOSKeychain) Name() string { return "macOS Keychain" }

func (m *macOSKeychain) Save(account string, secret []byte) error {
	cmd := exec.Command("security", "add-generic-password",
		"-s", service, "-a", account, "-w", string(secret), "-U")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("keychain: %s", strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (m *macOSKeychain) Load(account string) ([]byte, bool, error) {
	cmd := exec.Command("security", "find-generic-password",
		"-s", service, "-a", account, "-w")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "could not be found") {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("keychain: %s", msg)
	}
	return bytes.TrimSpace(stdout.Bytes()), true, nil
}

func (m *macOSKeychain) Describe() string { return "macOS Keychain (login)" }

// SaveWithFallback writes the secret to the keychain and, if that cannot be reached,
// to an owner-only file.
//
// The fallback is returned rather than swallowed. A plaintext key on disk is a
// downgrade, and the caller has to say so out loud: this tool exists so that secrets
// are not left lying around, so quietly accepting one would contradict its reason to
// exist.
func SaveWithFallback(repoRoot string, store Store, account string, secret []byte) (usedFallback bool, err error) {
	if err := store.Save(account, secret); err == nil {
		return false, nil
	}
	if ferr := SaveFallback(repoRoot, secret); ferr != nil {
		return false, fmt.Errorf("could not write to the keychain (%v) or to the file: %w", err, ferr)
	}
	return true, nil
}

// LoadFrom returns the secret from the keychain, or from the plaintext fallback file
// when the keychain holds nothing for this account.
func LoadFrom(repoRoot string, store Store, account string) ([]byte, bool, error) {
	secret, ok, err := store.Load(account)
	if err != nil {
		return nil, false, err
	}
	if ok {
		return secret, true, nil
	}
	secret, ok = LoadFallback(repoRoot)
	return secret, ok, nil
}
