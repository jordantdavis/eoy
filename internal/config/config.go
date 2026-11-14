// Package config reads and writes the eoy configuration file.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config is the content of config.toml. Every field is optional.
type Config struct {
	// GitHubUser overrides the login of the active gh account.
	GitHubUser string `toml:"github_user,omitempty"`
	// Mannequin is the placeholder login that an org migration made for you.
	// PRs from before the migration show it as their author.
	Mannequin string `toml:"mannequin,omitempty"`
	// ExtraAuthors are more logins to include in every PR sync, for example
	// an older account.
	ExtraAuthors []string `toml:"extra_authors,omitempty"`
	// Org is the default org for `eoy mannequin find`.
	Org string `toml:"org,omitempty"`
}

// DefaultPath returns $EOY_CONFIG, else $XDG_CONFIG_HOME/eoy/config.toml,
// else ~/.config/eoy/config.toml.
func DefaultPath() string {
	if p := os.Getenv("EOY_CONFIG"); p != "" {
		return p
	}
	return filepath.Join(baseDir("XDG_CONFIG_HOME", ".config"), "eoy", "config.toml")
}

// DefaultDBPath returns $EOY_DB, else $XDG_DATA_HOME/eoy/eoy.db, else
// ~/.local/share/eoy/eoy.db.
func DefaultDBPath() string {
	if p := os.Getenv("EOY_DB"); p != "" {
		return p
	}
	return filepath.Join(baseDir("XDG_DATA_HOME", filepath.Join(".local", "share")), "eoy", "eoy.db")
}

func baseDir(env, homeRel string) string {
	if d := os.Getenv(env); d != "" {
		return d
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, homeRel)
}

// Load reads the file at path. A missing file gives an empty Config.
func Load(path string) (Config, error) {
	var c Config
	_, err := toml.DecodeFile(path, &c)
	if errors.Is(err, fs.ErrNotExist) {
		return Config{}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("read %s: %w", path, err)
	}
	return c, nil
}

// Save writes c to path and creates the parent directory.
func Save(path string, c Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := toml.NewEncoder(f).Encode(c); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
