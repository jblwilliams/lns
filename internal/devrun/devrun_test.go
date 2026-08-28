package devrun

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"lns/internal/models"
)

func TestResolveCommandRunsSimpleViteScriptOnLeasedPort(t *testing.T) {
	root := t.TempDir()
	packageJSON := `{"packageManager":"pnpm@10.0.0","scripts":{"dev":"vite --port 5179"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveCommand(root, models.Service{Root: ".", Script: "dev"}, 4312)
	if err != nil {
		t.Fatalf("resolve command: %v", err)
	}
	want := []string{"pnpm", "exec", "vite", "--port", "4312", "--host", "127.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestResolveCommandOverridesFixedNextPort(t *testing.T) {
	root := t.TempDir()
	packageJSON := `{"scripts":{"dev":"next dev -p 3000"},"dependencies":{"next":"15"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := ResolveCommand(root, models.Service{Root: ".", Script: "dev"}, 4312)
	if err != nil {
		t.Fatalf("resolve command: %v", err)
	}
	want := []string{"npm", "exec", "next", "dev", "-p", "4312", "--hostname", "127.0.0.1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestApplyWorktreePrefixCreatesBranchSubdomain(t *testing.T) {
	if got := ApplyWorktreePrefix("demo-web.localhost", "fix-auth"); got != "fix-auth.demo-web.localhost" {
		t.Fatalf("unexpected worktree hostname %q", got)
	}
}

func TestWorktreeLabelFallsBackToDirectoryForDetachedHead(t *testing.T) {
	if got := worktreeLabel("", filepath.Join("tmp", "demo-fix-auth")); got != "demo-fix-auth" {
		t.Fatalf("unexpected detached worktree label %q", got)
	}
}

func TestPortEnvironmentUsesVitePortForCompoundViteScript(t *testing.T) {
	root := t.TempDir()
	packageJSON := `{"scripts":{"dev":"concurrently \"tsx watch server.ts\" \"vite\""}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatal(err)
	}

	got := PortEnvironment(root, models.Service{Name: "dashboard", Root: ".", Script: "dev", Profile: models.ProfileHMR}, 4312)
	want := []string{"LNS_PORT=4312", "VITE_PORT=4312"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}

func TestPortEnvironmentAddsNamedPortForServerScript(t *testing.T) {
	root := t.TempDir()
	packageJSON := `{"scripts":{"server":"node src/server/dev.ts"}}`
	if err := os.WriteFile(filepath.Join(root, "package.json"), []byte(packageJSON), 0644); err != nil {
		t.Fatal(err)
	}

	got := PortEnvironment(root, models.Service{Name: "server", Root: ".", Script: "server", Profile: models.ProfileStandard}, 4312)
	want := []string{"LNS_PORT=4312", "PORT=4312", "SERVER_PORT=4312"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("expected %#v, got %#v", want, got)
	}
}
