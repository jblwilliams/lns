package projectplan

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	listener := httpListener(t, service)
	if service.Name != "momentum" || listener.Port.Strategy != PortDynamic || !service.Default {
		t.Fatalf("unexpected service: %#v", service)
	}
	if listener.Hostname != "momentum.localhost" || listener.URL != "http://momentum.localhost" {
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
    "web": {"root": ".", "script": "dev", "profile": "hmr"}
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
	if got := httpListener(t, plan.Services[0]).URL; got != "https://acme-store.localhost:8443" {
		t.Fatalf("unexpected routed URL: %q", got)
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

func TestBuildRejectsLegacyDeploymentFieldsInOptionalConfig(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "lns.json"), `{
  "name": "demo",
  "services": {
    "web": {"root": ".", "script": "dev", "profile": "hmr", "port": 9000}
  }
}`)

	if _, err := Build(root, Route{Scheme: "http", Port: 80}); err == nil || !strings.Contains(err.Error(), "unknown field \"port\"") {
		t.Fatalf("expected strict local-only config error, got %v", err)
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

	observed := httpListener(t, plan.Services[0]).Port.Observed
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

func TestExplicitRemoteEnvironmentBlocksLocalExampleInference(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name":"demo", "private":true,
  "scripts":{"dev":"vite","server":"node server.js"},
  "dependencies":{"express":"^5"}, "devDependencies":{"vite":"^7"}
}`)
	mustWrite(t, filepath.Join(root, ".env"), "VITE_API_URL=https://staging.example.com\n")
	mustWrite(t, filepath.Join(root, ".env.example"), "VITE_API_URL=http://localhost:3001\n")

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	assertNoBinding(t, serviceNamed(t, plan, "web"), "VITE_API_URL")

	mustWrite(t, filepath.Join(root, ".env.local"), "VITE_API_URL=http://staging-api:8080\n")
	plan, err = Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	assertNoBinding(t, serviceNamed(t, plan, "web"), "VITE_API_URL")

	t.Setenv("VITE_API_URL", "https://shell.example.com")
	mustWrite(t, filepath.Join(root, ".env"), "VITE_API_URL=http://localhost:3001\n")
	plan, err = Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	assertNoBinding(t, serviceNamed(t, plan, "web"), "VITE_API_URL")
}

func TestExactComposeServiceHostIsEligibleForLocalInference(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name":"demo", "private":true, "workspaces":["web","server-ts"]
}`)
	mustWrite(t, filepath.Join(root, "web", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "server-ts", "package.json"), `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4"}}`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), `services:
  demo_server_ts:
    environment:
      PORT: 8787
  demo_web:
    environment:
      VITE_API_URL: http://demo_server_ts:8787
`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	assertBinding(t, serviceNamed(t, plan, "web"), "VITE_API_URL", BindingURL, "server-ts")
}

