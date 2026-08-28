package caddy

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"lns/internal/config"
	"lns/internal/devruntime"
	"lns/internal/models"
)

func TestGeneratedHTTPSServiceAdaptsWithCaddy(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy is not installed")
	}
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "web", Port: 5173, Profile: models.ProfileHMR, Status: models.StatusResolved}}}
	path := filepath.Join(t.TempDir(), "Caddyfile")
	if err := os.WriteFile(path, []byte(GenerateProjectCaddyfile(project, 8443, UpstreamModeHost)), 0644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("caddy", "adapt", "--config", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated HTTPS Caddyfile is invalid: %v\n%s", err, output)
	}
}

func TestGeneratedGlobalHTTPSConfigAdaptsWithCaddy(t *testing.T) {
	if _, err := exec.LookPath("caddy"); err != nil {
		t.Skip("caddy is not installed")
	}
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(config.GetCaddyConfigDir(), 0755); err != nil {
		t.Fatal(err)
	}
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "web", Port: 5173, Profile: models.ProfileHMR, Status: models.StatusResolved}}}
	if err := os.WriteFile(filepath.Join(config.GetCaddyConfigDir(), "demo.caddy"), []byte(GenerateProjectCaddyfile(project, 8443, UpstreamModeHost)), 0644); err != nil {
		t.Fatal(err)
	}
	path := config.GetGlobalCaddyfilePath()
	if err := os.WriteFile(path, []byte(GenerateGlobalCaddyfile(8443, "127.0.0.1:20190")), 0644); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("caddy", "adapt", "--config", path)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generated global HTTPS Caddyfile is invalid: %v\n%s", err, output)
	}
}

func TestGenerateRuntimeCaddyfileRoutesProcessLease(t *testing.T) {
	content := GenerateRuntimeCaddyfile([]devruntime.Lease{{
		Project:  "demo",
		Service:  "web",
		Hostname: "feature.demo.localhost",
		Port:     4312,
		PID:      os.Getpid(),
	}}, 8443, true)

	if !strings.Contains(content, "https://feature.demo.localhost:8443") || !strings.Contains(content, "reverse_proxy localhost:4312") {
		t.Fatalf("expected runtime hostname to route to its leased port:\n%s", content)
	}
}

func TestDockerExportUsesStableContainerPortInsteadOfDynamicDevelopmentPort(t *testing.T) {
	project := &models.Project{Name: "demo", Services: []models.Service{{
		Name:          "web",
		ContainerPort: 5173,
		Docker:        true,
		ContainerName: "web",
		Profile:       models.ProfileHMR,
		Status:        models.StatusResolved,
	}}}
	content := GenerateStandaloneCaddyfile(project, 80, UpstreamModeDockerNetwork)
	if !strings.Contains(content, "reverse_proxy web:5173") {
		t.Fatalf("expected stable container port in Docker export:\n%s", content)
	}
}

func TestGenerateServiceBlockUsesHMRHeadersForHMREntries(t *testing.T) {
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "web", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved}}}
	block := GenerateServiceBlock(project, &project.Services[0], 8888, UpstreamModeHost)

	if !strings.Contains(block, "header_up Host {host}") {
		t.Fatalf("expected HMR headers in block:\n%s", block)
	}
	if strings.Contains(block, "X-Forwarded-For") || strings.Contains(block, "X-Forwarded-Proto") {
		t.Fatalf("did not expect default forwarded headers in block:\n%s", block)
	}
}

func TestGenerateServiceBlockUsesSimpleProxyForStandardEntries(t *testing.T) {
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "api", Port: 8000, Profile: models.ProfileStandard, Status: models.StatusResolved}}}
	block := GenerateServiceBlock(project, &project.Services[0], 8888, UpstreamModeHost)

	if strings.Contains(block, "header_up Host {host}") {
		t.Fatalf("did not expect HMR headers in block:\n%s", block)
	}
}

func TestGenerateGlobalCaddyfileIsPreformatted(t *testing.T) {
	content := GenerateGlobalCaddyfile(8888, "127.0.0.1:20190")

	if strings.Contains(content, "\n\n{\n") {
		t.Fatalf("did not expect extra blank line before global block:\n%s", content)
	}
	if !strings.Contains(content, "auto_https disable_redirects") {
		t.Fatalf("expected HTTPS proxy not to claim the separate HTTP redirect port:\n%s", content)
	}
}

func TestGenerateServiceBlockUsesHTTPSForSingleServiceRepo(t *testing.T) {
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "web", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved}}}
	block := GenerateServiceBlock(project, &project.Services[0], 8888, UpstreamModeHost)

	if !strings.Contains(block, "https://demo.localhost:8888") || !strings.Contains(block, "tls internal") {
		t.Fatalf("expected local HTTPS with the Caddy internal CA:\n%s", block)
	}
}

func TestGenerateServiceBlockUsesServiceSuffixForMultiServiceRepo(t *testing.T) {
	project := &models.Project{
		Name: "demo",
		Services: []models.Service{
			{Name: "web", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved},
			{Name: "api", Port: 8000, Profile: models.ProfileStandard, Status: models.StatusResolved},
		},
	}
	block := GenerateServiceBlock(project, &project.Services[0], 8888, UpstreamModeHost)

	if !strings.Contains(block, "https://demo-web.localhost:8888") {
		t.Fatalf("expected multi-service hostname with service suffix:\n%s", block)
	}
}
