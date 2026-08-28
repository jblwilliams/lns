package discovery

import (
	"os"
	"path/filepath"
	"testing"

	"lns/internal/models"
)

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
	if service.Name != "frontend" {
		t.Fatalf("expected service name frontend, got %q", service.Name)
	}
	if service.Root != filepath.Join("apps", "frontend") {
		t.Fatalf("expected root apps/frontend, got %q", service.Root)
	}
	if service.Port != 4123 {
		t.Fatalf("expected port 4123, got %d", service.Port)
	}
}

func TestDetectServicesFindsRunnableWorkspacePackages(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{"workspaces":["web","server-ts"]}`)
	mustWriteFile(t, filepath.Join(root, "web", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWriteFile(t, filepath.Join(root, "server-ts", "package.json"), `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4"}}`)

	services := DetectServices(root)
	if len(services) != 2 {
		t.Fatalf("expected web and server-ts workspace services, got %#v", services)
	}
	if services[0].Script != "dev" || services[1].Script != "dev" {
		t.Fatalf("expected runnable dev scripts, got %#v", services)
	}
}

func TestDetectServicesFindsPnpmWorkspacePackages(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "pnpm-workspace.yaml"), "packages:\n  - dashboard\n  - 'packages/*'\n")
	mustWriteFile(t, filepath.Join(root, "dashboard", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)

	services := DetectServices(root)
	if len(services) != 1 || services[0].Name != "dashboard" {
		t.Fatalf("expected dashboard from pnpm workspace, got %#v", services)
	}
}

func TestDetectServicesFindsFrontendAndSiblingServerScripts(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{
  "scripts": {
    "dev": "vite",
    "server": "node src/server/dev.ts"
  },
  "dependencies": {"express": "^5"},
  "devDependencies": {"vite": "^7"}
}`)

	services := DetectServices(root)
	if len(services) != 2 {
		t.Fatalf("expected web and server services, got %#v", services)
	}
	if services[0].Name != "server" || services[0].Script != "server" || services[0].Profile != models.ProfileStandard {
		t.Fatalf("expected server script service, got %#v", services[0])
	}
	if services[1].Name != "web" || services[1].Script != "dev" || services[1].Profile != models.ProfileHMR {
		t.Fatalf("expected web dev service, got %#v", services[1])
	}
}

func TestDetectServicesPreservesCommonDirectoryNames(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "client", "package.json"), `{
  "name": "client",
  "scripts": {
    "dev": "vite --port 4123"
  },
  "devDependencies": {
    "vite": "^6.0.0"
  }
}`)
	mustWriteFile(t, filepath.Join(root, "server", "requirements.txt"), "fastapi\n")
	mustWriteFile(t, filepath.Join(root, "server", ".env"), "PORT=8000\n")

	services := DetectServices(root)
	if len(services) != 2 {
		t.Fatalf("expected 2 services, got %d", len(services))
	}

	if services[0].Name != "client" {
		t.Fatalf("expected first service name client, got %q", services[0].Name)
	}
	if services[1].Name != "server" {
		t.Fatalf("expected second service name server, got %q", services[1].Name)
	}
}

func TestDetectServicesDoesNotTreatDatabaseURLAsHTTPListener(t *testing.T) {
	root := t.TempDir()
	mustWriteFile(t, filepath.Join(root, "package.json"), `{"scripts":{"dev":"tsx watch server.ts"},"dependencies":{"hono":"^4"}}`)
	mustWriteFile(t, filepath.Join(root, ".env"), "DATABASE_URL=postgres://user:pass@localhost:5432/app\nREDIS_URL=redis://localhost:6379\n")

	services := DetectServices(root)
	if len(services) != 1 {
		t.Fatalf("expected one service, got %#v", services)
	}
	if services[0].Port != 0 || services[0].PortEvidence != "" {
		t.Fatalf("database dependency was mistaken for HTTP listener: %#v", services[0])
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
