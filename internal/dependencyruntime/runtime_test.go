package dependencyruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lns/internal/projectplan"
)

func TestSelectProvidersForRepresentativeProjectShapes(t *testing.T) {
	model := composeModel{Services: map[string]composeService{
		"server":             {DependsOn: dependencies("postgres")},
		"momentum_server_ts": {DependsOn: dependencies("postgres", "redis")},
		"postgres":           {Image: "pgvector/pgvector:pg17"},
		"postgres-test":      {Image: "postgres:18"},
		"redis":              {Image: "redis:7-alpine"},
		"localstack":         {Image: "localstack/localstack:4"},
	}}

	peyra, err := selectProviders(model, []projectplan.Service{{Name: "server"}})
	if err != nil || !reflect.DeepEqual(providerNames(peyra), []string{"postgres"}) {
		t.Fatalf("unexpected Peyra dependencies: %#v, %v", peyra, err)
	}
	momentum, err := selectProviders(model, []projectplan.Service{{Name: "server-ts"}, {Name: "web"}})
	if err != nil || !reflect.DeepEqual(providerNames(momentum), []string{"postgres", "redis"}) {
		t.Fatalf("unexpected Momentum dependencies: %#v, %v", momentum, err)
	}
	vetModel := composeModel{Services: map[string]composeService{
		"postgres": {Image: "postgres:18"}, "postgres-test": {Image: "postgres:18"}, "redis": {Image: "redis:7"},
	}}
	vet, err := selectProviders(vetModel, []projectplan.Service{{Name: "dashboard"}})
	if err != nil || !reflect.DeepEqual(providerNames(vet), []string{"postgres", "redis"}) {
		t.Fatalf("unexpected Vet Studio dependencies: %#v, %v", vet, err)
	}
}

