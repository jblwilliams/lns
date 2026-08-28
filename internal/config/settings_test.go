package config

import "testing"

func TestDefaultSettingsUseCanonicalHTTPOrigin(t *testing.T) {
	settings := DefaultSettings()
	if settings.HTTPS || settings.HTTPPort != 80 {
		t.Fatalf("expected canonical http://*.localhost origin, got %#v", settings)
	}
}
