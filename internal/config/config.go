// Package config loads and persists glute's non-secret configuration.
//
// The authentication token is deliberately NOT part of this file; it lives in a
// separate 0600 file (see package auth) so secrets never mix with shareable
// config.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Config is glute's on-disk configuration.
type Config struct {
	GitLabURL       string    `toml:"gitlab_url"`
	CACert          string    `toml:"ca_cert,omitempty"`
	RefreshInterval Duration  `toml:"refresh_interval"`
	RecentWindow    Duration  `toml:"recent_window"` // "recent failures & successes" lookback
	TopWindow       Duration  `toml:"top_window"`    // "top … last month" lookback
	Products        []Product `toml:"product,omitempty"`
}

// Product is a user-defined grouping of GitLab groups and/or projects (repos)
// that glute aggregates together. The name "product" avoids collision with
// GitLab's own "project" (a single repo) and "group" nouns.
type Product struct {
	Name     string   `toml:"name"`
	Groups   []string `toml:"groups,omitempty"`
	Projects []string `toml:"projects,omitempty"`
}

// Duration is a time.Duration that marshals to/from a TOML string like "30s".
type Duration time.Duration

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func (d *Duration) UnmarshalText(text []byte) error {
	parsed, err := time.ParseDuration(string(text))
	if err != nil {
		return err
	}
	*d = Duration(parsed)
	return nil
}

func defaultConfig() Config {
	return Config{
		RefreshInterval: Duration(30 * time.Second),
		RecentWindow:    Duration(24 * time.Hour),
		TopWindow:       Duration(30 * 24 * time.Hour),
	}
}

// Dir returns glute's config directory. It honors GLUTE_CONFIG_DIR, then
// XDG_CONFIG_HOME, and otherwise falls back to ~/.config/glute — kept
// consistent across Linux, macOS, and Windows by request.
func Dir() (string, error) {
	if v := os.Getenv("GLUTE_CONFIG_DIR"); v != "" {
		return v, nil
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "glute"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("locating home directory: %w", err)
	}
	return filepath.Join(home, ".config", "glute"), nil
}

// Path returns the path to the config file (best-effort, for display).
func Path() string {
	dir, err := Dir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(dir, "config.toml")
}

// Load reads the config file, returning sensible defaults when it doesn't yet
// exist (first run).
func Load() (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(Path())
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", Path(), err)
	}
	return cfg, nil
}

// Save writes the config file, creating the config directory (0700) as needed.
func Save(cfg Config) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	data, err := toml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encoding config: %w", err)
	}

	var buf bytes.Buffer
	buf.WriteString(configHeader)
	buf.Write(data)
	if len(cfg.Products) == 0 {
		// Nothing to watch yet: leave a commented template so the file is
		// self-documenting rather than an opaque `product = []`.
		buf.WriteString(productExample)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

const configHeader = `# glute configuration.
# The auth token is NOT stored here — see ~/.config/glute/token or $GLUTE_TOKEN.

`

const productExample = `
# A product is a named set of GitLab groups and/or projects (repositories);
# glute aggregates pipeline and job stats across all of them. Add one or more:
#
# [[product]]
# name     = "Payments"
# groups   = ["org/payments"]
# projects = ["org/legacy-gateway"]
`
