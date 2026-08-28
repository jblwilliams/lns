package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultSettingsUseDedicatedAdminAddress(t *testing.T) {
	settings := DefaultSettings()
	if settings.AdminAddr != "127.0.0.1:20190" {
		t.Fatalf("unexpected defaults: %#v", settings)
	}
}

func TestLoadSettingsIgnoresLegacyRouteOverrides(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, ".lns", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"http_port":8443,"https":true,"admin_addr":"127.0.0.1:2999"}`), 0644); err != nil {
		t.Fatal(err)
	}
	settings, err := LoadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings.AdminAddr != "127.0.0.1:2999" {
		t.Fatalf("legacy route changed canonical local origin: %#v", settings)
	}
}
