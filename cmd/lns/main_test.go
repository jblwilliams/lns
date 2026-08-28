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
	cfg := &projectconfig.Config{
		Name: "demo",
		Services: map[string]projectconfig.Service{
			"web": {Root: ".", Script: "dev", ContainerPort: 5173, Profile: models.ProfileHMR, Status: models.StatusResolved},
		},
	}

	run, err := prepareServiceRun(repo, cfg, "web", 4312, 1234, "fix-auth", config.Settings{HTTPPort: 8443, HTTPS: true})
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
	if cfg.Services["web"].ContainerPort != 5173 {
		t.Fatal("preparing a development run must not change deployment metadata")
	}
	if !containsEnv(run.Env, "LNS_WEB_URL=https://fix-auth.demo.localhost:8443") || !containsEnv(run.Env, "VITE_LNS_WEB_URL=https://fix-auth.demo.localhost:8443") {
		t.Fatalf("expected service URLs in child environment, got %#v", run.Env)
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
