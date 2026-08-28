package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lns/internal/models"
	"lns/internal/projectplan"
)

func TestBareCommandRejectsUnexpectedArguments(t *testing.T) {
	if err := rootCmd.Args(rootCmd, []string{"typo"}); err == nil {
		t.Fatal("expected bare lns to reject unexpected arguments")
	}
}

func TestPrepareServiceRunUsesDynamicPortAndWorktreeHostname(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"packageManager":"pnpm@10.0.0","scripts":{"dev":"vite"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := projectplan.Plan{
		Project: projectplan.Project{Name: "demo", Worktree: "fix-auth"},
		Services: []projectplan.Service{{
			Name: "web", Root: ".", Script: "dev", State: projectplan.StateManaged, Default: true,
			Listeners: []projectplan.Listener{{Name: "http", Profile: models.ProfileHMR, Public: true, Hostname: "fix-auth.demo.localhost", URL: "https://fix-auth.demo.localhost:8443", Environment: []string{"VITE_PORT"}}},
		}},
	}

	run, err := prepareServiceRun(repo, plan, plan.Services[0], endpointPorts(map[string]int{"web": 4312}), 1234)
	if err != nil {
		t.Fatalf("prepare service run: %v", err)
	}
	wantCommand := []string{"pnpm", "exec", "vite", "--port", "4312", "--host", "127.0.0.1"}
	if !reflect.DeepEqual(run.Command, wantCommand) {
		t.Fatalf("expected command %#v, got %#v", wantCommand, run.Command)
	}
	if len(run.Leases) != 1 || run.Leases[0].Hostname != "fix-auth.demo.localhost" || run.URL != "https://fix-auth.demo.localhost:8443/" {
		t.Fatalf("unexpected runtime route: %#v", run)
	}
	if !containsEnv(run.Env, "LNS_WEB_URL=https://fix-auth.demo.localhost:8443") || !containsEnv(run.Env, "VITE_LNS_WEB_URL=https://fix-auth.demo.localhost:8443") {
		t.Fatalf("expected service URLs in child environment, got %#v", run.Env)
	}
}

func TestPrepareServiceRunReceivesEveryManagedServicePort(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"scripts":{"dev":"vite","server":"node server.js"},"devDependencies":{"vite":"^7"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	plan := projectplan.Plan{
		Project: projectplan.Project{Name: "peyra"},
		Services: []projectplan.Service{
			{Name: "server", Root: ".", Script: "server", State: projectplan.StateManaged, Default: true, Listeners: []projectplan.Listener{{Name: "http", Profile: models.ProfileStandard, Public: true, Hostname: "peyra-server.localhost", URL: "http://peyra-server.localhost", Environment: []string{"PORT"}}}},
			{Name: "web", Root: ".", Script: "dev", State: projectplan.StateManaged, Default: true, Listeners: []projectplan.Listener{{Name: "http", Profile: models.ProfileHMR, Public: true, Hostname: "peyra-web.localhost", URL: "http://peyra-web.localhost", Environment: []string{"VITE_PORT"}}}},
		},
	}

	run, err := prepareServiceRun(repo, plan, plan.Services[1], endpointPorts(map[string]int{"server": 4301, "web": 4302}), 1234)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{
		"VITE_PORT=4302",
		"SERVER_PORT=4301",
		"WEB_PORT=4302",
		"LNS_SERVER_URL=http://peyra-server.localhost",
	} {
		if !containsEnv(run.Env, expected) {
			t.Fatalf("expected %q in child environment", expected)
		}
	}
}

func TestBuildRunPlanDoesNotCreateRepositoryConfig(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{"name":"demo","private":true,"scripts":{"dev":"vite"},"devDependencies":{"vite":"^7"}}`), 0644); err != nil {
		t.Fatal(err)
	}

	plan, err := buildRunPlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Services) != 1 || plan.Services[0].Name != "demo" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(repo, "lns.json")); !os.IsNotExist(err) {
		t.Fatalf("bare planning created repository config: %v", err)
	}
}

func TestPrepareServiceRunMaterializesInferredPeyraLinks(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "package.json"), []byte(`{
  "name":"peyra",
  "private":true,
  "scripts":{"dev":"vite","server":"node server.js"},
  "dependencies":{"express":"^5"},
  "devDependencies":{"vite":"^7"}
}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".env.example"), []byte("CLIENT_PORT=5173\nSERVER_PORT=3001\nCORS_ORIGIN=http://localhost:5173\nVITE_API_URL=http://localhost:3001\n"), 0644); err != nil {
		t.Fatal(err)
	}
	plan, err := buildRunPlan(repo)
	if err != nil {
		t.Fatal(err)
	}
	ports := endpointPorts(map[string]int{"server": 4301, "web": 4302})
	var web, server projectplan.Service
	for _, service := range plan.Services {
		switch service.Name {
		case "web":
			web = service
		case "server":
			server = service
		}
	}
	webRun, err := prepareServiceRun(repo, plan, web, ports, 1234)
	if err != nil {
		t.Fatal(err)
	}
	serverRun, err := prepareServiceRun(repo, plan, server, ports, 1234)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"CLIENT_PORT=4302", "SERVER_PORT=4301", "VITE_API_URL=http://peyra-server.localhost"} {
		if !containsEnv(webRun.Env, expected) {
			t.Fatalf("web missing %q", expected)
		}
	}
	if !containsEnv(serverRun.Env, "CORS_ORIGIN=http://peyra-web.localhost") {
		t.Fatal("server missing stable web origin")
	}
}

