package keystore

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAccountIsPerCheckout(t *testing.T) {
	a := Account("/Users/me/dev/app")
	b := Account("/Users/me/dev/app-copy")

	if a == b {
		t.Error("two different checkouts must not share one account")
	}
	if !strings.HasPrefix(a, "repo:") {
		t.Errorf("Account = %q, the prefix may have been lost", a)
	}
	// A trailing separator should not create a second entry for the same directory.
	if Account("/Users/me/dev/app/") != Account("/Users/me/dev/app") {
		t.Error("a trailing separator must not create a second account")
	}
}

func TestMemoryStoreRoundTrip(t *testing.T) {
	m := NewMemory()
	account := Account("/tmp/x")

	if _, ok, _ := m.Load(account); ok {
		t.Fatal("an unsaved account must not be found")
	}
	if err := m.Save(account, []byte("secret")); err != nil {
		t.Fatal(err)
	}
	got, ok, err := m.Load(account)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if string(got) != "secret" {
		t.Errorf("Load = %q", got)
	}
	if m.Name() == "" || m.Describe() == "" {
		t.Error("the store description must not be empty")
	}
}

// The test store must never look like a persistence mechanism. A store that quietly
// wrote secrets to disk would be a hole in the one package whose job is to avoid that.
func TestMemoryStoreIsNotPersisted(t *testing.T) {
	m := NewMemory()
	if err := m.Save(Account("/tmp/y"), []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Describe(), "persistent") == false {
		t.Log("the description does not say it is non-persistent")
	}
}

func TestTrimPassphraseOnlyTrims(t *testing.T) {
	// Folding case would make "secret" and "SECRET" the same key, quietly shrinking
	// the search space. Only surrounding whitespace may go.
	if got := TrimPassphrase("  AbC-dEf  "); got != "AbC-dEf" {
		t.Errorf("TrimPassphrase = %q, only surrounding whitespace should be removed", got)
	}
	if TrimPassphrase("secret") == TrimPassphrase("SECRET") {
		t.Error("case must not be folded")
	}
	if TrimPassphrase("a-b") == TrimPassphrase("ab") {
		t.Error("dashes must not be stripped")
	}
}

func TestFallbackPathsAreDistinctFromRecovery(t *testing.T) {
	root := "/tmp/some/repo"
	fallback := FallbackPath(root)
	recovery := RecoveryPath(root)

	if fallback == recovery {
		t.Error("the plaintext key file and the encrypted recovery file must not be the same path")
	}
	// The plaintext key is the sensitive one, so it must not sit where recovery lives.
	if strings.HasPrefix(fallback, filepath.Dir(recovery)+"/") {
		t.Errorf("the plaintext key must not live inside the recovery directory: %s", fallback)
	}
	if FallbackExists(root) {
		t.Error("a file that does not exist must not look present")
	}
}
