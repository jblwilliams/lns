package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"lns/internal/models"
	"lns/internal/projectconfig"
)

func TestBootstrapConfigDetectsResolvedWebAtRepoRoot(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{
  "name": "demo",
  "scripts": {
    "dev": "vite --port 5179"
  },
  "devDependencies": {
    "vite": "^6.0.0"
  }
}`)

	cfg := BootstrapConfig("demo", root, "")

	service, exists := cfg.Services["web"]
	if !exists {
		t.Fatalf("expected web service, got %#v", cfg.Services)
	}
	if service.Root != "." {
		t.Fatalf("expected root '.', got %q", service.Root)
	}
	if service.Port != 5179 {
		t.Fatalf("expected port 5179, got %d", service.Port)
	}
	if service.Profile != models.ProfileHMR {
		t.Fatalf("expected hmr profile, got %q", service.Profile)
	}
	if service.Status != models.StatusResolved {
		t.Fatalf("expected resolved status, got %q", service.Status)
	}
	if service.Source != models.SourceDetected {
		t.Fatalf("expected detected source, got %q", service.Source)
	}
}

func TestBootstrapConfigCreatesUnresolvedServiceWithoutPortFallback(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{
  "name": "demo",
  "scripts": {
    "dev": "next dev"
  },
  "dependencies": {
    "next": "15.0.0"
  }
}`)

	cfg := BootstrapConfig("demo", root, "")

	service := cfg.Services["web"]
	if service.Port != 0 {
		t.Fatalf("expected no inferred port, got %d", service.Port)
	}
	if service.Profile != models.ProfileHMR {
		t.Fatalf("expected hmr profile, got %q", service.Profile)
	}
	if service.Status != models.StatusUnresolved {
		t.Fatalf("expected unresolved status, got %q", service.Status)
	}
}

func TestDetectServicesFindsAppsFolderService(t *testing.T) {
	root := t.TempDir()
	serviceRoot := filepath.Join(root, "apps", "frontend")
	mustWriteFile(t, filepath.Join(serviceRoot, "package.json"), `{
  "name": "frontend",
  "scripts": {
    "dev": "vite --port 4123"
  },
  "devDependencies": {
    "vite": "^6.0.0"
  }
}`)

	services := DetectServices(root)
	if len(services) != 1 {
		t.Fatalf("expected 1 service, got %d", len(services))
	}

	service := services[0]
	if service.Name != "web" {
		t.Fatalf("expected service name web, got %q", service.Name)
	}
	if service.Root != filepath.Join("apps", "frontend") {
		t.Fatalf("expected root apps/frontend, got %q", service.Root)
	}
	if service.Port != 4123 {
		t.Fatalf("expected port 4123, got %d", service.Port)
	}
}

func TestDetectDriftReportsPortAndProfileMismatch(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{
  "name": "demo",
  "scripts": {
    "dev": "vite --port 5179"
  },
  "devDependencies": {
    "vite": "^6.0.0"
  }
}`)

	cfg := &projectconfig.Config{
		Name: "demo",
		Services: map[string]projectconfig.Service{
			"web": {
				Root:    ".",
				Port:    3000,
				Profile: models.ProfileStandard,
				Status:  models.StatusResolved,
			},
		},
	}

	drifts := DetectDrift(root, cfg)
	if len(drifts) != 2 {
		t.Fatalf("expected 2 drifts, got %d: %#v", len(drifts), drifts)
	}
}

func mustWriteFile(t *testing.T, path, contents string) {
	t.Helper()
	mustMkdir(t, filepath.Dir(path))
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func mustMkdir(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", path, err)
	}
}
