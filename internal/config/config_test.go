package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadDefaultsWhenMissing(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "does-not-exist.toml"))
	if err != nil {
		t.Fatalf("load missing config: %v", err)
	}
	if cfg.Server.Port != 8080 {
		t.Errorf("expected default port 8080, got %d", cfg.Server.Port)
	}
	if cfg.Ports.Base != 10000 {
		t.Errorf("expected default port base 10000, got %d", cfg.Ports.Base)
	}
	if !filepath.IsAbs(cfg.Paths.VMRoot) {
		t.Errorf("expected vm_root to be absolute, got %q", cfg.Paths.VMRoot)
	}
}

func TestLoadOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[server]
port = 9090

[ports]
base = 20000
block_size = 20
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != 9090 {
		t.Errorf("expected port 9090, got %d", cfg.Server.Port)
	}
	if cfg.Ports.Base != 20000 || cfg.Ports.BlockSize != 20 {
		t.Errorf("ports not applied: %+v", cfg.Ports)
	}
	// Unspecified fields keep defaults.
	if cfg.Limits.MaxConcurrentVMs != 4 {
		t.Errorf("expected default max_concurrent_vms 4, got %d", cfg.Limits.MaxConcurrentVMs)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("VPHONE_WEB_PORT", "7777")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Server.Port != 7777 {
		t.Errorf("expected env override port 7777, got %d", cfg.Server.Port)
	}
}

func TestValidateRejectsBadPort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("[server]\nport = 70000\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected validation error for out-of-range port")
	}
}
