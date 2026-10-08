package main

import (
	"fmt"
	"strings"

	"filippo.io/age"

	"github.com/atalayhuryasar/envmove/internal/crypt"
	"github.com/atalayhuryasar/envmove/internal/gitx"
	"github.com/atalayhuryasar/envmove/internal/keystore"
)

// holderRecipient returns the public key belonging to the identity stored for this
// repository, which is what identifies this machine inside the recipient list.
func holderRecipient(repo *gitx.Repo) string {
	stored, ok, err := loadKey(repo.Root)
	if err != nil || !ok {
		return ""
	}
	ids, err := age.ParseIdentities(strings.NewReader(string(stored)))
	if err != nil || len(ids) == 0 {
		return ""
	}
	if id, ok := ids[0].(*age.X25519Identity); ok {
		return id.Recipient().String()
	}
	return ""
}

// rewrapRecovery re-encrypts the recovery file around a new identity.
//
// The passphrase is required rather than optional. Leaving the old recovery file in
// place would look like working disaster recovery while actually restoring a key that
// can no longer read anything, which is worse than admitting there is none.
func rewrapRecovery(repo *gitx.Repo, identity string) error {
	if !keystore.RecoveryExists(repo.Root) {
		return nil
	}
	fmt.Print("  recovery passphrase (needed to protect the new key): ")
	passphrase := strings.TrimSpace(readLine())
	if passphrase == "" {
		if err := keystore.RemoveRecovery(repo.Root); err != nil {
			return err
		}
		fmt.Println("  ⚠ no passphrase entered, the old recovery copy was deleted.")
		fmt.Println("    The new key will have no way back. Continue anyway?")
		if !askYesNo("") {
			return fmt.Errorf("iptal edildi")
		}
		return nil
	}
	wrapped, err := wrapRecoveryFor(identity, passphrase)
	if err != nil {
		return err
	}
	if err := keystore.WriteRecovery(repo.Root, wrapped); err != nil {
		return err
	}
	fmt.Println("  ✓ recovery copy rewritten with the new key")
	return nil
}

func wrapRecoveryFor(identity, passphrase string) ([]byte, error) {
	return crypt.WrapWithPassphrase(keystore.TrimPassphrase(passphrase), []byte(identity))
}