func TestStartUsesEphemeralOverrideAndRewritesOnlyLocalConnections(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	mustWriteDependencyFile(t, filepath.Join(root, "docker-compose.yml"), "services:\n  postgres:\n    image: postgres:18\n  redis:\n    image: redis:7\n")
	mustWriteDependencyFile(t, filepath.Join(root, ".env"), strings.Join([]string{
		"DATABASE_URL=postgresql://user:secret@localhost:5432/app?sslmode=disable",
		"REDIS_URL=redis://localhost:6379/2",
	}, "\n"))
	model := composeModel{Services: map[string]composeService{
		"server":   {DependsOn: dependencies("postgres", "redis")},
		"postgres": {Image: "postgres:18"},
		"redis":    {Image: "redis:7"},
	}}
	data, _ := json.Marshal(model)
	runner := &fakeRunner{config: data, ports: map[string]string{"postgres": "127.0.0.1:49101\n", "redis": "127.0.0.1:49102\n"}}

	session, err := start(context.Background(), Request{
		Root: root, Project: "demo", Services: []projectplan.Service{{Name: "server", Root: "."}}, StateDir: state,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	overrides := session.Overrides("server")
	for key, want := range map[string]string{
		"PGHOST": "127.0.0.1", "PGPORT": "49101", "REDIS_HOST": "127.0.0.1", "REDIS_PORT": "49102",
		"DATABASE_URL": "postgresql://user:secret@127.0.0.1:49101/app?sslmode=disable",
		"REDIS_URL":    "redis://127.0.0.1:49102/2",
	} {
		if overrides[key] != want {
			t.Fatalf("%s: want %q, got %q", key, want, overrides[key])
		}
	}
	for _, remote := range []string{"postgresql://user:secret@db.example.com:5432/app", "postgresql://user:secret@prod-db:5432/app"} {
		if _, ok := rewriteLocalURL(remote, 49101, "postgres", "postgresql"); ok {
			t.Fatalf("remote database URL was treated as local: %s", remote)
		}
	}
	info, err := os.Stat(session.command.override)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("override must exist outside repo with mode 0600: %v, %v", info, err)
	}
	contents, _ := os.ReadFile(session.command.override)
	if strings.Contains(string(contents), "published:") || !strings.Contains(string(contents), "ports: !override") {
		t.Fatalf("override must ask Docker for ephemeral ports:\n%s", contents)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(session.command.override); !os.IsNotExist(err) {
		t.Fatalf("override not removed: %v", err)
	}
	if !runner.calledTail("up", "-d", "--wait", "postgres", "redis") || !runner.calledTail("stop", "postgres", "redis") {
		t.Fatalf("unexpected Docker transcript: %#v", runner.calls)
	}
}

func TestProviderSafetyRejectsHostStateAndUnsafeDependencies(t *testing.T) {
	model := composeModel{Services: map[string]composeService{
		"server":   {DependsOn: dependencies("postgres")},
		"postgres": {Image: "postgres:18", DependsOn: dependencies("setup")},
		"setup":    {Image: "busybox"},
	}}
	if _, err := selectProviders(model, []projectplan.Service{{Name: "server"}}); err == nil || !strings.Contains(err.Error(), "unsafe service") {
		t.Fatalf("expected safe-closure error, got %v", err)
	}
}

func TestSelectDependenciesIncludesMatchingWorkerClosure(t *testing.T) {
	model := composeModel{Services: map[string]composeService{
		"momentum_server_ts": {DependsOn: dependencies("postgres", "redis")},
		"momentum_worker_ts": {DependsOn: dependencies("momentum_fetcher", "postgres", "redis")},
		"momentum_fetcher":   {DependsOn: dependencies("postgres")},
		"postgres":           {Image: "pgvector/pgvector:pg17"},
		"redis":              {Image: "redis:7-alpine"},
		"unrelated_worker":   {DependsOn: dependencies("postgres")},
	}}

	providers, backgrounds, err := selectDependencies(model, []projectplan.Service{{Name: "server-ts"}})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := providerNames(providers), []string{"postgres", "redis"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers: want %#v, got %#v", want, got)
	}
	if want := []string{"momentum_fetcher", "momentum_worker_ts"}; !reflect.DeepEqual(backgrounds, want) {
		t.Fatalf("backgrounds: want %#v, got %#v", want, backgrounds)
	}
}

func TestBackgroundSafetyRejectsPublishedPortsAndGlobalResources(t *testing.T) {
	for name, unsafe := range map[string]composeService{
		"published ports": {Ports: []json.RawMessage{json.RawMessage(`{"target":9000,"published":"9000"}`)}},
		"host network":    {NetworkMode: "host"},
		"container name":  {ContainerName: "global-worker"},
		"external volume": {Volumes: []composeVolume{{Type: "volume", Source: "shared"}}},
		"external network": {Networks: map[string]json.RawMessage{
			"shared": json.RawMessage(`{}`),
		}},
	} {
		t.Run(name, func(t *testing.T) {
			model := composeModel{
				Services: map[string]composeService{
					"demo_server": {}, "demo_worker": unsafe, "postgres": {Image: "postgres:18"},
				},
				Volumes:  map[string]composeNamedVolume{"shared": {External: true}},
				Networks: map[string]composeNamedNetwork{"shared": {External: true}},
			}
			if _, _, err := selectDependencies(model, []projectplan.Service{{Name: "server"}}); err == nil {
				t.Fatal("expected unsafe background service to be rejected")
			}
		})
	}
}

func TestBackgroundSelectionRejectsMissingDependency(t *testing.T) {
	model := composeModel{Services: map[string]composeService{
		"demo_server": {}, "demo_worker": {DependsOn: dependencies("missing")},
	}}
	if _, _, err := selectDependencies(model, []projectplan.Service{{Name: "server"}}); err == nil || !strings.Contains(err.Error(), "not defined") {
		t.Fatalf("expected missing dependency error, got %v", err)
	}
}

func TestStartReusesRunningClosureAndStopsOnlyWhatItStarted(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	mustWriteDependencyFile(t, filepath.Join(root, "compose.yml"), "services:\n  postgres:\n    image: postgres:18\n  redis:\n    image: redis:7\n")
	model := composeModel{Services: map[string]composeService{
		"momentum_server_ts": {DependsOn: dependencies("postgres", "redis")},
		"momentum_worker_ts": {DependsOn: dependencies("momentum_fetcher", "postgres", "redis")},
		"momentum_fetcher":   {DependsOn: dependencies("postgres")},
		"postgres":           {Image: "postgres:18"},
		"redis":              {Image: "redis:7"},
	}}
	data, _ := json.Marshal(model)
	runner := &fakeRunner{
		config:  data,
		ports:   map[string]string{"postgres": "127.0.0.1:49101\n", "redis": "127.0.0.1:49102\n"},
		running: "momentum_fetcher\npostgres\n",
	}

	session, err := start(context.Background(), Request{
		Root: root, Project: "momentum", Services: []projectplan.Service{{Name: "server-ts", Root: "."}}, StateDir: state,
	}, runner)
	if err != nil {
		t.Fatal(err)
	}
	if !runner.calledTail("up", "-d", "--wait", "momentum_worker_ts", "redis") {
		t.Fatalf("only missing services should start: %#v", runner.calls)
	}
	if err := session.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !runner.calledTail("stop", "momentum_worker_ts", "redis") {
		t.Fatalf("only LNS-started services should stop: %#v", runner.calls)
	}
	for _, call := range runner.calls {
		if containsSequence(call, "up", "-d", "--wait", "momentum_server_ts") || containsSequence(call, "stop", "momentum_fetcher") || containsSequence(call, "stop", "postgres") {
			t.Fatalf("selected app or reused service was managed by Compose: %#v", runner.calls)
		}
	}
}

func TestProviderOverridesRespectRemoteSplitConfiguration(t *testing.T) {
	result := map[string]map[string]string{}
	services := []projectplan.Service{{Name: "api"}}
	values := map[string]map[string]string{"api": {"PGHOST": "staging-db.example.com", "PGPORT": "5432"}}
	applyProviderOverrides(result, t.TempDir(), services, values, provider{Name: "postgres", Kind: "postgres", Port: 49101})
	if _, ok := result["api"]["PGHOST"]; ok {
		t.Fatalf("remote PGHOST was overridden: %#v", result["api"])
	}
	if _, ok := result["api"]["PGPORT"]; ok {
		t.Fatalf("remote PGPORT was overridden: %#v", result["api"])
	}
}

func TestLocalPostgresUsesVerifiedExampleRoleDefaults(t *testing.T) {
	root := t.TempDir()
	mustWriteDependencyFile(t, filepath.Join(root, ".env.example"), strings.Join([]string{
		"PGDATABASE=vet_studio",
		"PGUSER_ADMIN=vet",
		"PGPASSWORD_ADMIN=vet",
		"# PGUSER_RUNTIME=dashboard_runtime",
		"# PGPASSWORD_RUNTIME=vet",
	}, "\n"))
	services := []projectplan.Service{{Name: "dashboard", Root: "."}}
	dotenv := map[string]map[string]string{"dashboard": {}}
	postgres := provider{
		Name: "postgres", Kind: "postgres", Port: 49101,
		Environment: map[string]string{"POSTGRES_USER": "vet", "POSTGRES_PASSWORD": "vet", "POSTGRES_DB": "vet_studio"},
	}
	result := map[string]map[string]string{}
	applyProviderOverrides(result, root, services, dotenv, postgres)
	for key, want := range map[string]string{
		"PGHOST": "127.0.0.1", "PGPORT": "49101", "PGDATABASE": "vet_studio",
		"PGUSER_RUNTIME": "dashboard_runtime", "PGPASSWORD_RUNTIME": "vet",
	} {
		if result["dashboard"][key] != want {
			t.Fatalf("%s: want %q, got %#v", key, want, result["dashboard"])
		}
	}

	custom := map[string]map[string]string{"dashboard": {"PGUSER_RUNTIME": "custom", "PGPASSWORD_RUNTIME": "secret"}}
	result = map[string]map[string]string{}
	applyProviderOverrides(result, root, services, custom, postgres)
	if result["dashboard"]["PGUSER_RUNTIME"] != "" || result["dashboard"]["PGPASSWORD_RUNTIME"] != "" {
		t.Fatalf("explicit role credentials were overridden: %#v", result["dashboard"])
	}
}

func TestLocalRoleDefaultsRequireMatchingProviderAndNonProduction(t *testing.T) {
	root := t.TempDir()
	mustWriteDependencyFile(t, filepath.Join(root, ".env.example"), "PGDATABASE=vet_studio\nPGUSER_ADMIN=vet\nPGPASSWORD_ADMIN=vet\n# PGUSER_RUNTIME=dashboard_runtime\n# PGPASSWORD_RUNTIME=vet\n")
	services := []projectplan.Service{{Name: "dashboard", Root: "."}}
	matching := map[string]string{"POSTGRES_USER": "vet", "POSTGRES_PASSWORD": "vet", "POSTGRES_DB": "vet_studio"}
	for name, testCase := range map[string]struct {
		dotenv      map[string]map[string]string
		environment map[string]string
	}{
		"production": {map[string]map[string]string{"dashboard": {"NODE_ENV": "production"}}, matching},
		"remote":     {map[string]map[string]string{"dashboard": {"PGHOST": "db.example.com"}}, matching},
		"mismatch":   {map[string]map[string]string{"dashboard": {}}, map[string]string{"POSTGRES_USER": "wrong", "POSTGRES_PASSWORD": "wrong", "POSTGRES_DB": "vet_studio"}},
	} {
		t.Run(name, func(t *testing.T) {
			result := map[string]map[string]string{}
			applyProviderOverrides(result, root, services, testCase.dotenv, provider{
				Name: "postgres", Kind: "postgres", Port: 49101,
				Environment: testCase.environment,
			})
			if result["dashboard"]["PGUSER_RUNTIME"] != "" {
				t.Fatalf("unsafe role defaults were injected: %#v", result["dashboard"])
			}
		})
	}
}

func TestStartRollsBackPartiallyFailedComposeUp(t *testing.T) {
	root := t.TempDir()
	state := t.TempDir()
	mustWriteDependencyFile(t, filepath.Join(root, "compose.yml"), "services:\n  postgres:\n    image: postgres:18\n")
	model := composeModel{Services: map[string]composeService{
		"server": {DependsOn: dependencies("postgres")}, "postgres": {Image: "postgres:18"},
	}}
	data, _ := json.Marshal(model)
	runner := &fakeRunner{config: data, ports: map[string]string{}, failOn: "up"}
	_, err := start(context.Background(), Request{Root: root, Project: "demo", Services: []projectplan.Service{{Name: "server"}}, StateDir: state}, runner)
	if err == nil || !strings.Contains(err.Error(), "start Docker dependencies") {
		t.Fatalf("expected startup failure, got %v", err)
	}
	if !runner.calledTail("stop", "postgres") {
		t.Fatalf("partial startup was not rolled back: %#v", runner.calls)
	}
	entries, readErr := os.ReadDir(state)
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("failed startup leaked state: %v, %#v", readErr, entries)
	}
}

func dependencies(names ...string) map[string]json.RawMessage {
	result := map[string]json.RawMessage{}
	for _, name := range names {
		result[name] = json.RawMessage(`{}`)
	}
	return result
}

type fakeRunner struct {
	config  []byte
	ports   map[string]string
	running string
	calls   [][]string
	failOn  string
}

func (runner *fakeRunner) Run(_ context.Context, _ string, _ []string, name string, args ...string) ([]byte, error) {
	call := append([]string{name}, args...)
	runner.calls = append(runner.calls, call)
	if runner.failOn != "" && indexOf(args, runner.failOn) >= 0 {
		return nil, errors.New("simulated failure")
	}
	if containsSequence(args, "config", "--no-env-resolution", "--format", "json") {
		return runner.config, nil
	}
	if containsSequence(args, "ps", "--status", "running", "--services") {
		return []byte(runner.running), nil
	}
	if index := indexOf(args, "port"); index >= 0 && index+1 < len(args) {
		return []byte(runner.ports[args[index+1]]), nil
	}
	return nil, nil
}

func (runner *fakeRunner) calledTail(values ...string) bool {
	for _, call := range runner.calls {
		if containsSequence(call, values...) {
			return true
		}
	}
	return false
}

func containsSequence(values []string, sequence ...string) bool {
	for index := 0; index+len(sequence) <= len(values); index++ {
		if reflect.DeepEqual(values[index:index+len(sequence)], sequence) {
			return true
		}
	}
	return false
}

func indexOf(values []string, target string) int {
	for index, value := range values {
		if value == target {
			return index
		}
	}
	return -1
}

func mustWriteDependencyFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
		t.Fatal(err)
	}
}
