// Package config holds envmove.toml, the committed contract both machines share.
//
// The file is written by envmove and edited only through `envmove add`, so it
// deliberately has no options that would need explaining. Recipients are public keys
// and are safe to publish; the private keys never come near the repository.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

const (
	FileName      = "envmove.toml"
	SchemaVersion = 1
	DefaultBranch = "envmove"
)

// BlobPath is where the encrypted snapshot lives on the envmove branch.
const BlobPath = "state.age"

type Config struct {
	Schema     int      `toml:"schema"`
	Branch     string   `toml:"branch"`
	Paths      []string `toml:"paths"`
	Recipients []string `toml:"recipients"`

	// PublicAcknowledged records that the user accepted that an encrypted snapshot on
	// a public remote is permanent. Without it setup keeps asking.
	PublicAcknowledged bool `toml:"public_acknowledged,omitempty"`
}

func Path(root string) string { return filepath.Join(root, FileName) }

func Exists(root string) bool {
	_, err := os.Stat(Path(root))
	return err == nil
}

func Load(root string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(Path(root), &c); err != nil {
		return nil, fmt.Errorf("read %s: %w", FileName, err)
	}
	if c.Schema != SchemaVersion {
		return nil, fmt.Errorf("%s has schema %d, this build understands %d — run `envmove setup` again", FileName, c.Schema, SchemaVersion)
	}
	if c.Branch == "" {
		c.Branch = DefaultBranch
	}
	return &c, nil
}

func Save(root string, c *Config) error {
	c.Schema = SchemaVersion
	if c.Branch == "" {
		c.Branch = DefaultBranch
	}
	f, err := os.Create(Path(root))
	if err != nil {
		return err
	}
	defer f.Close()
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		return err
	}
	return nil
}

// AddRecipient registers this machine's public key, keeping the list sorted and
// duplicate-free so two machines cannot race into a messy file.
func (c *Config) AddRecipient(key string) bool {
	for _, existing := range c.Recipients {
		if existing == key {
			return false
		}
	}
	c.Recipients = append(c.Recipients, key)
	sortStrings(c.Recipients)
	return true
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
