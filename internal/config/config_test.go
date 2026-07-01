package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveOmitsEmptyProductsAndAddsExample(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLUTE_CONFIG_DIR", dir)

	if err := Save(defaultConfig()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	t.Logf("generated config:\n%s", got)

	if strings.Contains(got, "product = []") {
		t.Errorf("generated config should not contain the empty inline array")
	}
	if !strings.Contains(got, "# [[product]]") {
		t.Errorf("generated config should include a commented [[product]] example")
	}

	// It must still round-trip cleanly.
	cfg, err := Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg.GitLabURL != "" || len(cfg.Products) != 0 {
		t.Errorf("unexpected reload: %+v", cfg)
	}
}

func TestSaveEmitsProductTables(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLUTE_CONFIG_DIR", dir)

	cfg := defaultConfig()
	cfg.GitLabURL = "https://gitlab.example.com"
	cfg.Products = []Product{{Name: "P", Groups: []string{"g"}}}
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(data), "[[product]]") {
		t.Errorf("populated config should use [[product]] table syntax:\n%s", string(data))
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Products) != 1 || got.Products[0].Name != "P" {
		t.Errorf("round-trip products: %+v", got.Products)
	}
}