func TestBuildInfersMomentumAPIFromGenericRole(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{"name":"momentum","private":true,"workspaces":["web","server-ts","desktop","reader-mode"]}`)
	mustWrite(t, filepath.Join(root, "web", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "server-ts", "package.json"), `{"scripts":{"dev":"tsx watch src/index.ts"},"dependencies":{"hono":"^4"}}`)
	mustWrite(t, filepath.Join(root, "desktop", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "reader-mode", "package.json"), `{"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`)
	mustWrite(t, filepath.Join(root, "reader-mode", "vite.config.js"), `const target = process.env.API_PROXY_TARGET || "http://localhost:8787"`)
	mustWrite(t, filepath.Join(root, "docker-compose.yml"), `services:
  momentum_web:
    environment:
      VITE_MOMENTUM_API_BASE: http://momentum-api.localhost
  momentum_reader_mode:
    environment:
      API_PROXY_TARGET: http://momentum-api.localhost
`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	if plan.Project.Name != "momentum" {
		t.Fatalf("unexpected project identity: %#v", plan.Project)
	}
	web := serviceNamed(t, plan, "web")
	server := serviceNamed(t, plan, "server-ts")
	desktop := serviceNamed(t, plan, "desktop")
	if !web.Default || !server.Default || desktop.Default {
		t.Fatalf("unexpected default graph: web=%t server=%t desktop=%t", web.Default, server.Default, desktop.Default)
	}
	if got := httpListener(t, server).URL; got != "http://momentum-api.localhost" {
		t.Fatalf("expected API route alias, got %q", got)
	}
	assertBinding(t, web, "VITE_MOMENTUM_API_BASE", BindingURL, "server-ts")
	assertNoBinding(t, web, "API_PROXY_TARGET")
	reader := serviceNamed(t, plan, "reader-mode")
	assertBinding(t, reader, "API_PROXY_TARGET", BindingURL, "server-ts")
}

func TestRouteAliasPreservesWorktreePrefix(t *testing.T) {
	plan := Plan{
		Project:  Project{Name: "momentum", Worktree: "fix-auth"},
		Services: []Service{{Name: "server-ts", Listeners: []Listener{{Name: "http", Public: true}}}},
	}
	applyRouteAlias(&plan, Route{Scheme: "http", Port: 80}, EndpointRef{Service: "server-ts", Listener: "http"}, "momentum-api.localhost")
	listener := plan.Services[0].Listeners[0]
	if listener.Hostname != "fix-auth.momentum-api.localhost" || listener.URL != "http://fix-auth.momentum-api.localhost" {
		t.Fatalf("route alias lost worktree identity: %#v", listener)
	}
}

func TestBuildRetainsMeaningfulWrapperAndInfersCompoundListeners(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "package.json"), `{
  "name":"vet-studio", "private":true, "workspaces":["dashboard"],
  "scripts":{
    "dashboard":"bash scripts/start-with-sidecar.sh pnpm --filter @vet-studio/dashboard dev",
    "dashboard:dev":"docker compose up -d postgres redis && pnpm --filter @vet-studio/dashboard dev"
  }
}`)
	mustWrite(t, filepath.Join(root, "dashboard", "package.json"), `{
  "name":"@vet-studio/dashboard",
  "scripts":{"dev":"concurrently -k \"tsx watch server.ts\" \"vite\""},
  "dependencies":{"hono":"^4"}, "devDependencies":{"vite":"^7"}
}`)
	mustWrite(t, filepath.Join(root, "dashboard", "vite.config.ts"), `
const port = Number(process.env.VITE_PORT) || 5173
const target = process.env.VITE_API_TARGET || "http://localhost:3377"
export default {server:{port,proxy:{"/api":{target}}}}
`)
	mustWrite(t, filepath.Join(root, "dashboard", "server", "config.ts"), `export const port = Number(process.env.PORT) || 3377`)
	mustWrite(t, filepath.Join(root, "scripts", "start-with-sidecar.sh"), `PORT="${SCRIBE_SILERO_SIDECAR_PORT:-8765}"; exec "$@"`)

	plan, err := Build(root, Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	service := serviceNamed(t, plan, "dashboard")
	if !service.Default || service.Root != "dashboard" || service.RunRoot != "." || service.Script != "dashboard" {
		t.Fatalf("unexpected dashboard wrapper: %#v", service)
	}
	if len(service.Listeners) != 3 {
		t.Fatalf("expected web, api, and sidecar listeners, got %#v", service.Listeners)
	}
	public := httpListener(t, service)
	if public.URL != "http://vet-studio.localhost" || !reflect.DeepEqual(public.Environment, []string{"VITE_PORT"}) {
		t.Fatalf("unexpected public listener: %#v", public)
	}
	api, ok := listenerByName(service, "api")
	if !ok || api.Public || !reflect.DeepEqual(api.Environment, []string{"PORT"}) {
		t.Fatalf("unexpected API listener: %#v", api)
	}
	for _, binding := range service.Environment {
		if binding.Name == "VITE_API_TARGET" && binding.Target == (EndpointRef{Service: "dashboard", Listener: "api"}) && binding.Network == NetworkLoopback {
			return
		}
	}
	t.Fatalf("missing private API target: %#v", service.Environment)
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
	observed := httpListener(t, service).Port.Observed
	if len(observed) != 1 || observed[0].Value != want {
		t.Fatalf("expected %s observed port %d, got %#v", service.Name, want, observed)
	}
}

func assertBinding(t *testing.T, service Service, name string, kind BindingKind, target string) {
	t.Helper()
	for _, binding := range service.Environment {
		if binding.Name == name && binding.Kind == kind && binding.Target.Service == target {
			return
		}
	}
	t.Fatalf("expected %s binding %s -> %s, got %#v", service.Name, name, target, service.Environment)
}

func assertNoBinding(t *testing.T, service Service, name string) {
	t.Helper()
	for _, binding := range service.Environment {
		if binding.Name == name {
			t.Fatalf("unexpected %s binding %s: %#v", service.Name, name, service.Environment)
		}
	}
}

func httpListener(t *testing.T, service Service) Listener {
	t.Helper()
	for _, listener := range service.Listeners {
		if listener.Name == "http" {
			return listener
		}
	}
	t.Fatalf("service %q has no http listener: %#v", service.Name, service.Listeners)
	return Listener{}
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
