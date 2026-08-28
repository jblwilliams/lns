package devrun

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestPackageManagerLockfilePrecedenceIsDeterministic(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"pnpm-lock.yaml", "yarn.lock", "package-lock.json"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0644); err != nil {
			t.Fatal(err)
		}
	}
	for attempt := 0; attempt < 20; attempt++ {
		if got := packageManager(root, root, ""); got != "pnpm" {
			t.Fatalf("expected pnpm precedence, got %q", got)
		}
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

func TestOverlayEnvironmentReplacesDuplicatesExactlyOnce(t *testing.T) {
	base := []string{"PATH=/bin", "PORT=3000", "PORT=3001", "HOME=/tmp/home"}

	got := OverlayEnvironment(base, map[string]string{"PORT": "4300", "SERVER_PORT": "4301"})

	counts := map[string]int{}
	values := map[string]string{}
	for _, item := range got {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			t.Fatalf("invalid environment entry %q", item)
		}
		counts[key]++
		values[key] = value
	}
	if counts["PORT"] != 1 || values["PORT"] != "4300" {
		t.Fatalf("expected one overlaid PORT, got %#v", got)
	}
	if counts["SERVER_PORT"] != 1 || values["SERVER_PORT"] != "4301" {
		t.Fatalf("expected one SERVER_PORT, got %#v", got)
	}
	if values["PATH"] != "/bin" || values["HOME"] != "/tmp/home" {
		t.Fatalf("base environment was not preserved: %#v", got)
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
