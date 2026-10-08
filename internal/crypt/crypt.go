// Package crypt wraps age encryption.
//
// Encryption happens only in transit. Files on disk stay plaintext so that editors,
// docker and every other tool keep working exactly as before. Plaintext never leaves
// the machine: the private key lives in the OS keychain and never enters git.
package crypt

import (
	"bytes"
	"fmt"
	"io"
	"os"

	"filippo.io/age"
)

// Cipher encrypts snapshots to a fixed set of recipients and decrypts with the
// private keys this machine holds.
type Cipher struct {
	recipients []age.Recipient
	identities []age.Identity
}

// New builds a Cipher. Missing recipients or unparsable keys are reported as errors
// rather than silently ignored: a snapshot nobody can decrypt is worse than a failure.
func New(recipientKeys []string, identities []age.Identity) (*Cipher, error) {
	if len(recipientKeys) == 0 {
		return nil, fmt.Errorf("no recipients configured")
	}
	recipients := make([]age.Recipient, 0, len(recipientKeys))
	for _, key := range recipientKeys {
		r, err := age.ParseX25519Recipient(key)
		if err != nil {
			return nil, fmt.Errorf("invalid recipient key: %w", err)
		}
		recipients = append(recipients, r)
	}
	return &Cipher{recipients: recipients, identities: identities}, nil
}

func (c *Cipher) Encrypt(plaintext []byte) ([]byte, error) {
	if len(c.recipients) == 0 {
		return nil, fmt.Errorf("no recipients configured")
	}
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, c.recipients...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(plaintext); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (c *Cipher) Decrypt(ciphertext []byte) ([]byte, error) {
	if len(c.identities) == 0 {
		return nil, fmt.Errorf("no private key available for this repository")
	}
	r, err := age.Decrypt(bytes.NewReader(ciphertext), c.identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt failed: %w", err)
	}
	return io.ReadAll(r)
}

// GenerateIdentity creates a new X25519 keypair for this machine.
func GenerateIdentity() (*age.X25519Identity, error) {
	id, err := age.GenerateX25519Identity()
	if err != nil {
		return nil, err
	}
	return id, nil
}

// LoadIdentities reads private keys from file. Used for the recovery path, where the
// keychain is unavailable and the user supplies the recovery passphrase instead.
func LoadIdentities(paths []string) ([]age.Identity, error) {
	var ids []age.Identity
	for _, p := range paths {
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		parsed, err := age.ParseIdentities(f)
		f.Close()
		if err != nil {
			return nil, fmt.Errorf("parse identities in %s: %w", p, err)
		}
		ids = append(ids, parsed...)
	}
	return ids, nil
}
