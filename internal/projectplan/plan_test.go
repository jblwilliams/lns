package projectplan

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestBuildDiscoversPlanWithoutWritingRepository(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "momentum",
  "private": true,
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^7"}
}`)
	before := mustEntries(t, root)

	plan, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if plan.SchemaVersion != 1 {
		t.Fatalf("expected schema version 1, got %d", plan.SchemaVersion)
	}
	if plan.Project.Name != "momentum" || plan.Project.Source != SourceDiscovered {
		t.Fatalf("unexpected project: %#v", plan.Project)
	}
	if len(plan.Services) != 1 {
		t.Fatalf("expected one service, got %#v", plan.Services)
	}
	service := plan.Services[0]
	if service.Name != "momentum" || service.Port.Strategy != PortDynamic {
		t.Fatalf("unexpected service: %#v", service)
	}
	if service.Hostname != "momentum.localhost" || service.URL != "http://momentum.localhost" {
		t.Fatalf("unexpected local name: %#v", service)
	}
	if after := mustEntries(t, root); !reflect.DeepEqual(after, before) {
		t.Fatalf("planning changed repository entries: before=%v after=%v", before, after)
	}
	if _, err := os.Stat(filepath.Join(root, "lns.json")); !os.IsNotExist(err) {
		t.Fatalf("planning must not create lns.json, got %v", err)
	}
}

func TestBuildUsesExplicitConfigWithoutRepairingIt(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "lns.json"), `{
  "name": "custom",
  "services": {
    "web": {"root": ".", "script": "dev", "profile": "hmr", "status": "resolved"}
  }
}`)
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "ignored",
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^7"}
}`)
	before, err := os.ReadFile(filepath.Join(root, "lns.json"))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := Build(root)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	if plan.Project.Name != "custom" || plan.Project.Source != SourceConfig {
		t.Fatalf("unexpected project: %#v", plan.Project)
	}
	if len(plan.Services) != 1 || plan.Services[0].Name != "web" {
		t.Fatalf("unexpected services: %#v", plan.Services)
	}
	after, err := os.ReadFile(filepath.Join(root, "lns.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Fatal("planning repaired or rewrote explicit config")
	}
}

func TestBuildSortsServicesAndEvidence(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"suite","private":true}`)
	mustWrite(t, filepath.Join(root, "apps", "zeta", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "apps", "alpha", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)

	plan, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{plan.Services[0].Name, plan.Services[1].Name}; !reflect.DeepEqual(got, []string{"alpha", "zeta"}) {
		t.Fatalf("services not stable: %v", got)
	}
	for _, service := range plan.Services {
		if !reflect.DeepEqual(service.Evidence, []string{"package.json", "package.json script dev"}) {
			t.Fatalf("evidence not sorted for %s: %v", service.Name, service.Evidence)
		}
	}
}

func mustWrite(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}

func mustEntries(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}
