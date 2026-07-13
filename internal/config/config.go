// Package config loads and persists glute's non-secret configuration.
//
// glute supports multiple named instances (e.g. two GitLab servers), each with
// its own config, token, and log. The "default" instance uses the base config
// dir directly; named instances get a subdirectory under it. The auth token is
// deliberately NOT part of the config file; it lives in a separate 0600 file
// (see package auth) so secrets never mix with shareable config.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// DefaultInstance is the instance used when none is specified. Its data lives
// directly in the base config dir (not a subdirectory), keeping the common
// single-instance case simple and backward compatible.
const DefaultInstance = "default"

const envInstance = "GLUTE_INSTANCE"

// Config is glute's on-disk configuration for a single instance.
type Config struct {
	GitLabURL       string    `toml:"gitlab_url"`
	CACert          string    `toml:"ca_cert,omitempty"`
	RefreshInterval Duration  `toml:"refresh_interval"`
	RecentWindow    Duration  `toml:"recent_window"` // "recent failures & successes" lookback
	TopWindow       Duration  `toml:"top_window"`    // "top … last month" lookback
	Products        []Product `toml:"product,omitempty"`
	// RunnerAliases maps a runner's full name (its GitLab description) to a
	// shorter label for display; runners not listed show their real name. It's a
	// display-only remap (the data layer keeps the true name), handy because
	// runner descriptions are long and change rarely.
	RunnerAliases map[string]string `toml:"runner_aliases,omitempty"`
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

var instanceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)

// ValidateInstance checks that name is safe to use as a directory component.
func ValidateInstance(name string) error {
	if name == DefaultInstance {
		return nil
	}
	if !instanceNameRe.MatchString(name) {
		return fmt.Errorf("invalid instance name %q: use letters, digits, '.', '_' or '-' (starting alphanumeric)", name)
	}
	return nil
}

// ResolveInstance picks the active instance: the flag value, else
// $GLUTE_INSTANCE, else "default". The result is validated.
func ResolveInstance(flag string) (string, error) {
	name := flag
	if name == "" {
		name = os.Getenv(envInstance)
	}
	if name == "" {
		name = DefaultInstance
	}
	if err := ValidateInstance(name); err != nil {
		return "", err
	}
	return name, nil
}

// BaseDir returns glute's root config directory: GLUTE_CONFIG_DIR, else
// XDG_CONFIG_HOME/glute, else ~/.config/glute — consistent across platforms.
func BaseDir() (string, error) {
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

// InstanceDir returns the directory holding an instance's config, token, and
// log. The default instance uses BaseDir directly; named instances get a
// subdirectory.
func InstanceDir(instance string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	if instance == "" || instance == DefaultInstance {
		return base, nil
	}
	if err := ValidateInstance(instance); err != nil {
		return "", err
	}
	return filepath.Join(base, instance), nil
}

// Path returns the config file path for an instance (best-effort, for display).
func Path(instance string) string {
	dir, err := InstanceDir(instance)
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(dir, "config.toml")
}

// Load reads an instance's config, returning sensible defaults when it doesn't
// yet exist.
func Load(instance string) (Config, error) {
	cfg := defaultConfig()
	data, err := os.ReadFile(Path(instance))
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("reading config: %w", err)
	}
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parsing %s: %w", Path(instance), err)
	}
	return cfg, nil
}

// Save writes an instance's config, creating its directory (0700) as needed.
func Save(instance string, cfg Config) error {
	dir, err := InstanceDir(instance)
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
		buf.WriteString(productExample)
	}
	if len(cfg.RunnerAliases) == 0 {
		buf.WriteString(runnerAliasExample)
	}

	if err := os.WriteFile(filepath.Join(dir, "config.toml"), buf.Bytes(), 0o644); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	return nil
}

// Instances returns the names of instances that have a config file, including
// "default" when the base dir has one. Sorted.
func Instances() ([]string, error) {
	base, err := BaseDir()
	if err != nil {
		return nil, err
	}

	var names []string
	if _, err := os.Stat(filepath.Join(base, "config.toml")); err == nil {
		names = append(names, DefaultInstance)
	}
	entries, err := os.ReadDir(base)
	if errors.Is(err, fs.ErrNotExist) {
		return names, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if _, err := os.Stat(filepath.Join(base, e.Name(), "config.toml")); err == nil {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names, nil
}

const configHeader = `# glute configuration.
# The auth token is stored separately (run ` + "`glute auth`" + `), never in this file.

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

const runnerAliasExample = `
# Shorten long runner names for display. Map each runner's full name (as GitLab
# reports it) to a short label; runners not listed keep their real name.
#
# [runner_aliases]
# "shared-gitlab-runner-linux-x86-64-prod-01" = "linux-01"
# "macos-m2-signing-runner"                   = "mac-sign"
`