func TestPrepareCompoundWrapperUsesAllPortsAndOnePublicLease(t *testing.T) {
	repo := t.TempDir()
	mustWriteRunFixture(t, filepath.Join(repo, "package.json"), `{
  "name":"vet-studio", "private":true, "workspaces":["dashboard"],
  "scripts":{"dashboard":"bash scripts/start-sidecar.sh pnpm --filter @vet-studio/dashboard dev"}
}`)
	mustWriteRunFixture(t, filepath.Join(repo, "pnpm-lock.yaml"), "lockfileVersion: '9.0'\n")
	mustWriteRunFixture(t, filepath.Join(repo, "dashboard", "package.json"), `{
  "name":"@vet-studio/dashboard", "scripts":{"dev":"concurrently -k \"tsx watch server.ts\" \"vite\""},
  "dependencies":{"hono":"^4"}, "devDependencies":{"vite":"^7"}
}`)
	mustWriteRunFixture(t, filepath.Join(repo, "dashboard", "vite.config.ts"), `
const port = Number(process.env.VITE_PORT) || 5173
const apiTarget = process.env.VITE_API_TARGET || "http://localhost:3377"
export default {server:{port,proxy:{"/api":{target:apiTarget}}}}
`)
	mustWriteRunFixture(t, filepath.Join(repo, "dashboard", "server", "config.ts"), `export const port = parseInt(process.env.PORT ?? "", 10) || 3377`)
	mustWriteRunFixture(t, filepath.Join(repo, "scripts", "start-sidecar.sh"), `PORT="${SCRIBE_SILERO_SIDECAR_PORT:-8765}"; exec "$@"`)

	plan, err := projectplan.Build(repo, projectplan.Route{Scheme: "http", Port: 80})
	if err != nil {
		t.Fatal(err)
	}
	service := plan.Services[0]
	ports := map[string]int{
		projectplan.EndpointKey(projectplan.EndpointRef{Service: service.Name, Listener: "http"}):           4401,
		projectplan.EndpointKey(projectplan.EndpointRef{Service: service.Name, Listener: "api"}):            4402,
		projectplan.EndpointKey(projectplan.EndpointRef{Service: service.Name, Listener: "silero-sidecar"}): 4403,
	}
	run, err := prepareServiceRun(repo, plan, service, ports, 1234)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(run.Command, []string{"pnpm", "run", "dashboard"}) || len(run.Leases) != 1 || run.Leases[0].Port != 4401 {
		t.Fatalf("unexpected compound run: %#v", run)
	}
	for _, expected := range []string{"VITE_PORT=4401", "PORT=4402", "SCRIBE_SILERO_SIDECAR_PORT=4403", "VITE_API_TARGET=http://127.0.0.1:4402"} {
		if !containsEnv(run.Env, expected) {
			t.Fatalf("missing %q in compound environment", expected)
		}
	}
}

func TestExpandServiceClosureIncludesRequiredServicesCycleSafely(t *testing.T) {
	plan := projectplan.Plan{Services: []projectplan.Service{
		{Name: "api", State: projectplan.StateManaged, Environment: []projectplan.EnvironmentBinding{{Name: "CORS_ORIGIN", Target: projectplan.EndpointRef{Service: "web", Listener: "http"}}}},
		{Name: "web", State: projectplan.StateManaged, Environment: []projectplan.EnvironmentBinding{
			{Name: "VITE_API_URL", Target: projectplan.EndpointRef{Service: "api", Listener: "http"}},
			{Name: "VITE_API_TARGET", Target: projectplan.EndpointRef{Service: "web", Listener: "api"}},
		}},
	}}
	selected, err := expandServiceClosure(plan, []projectplan.Service{plan.Services[1]})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := serviceNames(selected), []string{"api", "web"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("service closure: want %#v, got %#v", want, got)
	}
}

func TestExpandServiceClosureRejectsUnrunnableRequirement(t *testing.T) {
	plan := projectplan.Plan{Services: []projectplan.Service{
		{Name: "api", State: projectplan.StateUnresolved},
		{Name: "web", State: projectplan.StateManaged, Environment: []projectplan.EnvironmentBinding{{Name: "VITE_API_URL", Target: projectplan.EndpointRef{Service: "api", Listener: "http"}}}},
	}}
	if _, err := expandServiceClosure(plan, []projectplan.Service{plan.Services[1]}); err == nil {
		t.Fatal("expected an unresolved required service to fail before runtime mutation")
	}
}

func containsEnv(env []string, want string) bool {
	for _, value := range env {
		if value == want {
			return true
		}
	}
	return false
}

func endpointPorts(values map[string]int) map[string]int {
	result := make(map[string]int, len(values))
	for service, port := range values {
		result[projectplan.EndpointKey(projectplan.EndpointRef{Service: service, Listener: "http"})] = port
	}
	return result
}

func mustWriteRunFixture(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}
