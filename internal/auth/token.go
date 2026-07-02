// Package auth resolves and persists the GitLab access token, per instance.
//
// Resolution order for reads is: the GLUTE_TOKEN environment variable (highest
// precedence, ideal for headless/CI hosts), then a 0600 file in the instance's
// config directory. An OS-keychain backend can slot in here later without
// changing callers.
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

// TokenPath returns the path to an instance's token file (best-effort, for
// display).
func TokenPath(instance string) string {
	dir, err := config.InstanceDir(instance)
	if err != nil {
		return "token"
	}
	return filepath.Join(dir, "token")
}

// LoadToken resolves the active token for an instance. It returns an empty
// string (and no error) when none is configured.
func LoadToken(instance string) (string, error) {
	if v := strings.TrimSpace(os.Getenv(envToken)); v != "" {
		return v, nil
	}
	data, err := os.ReadFile(TokenPath(instance))
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading token: %w", err)
	}
	return strings.TrimSpace(string(data)), nil
}

// StoreToken writes an instance's token to a 0600 file in its config dir.
func StoreToken(instance, token string) error {
	dir, err := config.InstanceDir(instance)
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

// DeleteToken removes an instance's token file, if present.
func DeleteToken(instance string) error {
	err := os.Remove(TokenPath(instance))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Source describes where an instance's active token came from, for status
// output.
func Source(instance string) string {
	if strings.TrimSpace(os.Getenv(envToken)) != "" {
		return envToken + " env var"
	}
	if _, err := os.Stat(TokenPath(instance)); err == nil {
		return TokenPath(instance)
	}
	return ""
}
