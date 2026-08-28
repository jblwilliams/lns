package caddy

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"lns/internal/config"
	"lns/internal/devruntime"
	"lns/internal/models"
)

func TestRuntimeCaddyfileRoutesStandardAndHMRLeases(t *testing.T) {
	content := GenerateRuntimeCaddyfile([]devruntime.Lease{
		{Hostname: "demo-web.localhost", Port: 4312, Profile: models.ProfileHMR},
		{Hostname: "demo-api.localhost", Port: 4313, Profile: models.ProfileStandard},
	}, 80, false)
	for _, want := range []string{"http://demo-web.localhost:80", "bind 127.0.0.1 ::1", "reverse_proxy 127.0.0.1:4312", "header_up Host {host}", "http://demo-api.localhost:80", "reverse_proxy 127.0.0.1:4313"} {
		if !strings.Contains(content, want) {
			t.Fatalf("missing %q:\n%s", want, content)
		}
	}
}

func TestGlobalCaddyfileImportsOnlyRuntimeRoutes(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	content := GenerateGlobalCaddyfile(80)
	if !strings.Contains(content, config.GetRuntimeCaddyfilePath()) || strings.Contains(content, "*.caddy") {
		t.Fatalf("global config must import only process-owned routes:\n%s", content)
	}
	if !strings.Contains(content, "admin "+config.CaddyAdminAddr) || strings.Contains(content, "admin 0.0.0.0") {
		t.Fatalf("Caddy control endpoint must stay on canonical loopback:\n%s", content)
	}
}

func TestGeneratedCanonicalConfigAdaptsWithCaddy(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	if _, err := WriteRuntimeCaddyfile([]devruntime.Lease{{Hostname: "demo.localhost", Port: 4312}}); err != nil {
		t.Fatal(err)
	}
	path, err := WriteGlobalCaddyfile()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command("caddy", "adapt", "--config", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated Caddy config is invalid: %v\n%s", err, output)
	}
	if _, err := os.Stat(config.GetRuntimeCaddyfilePath()); err != nil {
		t.Fatal(err)
	}
}
