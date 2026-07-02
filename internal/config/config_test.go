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

	if err := Save(DefaultInstance, defaultConfig()); err != nil {
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

	cfg, err := Load(DefaultInstance)
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
	if err := Save(DefaultInstance, cfg); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(dir, "config.toml"))
	if !strings.Contains(string(data), "[[product]]") {
		t.Errorf("populated config should use [[product]] table syntax:\n%s", string(data))
	}

	got, err := Load(DefaultInstance)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Products) != 1 || got.Products[0].Name != "P" {
		t.Errorf("round-trip products: %+v", got.Products)
	}
}

func TestInstancesAreIsolated(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GLUTE_CONFIG_DIR", dir)

	def := defaultConfig()
	def.GitLabURL = "https://default.example.com"
	work := defaultConfig()
	work.GitLabURL = "https://work.example.com"

	if err := Save(DefaultInstance, def); err != nil {
		t.Fatal(err)
	}
	if err := Save("work", work); err != nil {
		t.Fatal(err)
	}

	// Default lives in the base dir; "work" in a subdirectory.
	if _, err := os.Stat(filepath.Join(dir, "config.toml")); err != nil {
		t.Errorf("default config should be in the base dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "work", "config.toml")); err != nil {
		t.Errorf("work config should be in a subdirectory: %v", err)
	}

	gotDef, _ := Load(DefaultInstance)
	gotWork, _ := Load("work")
	if gotDef.GitLabURL != "https://default.example.com" || gotWork.GitLabURL != "https://work.example.com" {
		t.Errorf("instances not isolated: default=%q work=%q", gotDef.GitLabURL, gotWork.GitLabURL)
	}

	names, err := Instances()
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != DefaultInstance || names[1] != "work" {
		t.Errorf("Instances() = %v, want [default work]", names)
	}
}

func TestValidateInstance(t *testing.T) {
	ok := []string{"default", "work", "gitlab.example.com", "a_b-1"}
	for _, n := range ok {
		if err := ValidateInstance(n); err != nil {
			t.Errorf("ValidateInstance(%q) = %v, want nil", n, err)
		}
	}
	bad := []string{"", "..", ".", "../etc", "a/b", "a\\b", "-lead", ".dot"}
	for _, n := range bad {
		if err := ValidateInstance(n); err == nil {
			t.Errorf("ValidateInstance(%q) = nil, want error", n)
		}
	}
}

func TestResolveInstance(t *testing.T) {
	t.Setenv("GLUTE_INSTANCE", "")

	if got, _ := ResolveInstance("flagval"); got != "flagval" {
		t.Errorf("flag precedence: got %q", got)
	}
	if got, _ := ResolveInstance(""); got != DefaultInstance {
		t.Errorf("empty -> default: got %q", got)
	}

	t.Setenv("GLUTE_INSTANCE", "fromenv")
	if got, _ := ResolveInstance(""); got != "fromenv" {
		t.Errorf("env fallback: got %q", got)
	}
	if got, _ := ResolveInstance("flagwins"); got != "flagwins" {
		t.Errorf("flag over env: got %q", got)
	}
	if _, err := ResolveInstance("bad/name"); err == nil {
		t.Errorf("invalid instance should error")
	}
}
