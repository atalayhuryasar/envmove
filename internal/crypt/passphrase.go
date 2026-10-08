package crypt

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"math/big"
	"strings"

	"filippo.io/age"
)

// scryptWorkFactor is 2^18, the cost of one passphrase unlock. It is slow on purpose:
// the recovery file is the only thing standing between a lost keychain and losing every
// secret the repository ever carried.
const scryptWorkFactor = 18

// passphraseAlphabet omits characters that are easy to confuse when transcribed:
// no 0/O, 1/l/I, or similar shapes.
const passphraseAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

const passphraseLength = 20

// GeneratePassphrase returns a random passphrase in groups of four, which is long
// enough to be safe and short enough to write down or paste into a password manager.
func GeneratePassphrase() (string, error) {
	raw := make([]byte, passphraseLength)
	max := big.NewInt(int64(len(passphraseAlphabet)))
	for i := range raw {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		raw[i] = passphraseAlphabet[n.Int64()]
	}
	groups := make([]string, 0, 5)
	for i := 0; i < len(raw); i += 4 {
		end := i + 4
		if end > len(raw) {
			end = len(raw)
		}
		groups = append(groups, string(raw[i:end]))
	}
	return strings.Join(groups, "-"), nil
}

// WrapWithPassphrase encrypts data so that only the passphrase can open it.
func WrapWithPassphrase(passphrase string, data []byte) ([]byte, error) {
	if len(passphrase) == 0 {
		return nil, fmt.Errorf("passphrase must not be empty")
	}
	recipient, err := age.NewScryptRecipient(passphrase)
	if err != nil {
		return nil, err
	}
	recipient.SetWorkFactor(scryptWorkFactor)
	return encryptTo(data, recipient)
}

// UnwrapWithPassphrase reverses WrapWithPassphrase.
func UnwrapWithPassphrase(passphrase string, data []byte) ([]byte, error) {
	identity, err := age.NewScryptIdentity(passphrase)
	if err != nil {
		return nil, err
	}
	reader, err := age.Decrypt(bytes.NewReader(data), identity)
	if err != nil {
		return nil, fmt.Errorf("wrong passphrase, or the recovery file is damaged")
	}
	return io.ReadAll(reader)
}

func encryptTo(data []byte, recipients ...age.Recipient) ([]byte, error) {
	var buf bytes.Buffer
	w, err := age.Encrypt(&buf, recipients...)
	if err != nil {
		return nil, err
	}
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
