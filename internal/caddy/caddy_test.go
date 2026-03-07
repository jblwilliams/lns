package caddy

import (
	"strings"
	"testing"

	"lns/internal/models"
)

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
}

func TestGenerateServiceBlockUsesProjectHostnameForSingleServiceRepo(t *testing.T) {
	project := &models.Project{Name: "demo", Services: []models.Service{{Name: "web", Port: 5179, Profile: models.ProfileHMR, Status: models.StatusResolved}}}
	block := GenerateServiceBlock(project, &project.Services[0], 8888, UpstreamModeHost)

	if !strings.Contains(block, "http://demo.localhost:8888") {
		t.Fatalf("expected single-service hostname without service suffix:\n%s", block)
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

	if !strings.Contains(block, "http://demo-web.localhost:8888") {
		t.Fatalf("expected multi-service hostname with service suffix:\n%s", block)
	}
}
