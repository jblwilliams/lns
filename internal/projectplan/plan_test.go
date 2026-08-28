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

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
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

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
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

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
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

func TestBuildUsesRouteSettingsAndCanonicalScopedName(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "@acme/store",
  "private": true,
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^7"}
}`)

	plan, err := Build(root, Route{Scheme: "https", Port: 8443})
	if err != nil {
		t.Fatal(err)
	}

	if got := ProjectName(root); got != "acme-store" {
		t.Fatalf("expected canonical scoped name, got %q", got)
	}
	if plan.Services[0].URL != "https://acme-store.localhost:8443" {
		t.Fatalf("unexpected routed URL: %q", plan.Services[0].URL)
	}
}

func TestProjectNamePrefersComposeIdentity(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"peyra-proto","private":true}`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), "name: peyra\n\nservices: {}\n")

	if got := ProjectName(root); got != "peyra" {
		t.Fatalf("expected Compose identity, got %q", got)
	}
}

func TestBuildSurfacesUnresolvedAndExternalServicesHonestly(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "lns.json"), `{
  "name": "demo",
  "services": {
    "broken": {"root": ".", "script": "dev", "status": "unresolved"},
    "proxy": {"root": ".", "port": 9000, "profile": "standard", "status": "resolved"}
  }
}`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}

	if plan.Services[0].Name != "broken" || plan.Services[0].State != StateUnresolved || plan.Services[0].Port.Strategy != PortUnresolved {
		t.Fatalf("unexpected unresolved service: %#v", plan.Services[0])
	}
	if plan.Services[1].Name != "proxy" || plan.Services[1].State != StateExternal || plan.Services[1].Port.Strategy != PortFixed || plan.Services[1].Port.Fixed != 9000 {
		t.Fatalf("unexpected external service: %#v", plan.Services[1])
	}
	if len(plan.Warnings) != 1 || plan.Warnings[0].Code != "unresolved-service" {
		t.Fatalf("expected one unresolved warning, got %#v", plan.Warnings)
	}
	if plan.Warnings[0].Recovery != "edit lns.json and set a valid profile plus script, command, or port" {
		t.Fatalf("unexpected recovery: %#v", plan.Warnings[0])
	}
}

func TestBuildKeepsPortEvidenceAttachedToPortSource(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "demo",
  "private": true,
  "scripts": {"dev": "vite"},
  "devDependencies": {"vite": "^7"}
}`)
	mustWrite(t, filepath.Join(root, ".env"), "VITE_PORT=4173\n")

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}

	observed := plan.Services[0].Port.Observed
	if len(observed) != 1 || observed[0].Value != 4173 || observed[0].Evidence != ".env:VITE_PORT" {
		t.Fatalf("unexpected observed port provenance: %#v", observed)
	}
}

func TestBuildWarnsWhenNoServicesAreDiscovered(t *testing.T) {
	plan, err := Build(t.TempDir(), Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Services) != 0 || len(plan.Warnings) != 1 || plan.Warnings[0].Code != "no-services" {
		t.Fatalf("unexpected empty plan: %#v", plan)
	}
}

func TestBuildInfersPeyraStylePortsAndLinks(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name": "peyra",
  "private": true,
  "scripts": {"dev": "vite", "server": "node server.js"},
  "dependencies": {"express": "^5"},
  "devDependencies": {"vite": "^7"}
}`)
	mustWrite(t, filepath.Join(root, ".env.example"), `CLIENT_PORT=5173
SERVER_PORT=3001
CORS_ORIGIN=http://localhost:5173
VITE_API_URL=http://localhost:3001
DATABASE_URL=postgres://postgres:postgres@localhost:5432/peyra
`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	web := serviceNamed(t, plan, "web")
	server := serviceNamed(t, plan, "server")
	assertObservedPort(t, web, 5173)
	assertObservedPort(t, server, 3001)
	assertBinding(t, web, "CLIENT_PORT", BindingPort, "web")
	assertBinding(t, web, "SERVER_PORT", BindingPort, "server")
	assertBinding(t, web, "VITE_API_URL", BindingURL, "server")
	assertBinding(t, server, "CORS_ORIGIN", BindingURL, "web")
	for _, service := range plan.Services {
		for _, binding := range service.Environment {
			if binding.Name == "DATABASE_URL" {
				t.Fatal("database values must not be exposed as HTTP service links")
			}
		}
	}
}

func TestBuildInfersMomentumAPIFromGenericRole(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"momentum","private":true,"workspaces":["web","server-ts","desktop"]}`)
	mustWrite(t, filepath.Join(root, "web", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "server-ts", "package.json"), `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4"}}`)
	mustWrite(t, filepath.Join(root, "desktop", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), `services:
  momentum_web:
    environment:
      VITE_MOMENTUM_API_BASE: http://momentum-api.localhost
`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project.Name != "momentum" {
		t.Fatalf("unexpected project identity: %#v", plan.Project)
	}
	web := serviceNamed(t, plan, "web")
	assertBinding(t, web, "VITE_MOMENTUM_API_BASE", BindingURL, "server-ts")
}

func serviceNamed(t *testing.T, plan Plan, name string) Service {
	t.Helper()
	for _, service := range plan.Services {
		if service.Name == name {
			return service
		}
	}
	t.Fatalf("service %q not found in %#v", name, plan.Services)
	return Service{}
}

func assertObservedPort(t *testing.T, service Service, want int) {
	t.Helper()
	if len(service.Port.Observed) != 1 || service.Port.Observed[0].Value != want {
		t.Fatalf("expected %s observed port %d, got %#v", service.Name, want, service.Port.Observed)
	}
}

func assertBinding(t *testing.T, service Service, name string, kind BindingKind, target string) {
	t.Helper()
	for _, binding := range service.Environment {
		if binding.Name == name && binding.Kind == kind && binding.Target == target {
			return
		}
	}
	t.Fatalf("expected %s binding %s -> %s, got %#v", service.Name, name, target, service.Environment)
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
