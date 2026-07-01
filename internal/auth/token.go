// Package auth resolves and persists the GitLab access token.
//
// Resolution order for reads is: the GLUTE_TOKEN environment variable (highest
// precedence, ideal for headless/CI hosts), then a 0600 file in the config
// directory. An OS-keychain backend can slot in here later without changing
// callers.
package auth

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/MarcelInTO/glute/internal/config"
)

const envToken = "GLUTE_TOKEN"

// TokenPath returns the path to the on-disk token file (best-effort, for
// display).
func TokenPath() string {
	dir, err := config.Dir()
	if err != nil {
		return "token"
	}
	return filepath.Join(dir, "token")
}

// LoadToken resolves the active token. It returns an empty string (and no
// error) when none is configured.
func LoadToken() (string, error) {
	if v := strings.TrimSpace(os.Getenv(envToken)); v != "" {
		return v, nil
	}
	data, err := os.ReadFile(TokenPath())
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// StoreToken writes the token to a 0600 file in the config directory.
func StoreToken(token string) error {
	dir, err := config.Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating config dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "token"), []byte(token+"\n"), 0o600); err != nil {
		return fmt.Errorf("writing token: %w", err)
	}
	return nil
}

// DeleteToken removes the on-disk token file, if present.
func DeleteToken() error {
	err := os.Remove(TokenPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Source describes where the active token came from, for status output.
func Source() string {
	if strings.TrimSpace(os.Getenv(envToken)) != "" {
		return envToken + " env var"
	}
	if _, err := os.Stat(TokenPath()); err == nil {
		return TokenPath()
	}
	return ""
}
