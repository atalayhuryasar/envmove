package crypt

import (
	"bytes"
	"strings"
	"testing"
)

func TestGeneratePassphraseFormat(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		p, err := GeneratePassphrase()
		if err != nil {
			t.Fatal(err)
		}
		// Five groups of four keeps it copyable without losing the grouping that
		// makes a long random string readable.
		if groups := strings.Split(p, "-"); len(groups) != 5 {
			t.Fatalf("%q is not five groups: %d", p, len(groups))
		}
		if seen[p] {
			t.Fatalf("a passphrase was generated twice: %q", p)
		}
		seen[p] = true
		for _, r := range strings.ReplaceAll(p, "-", "") {
			if !strings.ContainsRune(passphraseAlphabet, r) {
				t.Errorf("%q contains a character outside the alphabet: %q", p, r)
			}
		}
	}
}

func TestWrapUnwrapRoundTrip(t *testing.T) {
	passphrase, err := GeneratePassphrase()
	if err != nil {
		t.Fatal(err)
	}
	secret := []byte("AGE-SECRET-KEY-1q2w3e4r5t6y\n")

	wrapped, err := WrapWithPassphrase(passphrase, secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(wrapped, secret) {
		t.Fatal("the wrapped data contains plaintext")
	}
	if !bytes.Contains(wrapped, []byte("scrypt")) {
		t.Error("the scrypt recipient was not used")
	}

	got, err := UnwrapWithPassphrase(passphrase, wrapped)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, secret) {
		t.Errorf("unwrapped = %q, expected %q", got, secret)
	}
}

func TestUnwrapRejectsWrongPassphrase(t *testing.T) {
	wrapped, err := WrapWithPassphrase("dogru-sifre-buraya", []byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := UnwrapWithPassphrase("yanlis-sifre-burda", wrapped); err == nil {
		t.Fatal("a wrong passphrase was accepted")
	}
}

func TestWrapRejectsEmptyPassphrase(t *testing.T) {
	if _, err := WrapWithPassphrase("", []byte("secret")); err == nil {
		t.Fatal("an empty passphrase was accepted")
	}
}
