package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"

	"lns/internal/caddy"
	"lns/internal/config"
	"lns/internal/models"
	"lns/internal/projectconfig"
	"lns/internal/projectplan"
)

func TestExportCommandDefaultsToContainerHTTPPort(t *testing.T) {
	flag := exportCmd.Flags().Lookup("proxy-port")
	if flag == nil {
		t.Fatal("expected export --proxy-port flag")
	}
	if flag.DefValue != strconv.Itoa(80) {
		t.Fatalf("expected Docker export port 80, got %s", flag.DefValue)
	}
}

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
			Name: "web", Root: ".", Script: "dev", Profile: models.ProfileHMR,
			State: projectplan.StateManaged, Hostname: "fix-auth.demo.localhost", URL: "https://fix-auth.demo.localhost:8443",
		}},
	}

	run, err := prepareServiceRun(repo, plan, plan.Services[0], map[string]int{"web": 4312}, 1234)
	if err != nil {
		t.Fatalf("prepare service run: %v", err)
	}
	wantCommand := []string{"pnpm", "exec", "vite", "--port", "4312", "--host", "127.0.0.1"}
	if !reflect.DeepEqual(run.Command, wantCommand) {
		t.Fatalf("expected command %#v, got %#v", wantCommand, run.Command)
	}
	if run.Lease.Hostname != "fix-auth.demo.localhost" || run.URL != "https://fix-auth.demo.localhost:8443/" {
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
			{Name: "server", Root: ".", Script: "server", Profile: models.ProfileStandard, State: projectplan.StateManaged, Hostname: "peyra-server.localhost", URL: "http://peyra-server.localhost"},
			{Name: "web", Root: ".", Script: "dev", Profile: models.ProfileHMR, State: projectplan.StateManaged, Hostname: "peyra-web.localhost", URL: "http://peyra-web.localhost"},
		},
	}

	run, err := prepareServiceRun(repo, plan, plan.Services[1], map[string]int{"server": 4301, "web": 4302}, 1234)
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

	plan, err := buildRunPlan(repo, config.Settings{HTTPPort: 80, HTTPS: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Services) != 1 || plan.Services[0].Name != "demo" {
		t.Fatalf("unexpected plan: %#v", plan)
	}
	if _, err := os.Stat(filepath.Join(repo, projectconfig.Filename)); !os.IsNotExist(err) {
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
	plan, err := buildRunPlan(repo, config.Settings{HTTPPort: 80, HTTPS: false})
	if err != nil {
		t.Fatal(err)
	}
	ports := map[string]int{"server": 4301, "web": 4302}
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

func containsEnv(env []string, want string) bool {
	for _, value := range env {
		if value == want {
			return true
		}
	}
	return false
}

func TestSyncProjectWritesRegistryAndCaddyState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &projectconfig.Config{
		Name: "demo",
		Services: map[string]projectconfig.Service{
			"frontend": {
				Root:    ".",
				Port:    3988,
				Profile: models.ProfileHMR,
				Status:  models.StatusResolved,
			},
		},
	}

	if err := syncProject(repo, cfg); err != nil {
		t.Fatalf("syncProject: %v", err)
	}

	if _, err := os.Stat(config.GetRegistryPath()); err != nil {
		t.Fatalf("expected registry file: %v", err)
	}
	if _, err := os.Stat(config.GetGlobalCaddyfilePath()); err != nil {
		t.Fatalf("expected global caddyfile: %v", err)
	}
	if _, err := os.Stat(caddy.ProjectCaddyfilePath("demo")); err != nil {
		t.Fatalf("expected project caddyfile: %v", err)
	}
}

func TestSyncProjectBlocksUnresolvedServicesWithoutMutatingState(t *testing.T) {
	home := t.TempDir()
	repo := t.TempDir()
	t.Setenv("HOME", home)

	cfg := &projectconfig.Config{
		Name: "demo",
		Services: map[string]projectconfig.Service{
			"frontend": {
				Root:   ".",
				Status: models.StatusUnresolved,
			},
		},
	}

	if err := syncProject(repo, cfg); err == nil {
		t.Fatal("expected unresolved sync to fail")
	}

	if _, err := os.Stat(config.GetRegistryPath()); !os.IsNotExist(err) {
		t.Fatalf("expected no registry mutation, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(config.GetCaddyConfigDir(), "demo.caddy")); !os.IsNotExist(err) {
		t.Fatalf("expected no project caddyfile, got %v", err)
	}
}
