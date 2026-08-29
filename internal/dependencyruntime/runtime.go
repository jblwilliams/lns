// Package dependencyruntime owns ephemeral host access to local Compose
// dependencies. LNS itself never edits the inspected repository or starts
// selected foreground application services from Compose. Private workers that
// belong to a selected service remain under Compose so their internal service
// links work.
package dependencyruntime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"lns/internal/config"
	"lns/internal/devrun"
	"lns/internal/discovery"
	"lns/internal/projectplan"
	"lns/internal/state"
)

type Request struct {
	Root     string
	Project  string
	Worktree string
	Services []projectplan.Service
	StateDir string
}

type Session struct {
	overrides map[string]map[string]string
	runner    commandRunner
	command   composeCommand
	ownerPath string
	started   []string
}

type composeCommand struct {
	root, source, override, project string
	environment                     []string
}

type commandRunner interface {
	Run(context.Context, string, []string, string, ...string) ([]byte, error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, root string, environment []string, name string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, name, args...)
	command.Dir = root
	command.Env = environment
	output, err := command.CombinedOutput()
	if err != nil {
		message := strings.TrimSpace(string(output))
		if message == "" {
			message = err.Error()
		}
		return nil, errors.New(message)
	}
	return output, nil
}

func Start(ctx context.Context, request Request) (*Session, error) {
	return start(ctx, request, execRunner{})
}

func start(ctx context.Context, request Request, runner commandRunner) (*Session, error) {
	root, err := filepath.Abs(request.Root)
	if err != nil {
		return nil, fmt.Errorf("resolve dependency root: %w", err)
	}
	source := composePath(root)
	if source == "" || !composeMayHaveProviders(source) {
		return &Session{overrides: map[string]map[string]string{}}, nil
	}
	stateDir := request.StateDir
	if stateDir == "" {
		stateDir = filepath.Join(config.GetConfigDir(), "dependencies")
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return nil, fmt.Errorf("create dependency state: %w", err)
	}
	stateID := runtimeStateID(request.Project, root, request.Worktree)
	session := &Session{overrides: map[string]map[string]string{}, runner: runner}
	failed := true
	defer func() {
		if failed && session.ownerPath != "" && len(session.started) == 0 {
			_ = os.Remove(session.ownerPath)
		}
	}()

	dotenv := dotenvByService(root, request.Services)
	environment := composeEnvironment(dotenv)
	base := composeCommand{root: root, source: source, environment: environment}
	inspect := base
	data, err := runner.Run(ctx, root, environment, "docker", composeArgs(inspect, "config", "--no-env-resolution", "--format", "json")...)
	if err != nil {
		return nil, fmt.Errorf("inspect Docker dependencies: %w", err)
	}
	var model composeModel
	if err := json.Unmarshal(data, &model); err != nil {
		return nil, fmt.Errorf("decode Docker Compose plan: %w", err)
	}
	providers, backgrounds, err := selectDependencies(model, request.Services)
	if err != nil {
		return nil, err
	}
	if len(providers) == 0 && len(backgrounds) == 0 {
		failed = false
		return session, nil
	}
	ownerPath := filepath.Join(stateDir, composeOwner(model.Name, root)+".owner.json")
	stale, err := claimOwner(ownerPath)
	if err != nil {
		return nil, err
	}
	session.ownerPath = ownerPath
	if len(stale.Started) > 0 {
		session.command = base
		session.started = stale.Started
		if err := writeOwner(ownerPath, session.started, stale.Override); err != nil {
			return nil, fmt.Errorf("record stale Docker dependency ownership: %w", err)
		}
		if _, err := runner.Run(ctx, root, environment, "docker", composeArgs(base, append([]string{"stop"}, stale.Started...)...)...); err != nil {
			failed = false // preserve the record so the next run can retry cleanup
			return nil, fmt.Errorf("stop stale LNS Docker dependencies: %w", err)
		}
		if err := writeOwner(ownerPath, nil, ""); err != nil {
			return nil, fmt.Errorf("clear stale Docker dependency ownership: %w", err)
		}
		session.started = nil
		if filepath.Dir(filepath.Clean(stale.Override)) == filepath.Clean(stateDir) {
			_ = os.Remove(stale.Override)
		}
	}
	runningOutput, err := runner.Run(ctx, root, environment, "docker", composeArgs(inspect, "ps", "--status", "running", "--services")...)
	if err != nil {
		return nil, fmt.Errorf("inspect running Docker dependencies: %w", err)
	}
	running := lineSet(string(runningOutput))
	managedProviders := make([]provider, 0, len(providers))
	managedNames := map[string]bool{}
	for _, provider := range providers {
		if !running[provider.Name] {
			managedProviders = append(managedProviders, provider)
			managedNames[provider.Name] = true
			continue
		}
		output, err := runner.Run(ctx, root, environment, "docker", composeArgs(inspect, "port", provider.Name, strconv.Itoa(provider.Target))...)
		if err != nil {
			return nil, fmt.Errorf("Docker dependency %q is already running without a usable host port; stop the normal Compose stack before starting LNS", provider.Name)
		}
		provider.Port, err = publishedPort(string(output))
		if err != nil {
			return nil, fmt.Errorf("read existing %s host port: %w", provider.Name, err)
		}
		applyProviderOverrides(session.overrides, root, request.Services, dotenv, provider)
	}
	for _, name := range backgrounds {
		if !running[name] {
			managedNames[name] = true
		}
	}
	if len(managedNames) == 0 {
		failed = false
		return session, nil
	}
	if len(managedProviders) > 0 {
		overridePath := filepath.Join(stateDir, stateID+".override.yml")
		if err := os.WriteFile(overridePath, []byte(overrideYAML(managedProviders)), 0600); err != nil {
			return nil, fmt.Errorf("write dependency override: %w", err)
		}
		base.override = overridePath
	}
	session.command = base
	if _, err := runner.Run(ctx, root, environment, "docker", composeArgs(base, "config", "-q")...); err != nil {
		_ = os.Remove(base.override)
		return nil, fmt.Errorf("validate dynamic dependency ports: %w", err)
	}
	names := sortedKeys(managedNames)
	session.started = names // rollback even if Compose fails partway through up
	if err := writeOwner(session.ownerPath, session.started, base.override); err != nil {
		return nil, fmt.Errorf("record Docker dependency ownership: %w", err)
	}
	if _, err := runner.Run(ctx, root, environment, "docker", composeArgs(base, append([]string{"up", "-d", "--wait"}, names...)...)...); err != nil {
		closeAfterFailure(session)
		return nil, fmt.Errorf("start Docker dependencies: %w", err)
	}
	for _, provider := range managedProviders {
		output, err := runner.Run(ctx, root, environment, "docker", composeArgs(base, "port", provider.Name, strconv.Itoa(provider.Target))...)
		if err != nil {
			closeAfterFailure(session)
			return nil, fmt.Errorf("read %s host port: %w", provider.Name, err)
		}
		provider.Port, err = publishedPort(string(output))
		if err != nil {
			closeAfterFailure(session)
			return nil, fmt.Errorf("read %s host port: %w", provider.Name, err)
		}
		applyProviderOverrides(session.overrides, root, request.Services, dotenv, provider)
	}
	failed = false
	return session, nil
}

func closeAfterFailure(session *Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_ = session.Close(ctx)
}

func (session *Session) Overrides(service string) map[string]string {
	result := map[string]string{}
	for key, value := range session.overrides[service] {
		result[key] = value
	}
	return result
}

func (session *Session) Close(ctx context.Context) error {
	if session == nil {
		return nil
	}
	var closeErr error
	if len(session.started) > 0 {
		_, closeErr = session.runner.Run(ctx, session.command.root, session.command.environment, "docker", composeArgs(session.command, append([]string{"stop"}, session.started...)...)...)
		if closeErr != nil {
			return fmt.Errorf("stop Docker dependencies: %w", closeErr)
		}
		if session.ownerPath != "" {
			if err := writeOwner(session.ownerPath, nil, ""); err != nil {
				return fmt.Errorf("clear Docker dependency ownership: %w", err)
			}
		}
		session.started = nil
	}
	if session.command.override != "" {
		_ = os.Remove(session.command.override)
	}
	if session.ownerPath != "" {
		_ = os.Remove(session.ownerPath)
	}
	return nil
}

func composeArgs(command composeCommand, tail ...string) []string {
	args := []string{"compose", "--profile", "*", "-f", command.source}
	if command.override != "" {
		args = append(args, "-f", command.override)
	}
	args = append(args, "--project-directory", command.root)
	if command.project != "" {
		args = append(args, "-p", command.project)
	}
	return append(args, tail...)
}

func composePath(root string) string {
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		path := filepath.Join(root, name)
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return ""
}

func composeMayHaveProviders(path string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	text := strings.ToLower(string(data))
	for _, signal := range []string{"postgres", "pgvector", "redis", "valkey"} {
		if strings.Contains(text, signal) {
			return true
		}
	}
	return false
}

func runtimeStateID(project, root, worktree string) string {
	hash := sha256.Sum256([]byte(root + "\x00" + worktree))
	name := "lns-" + discovery.NormalizeName(project) + "-" + hex.EncodeToString(hash[:4])
	return strings.Trim(name, "-")
}

type ownerRecord struct {
	PID      int      `json:"pid"`
	Started  []string `json:"started,omitempty"`
	Override string   `json:"override,omitempty"`
}

func claimOwner(path string) (ownerRecord, error) {
	var claimed ownerRecord
	err := withOwnerClaimLock(path, func() error {
		var err error
		claimed, err = claimOwnerLocked(path)
		return err
	})
	return claimed, err
}

func claimOwnerLocked(path string) (ownerRecord, error) {
	data, _ := json.Marshal(ownerRecord{PID: os.Getpid()})
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		_, writeErr := file.Write(data)
		closeErr := file.Close()
		if writeErr != nil {
			return ownerRecord{}, writeErr
		}
		return ownerRecord{}, closeErr
	}
	if !os.IsExist(err) {
		return ownerRecord{}, err
	}
	existing, _ := os.ReadFile(path)
	var stale ownerRecord
	if json.Unmarshal(existing, &stale) == nil && processAlive(stale.PID) {
		return ownerRecord{}, fmt.Errorf("Docker dependencies are already owned by LNS process %d; stop that run before starting this Compose project", stale.PID)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return ownerRecord{}, err
	}
	file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return ownerRecord{}, err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return ownerRecord{}, writeErr
	}
	return stale, closeErr
}

func writeOwner(path string, started []string, override string) error {
	data, err := json.Marshal(ownerRecord{PID: os.Getpid(), Started: started, Override: override})
	if err != nil {
		return err
	}
	return state.WriteFileAtomic(path, data, 0600)
}

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	process, err := os.FindProcess(pid)
	return err == nil && process.Signal(syscall.Signal(0)) == nil
}

type composeModel struct {
	Name     string                         `json:"name"`
	Services map[string]composeService      `json:"services"`
	Volumes  map[string]composeNamedVolume  `json:"volumes"`
	Networks map[string]composeNamedNetwork `json:"networks"`
}

func composeOwner(name, root string) string {
	if name = strings.TrimSpace(name); name != "" {
		hash := sha256.Sum256([]byte(name))
		label := discovery.NormalizeName(name)
		if label == "" {
			label = "project"
		}
		return "compose-" + label + "-" + hex.EncodeToString(hash[:8])
	}
	hash := sha256.Sum256([]byte(root))
	return "compose-" + hex.EncodeToString(hash[:8])
}

type composeService struct {
	Image         string                     `json:"image"`
	ContainerName string                     `json:"container_name"`
	NetworkMode   string                     `json:"network_mode"`
	DependsOn     map[string]json.RawMessage `json:"depends_on"`
	Volumes       []composeVolume            `json:"volumes"`
	Networks      map[string]json.RawMessage `json:"networks"`
	Ports         []json.RawMessage          `json:"ports"`
	Environment   map[string]string          `json:"environment"`
}

type composeVolume struct {
	Type     string `json:"type"`
	Source   string `json:"source"`
	ReadOnly bool   `json:"read_only"`
}

type composeNamedVolume struct {
	Name     string `json:"name"`
	External bool   `json:"external"`
}

type composeNamedNetwork struct {
	External bool `json:"external"`
}

type provider struct {
	Name, Kind  string
	Target      int
	Port        int
	Environment map[string]string
}

func selectProviders(model composeModel, services []projectplan.Service) ([]provider, error) {
	providers, _, err := selectDependencies(model, services)
	return providers, err
}

func selectDependencies(model composeModel, services []projectplan.Service) ([]provider, []string, error) {
	matchedApps := map[string]bool{}
	selected := map[string]bool{}
	for _, service := range services {
		for name := range model.Services {
			if composeMatchesService(name, service.Name) {
				matchedApps[name] = true
				collectDependencies(model, name, selected)
			}
		}
	}
	backgroundClosure := map[string]bool{}
	serviceNames := make([]string, 0, len(model.Services))
	for name := range model.Services {
		serviceNames = append(serviceNames, name)
	}
	sort.Strings(serviceNames)
	for _, name := range serviceNames {
		if matchesBackground(name, matchedApps) {
			closure := map[string]bool{}
			collectServiceAndDependencies(model, name, closure)
			for _, app := range sortedKeys(matchedApps) {
				if closure[app] {
					return nil, nil, fmt.Errorf("Compose background %q depends on selected foreground service %q; run that worker outside Compose or remove the dependency", name, app)
				}
			}
			for dependency := range closure {
				backgroundClosure[dependency] = true
			}
		}
	}
	var providers []provider
	for name, service := range model.Services {
		kind, target := providerKind(name, service.Image)
		needed := selected[name] || backgroundClosure[name]
		if kind == "" || ((len(selected) > 0 || len(backgroundClosure) > 0) && !needed) {
			continue
		}
		if len(selected) == 0 && len(backgroundClosure) == 0 && discovery.NormalizeName(name) != kind {
			continue
		}
		if err := validateProvider(name, service, model); err != nil {
			return nil, nil, err
		}
		providers = append(providers, provider{Name: name, Kind: kind, Target: target, Environment: service.Environment})
	}
	var backgrounds []string
	for name := range backgroundClosure {
		service, ok := model.Services[name]
		if !ok {
			return nil, nil, fmt.Errorf("Compose background dependency %q is not defined", name)
		}
		if kind, _ := providerKind(name, service.Image); kind != "" {
			continue
		}
		if err := validateBackground(name, service, model); err != nil {
			return nil, nil, err
		}
		backgrounds = append(backgrounds, name)
	}
	sort.Slice(providers, func(i, j int) bool { return providers[i].Name < providers[j].Name })
	sort.Strings(backgrounds)
	return providers, backgrounds, nil
}

func collectDependencies(model composeModel, name string, selected map[string]bool) {
	service, ok := model.Services[name]
	if !ok {
		return
	}
	for dependency := range service.DependsOn {
		if selected[dependency] {
			continue
		}
		selected[dependency] = true
		collectDependencies(model, dependency, selected)
	}
}

func collectServiceAndDependencies(model composeModel, name string, selected map[string]bool) {
	if selected[name] {
		return
	}
	if _, ok := model.Services[name]; !ok {
		return
	}
	selected[name] = true
	collectDependencies(model, name, selected)
}

func matchesBackground(name string, selected map[string]bool) bool {
	tokens := strings.Split(discovery.NormalizeName(name), "-")
	for index, token := range tokens {
		if token != "worker" {
			continue
		}
		for _, replacement := range []string{"server", "api", "backend"} {
			candidate := append([]string(nil), tokens...)
			candidate[index] = replacement
			for selectedName := range selected {
				if strings.Join(candidate, "-") == discovery.NormalizeName(selectedName) {
					return true
				}
			}
		}
	}
	return false
}

func composeMatchesService(composeName, serviceName string) bool {
	composeName = discovery.NormalizeName(composeName)
	serviceName = discovery.NormalizeName(serviceName)
	return composeName == serviceName || strings.HasSuffix(composeName, "-"+serviceName)
}

func providerKind(name, image string) (string, int) {
	value := strings.ToLower(name + " " + image)
	switch {
	case strings.Contains(value, "postgres"), strings.Contains(value, "pgvector"):
		return "postgres", 5432
	case strings.Contains(value, "redis"), strings.Contains(value, "valkey"):
		return "redis", 6379
	default:
		return "", 0
	}
}

func validateProvider(name string, service composeService, model composeModel) error {
	if service.ContainerName != "" || service.NetworkMode == "host" {
		return fmt.Errorf("Compose dependency %q uses global container or network state", name)
	}
	for _, volume := range service.Volumes {
		if volume.Type == "bind" && !volume.ReadOnly {
			return fmt.Errorf("Compose dependency %q has a writable host bind mount", name)
		}
		if volume.Type == "volume" && model.Volumes[volume.Source].External {
			return fmt.Errorf("Compose dependency %q uses external volume %q", name, volume.Source)
		}
	}
	for network := range service.Networks {
		if model.Networks[network].External {
			return fmt.Errorf("Compose dependency %q uses external network %q", name, network)
		}
	}
	for dependency := range service.DependsOn {
		kind, _ := providerKind(dependency, model.Services[dependency].Image)
		if kind == "" {
			return fmt.Errorf("Compose dependency %q depends on unsafe service %q", name, dependency)
		}
	}
	return nil
}

func validateBackground(name string, service composeService, model composeModel) error {
	if service.ContainerName != "" || service.NetworkMode == "host" {
		return fmt.Errorf("Compose background service %q uses global container or network state", name)
	}
	if len(service.Ports) > 0 {
		return fmt.Errorf("Compose background service %q publishes host ports", name)
	}
	for _, volume := range service.Volumes {
		if volume.Type == "volume" && model.Volumes[volume.Source].External {
			return fmt.Errorf("Compose background service %q uses external volume %q", name, volume.Source)
		}
	}
	for network := range service.Networks {
		if model.Networks[network].External {
			return fmt.Errorf("Compose background service %q uses external network %q", name, network)
		}
	}
	return nil
}

func overrideYAML(providers []provider) string {
	var result strings.Builder
	result.WriteString("services:\n")
	for _, provider := range providers {
		fmt.Fprintf(&result, "  %s:\n    ports: !override\n      - target: %d\n        host_ip: 127.0.0.1\n        protocol: tcp\n", provider.Name, provider.Target)
	}
	return result.String()
}

func lineSet(value string) map[string]bool {
	result := map[string]bool{}
	for _, line := range strings.Split(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result[line] = true
		}
	}
	return result
}

func providerNames(providers []provider) []string {
	result := make([]string, len(providers))
	for index, provider := range providers {
		result[index] = provider.Name
	}
	return result
}

func sortedKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func publishedPort(value string) (int, error) {
	value = strings.TrimSpace(value)
	_, port, err := net.SplitHostPort(value)
	if err != nil {
		index := strings.LastIndex(value, ":")
		if index < 0 {
			return 0, fmt.Errorf("unexpected address %q", value)
		}
		port = value[index+1:]
	}
	parsed, err := strconv.Atoi(port)
	if err != nil || parsed < 1 || parsed > 65535 {
		return 0, fmt.Errorf("unexpected address %q", value)
	}
	return parsed, nil
}

func dotenvByService(root string, services []projectplan.Service) map[string]map[string]string {
	rootValues := readDotenvLayers(root)
	result := map[string]map[string]string{}
	for _, service := range services {
		values := cloneValues(rootValues)
		for key, value := range readDotenvLayers(filepath.Join(root, service.Root)) {
			values[key] = value
		}
		for _, entry := range os.Environ() {
			key, value, ok := strings.Cut(entry, "=")
			if ok && relevantConnectionKey(key) {
				values[key] = value
			}
		}
		result[service.Name] = values
	}
	return result
}

func readDotenvLayers(root string) map[string]string {
	result := map[string]string{}
	for _, name := range []string{".env", ".env.development", ".env.local", ".env.development.local"} {
		for key, value := range readDotenv(filepath.Join(root, name)) {
			result[key] = value
		}
	}
	return result
}

func readDotenv(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || key == "" || strings.HasPrefix(key, "#") {
			continue
		}
		if !relevantConnectionKey(key) {
			continue
		}
		result[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return result
}

func cloneValues(values map[string]string) map[string]string {
	result := map[string]string{}
	for key, value := range values {
		result[key] = value
	}
	return result
}

func relevantConnectionKey(key string) bool {
	switch key {
	case "NODE_ENV", "POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB", "PGHOST", "PGPORT", "PGDATABASE", "REDIS_HOST", "REDIS_PORT":
		return true
	default:
		return strings.HasPrefix(key, "PGUSER_") || strings.HasPrefix(key, "PGPASSWORD_") || databaseURLKey(key) || redisURLKey(key)
	}
}

func composeEnvironment(values map[string]map[string]string) []string {
	overrides := map[string]string{}
	for _, serviceValues := range values {
		for _, key := range []string{"POSTGRES_USER", "POSTGRES_PASSWORD", "POSTGRES_DB"} {
			if value := serviceValues[key]; value != "" {
				overrides[key] = value
			}
		}
	}
	return devrun.OverlayEnvironment(os.Environ(), overrides)
}

func applyProviderOverrides(result map[string]map[string]string, root string, services []projectplan.Service, dotenv map[string]map[string]string, provider provider) {
	for _, service := range services {
		if result[service.Name] == nil {
			result[service.Name] = map[string]string{}
		}
		overrides := result[service.Name]
		values := dotenv[service.Name]
		switch provider.Kind {
		case "postgres":
			if consumerUsesLocalProvider(values, "PGHOST", provider, databaseURLKey, "postgres", "postgresql") {
				overrides["PGHOST"] = "127.0.0.1"
				overrides["PGPORT"] = strconv.Itoa(provider.Port)
				applyLocalRoleDefaults(overrides, values, localRoleDefaults(root, service.Root, provider))
			}
			for key, value := range values {
				if databaseURLKey(key) {
					if rewritten, ok := rewriteLocalURL(value, provider.Port, provider.Name, "postgres", "postgresql"); ok {
						overrides[key] = rewritten
					}
				}
			}
		case "redis":
			if consumerUsesLocalProvider(values, "REDIS_HOST", provider, redisURLKey, "redis") {
				overrides["REDIS_HOST"] = "127.0.0.1"
				overrides["REDIS_PORT"] = strconv.Itoa(provider.Port)
			}
			for key, value := range values {
				if redisURLKey(key) {
					if rewritten, ok := rewriteLocalURL(value, provider.Port, provider.Name, "redis"); ok {
						overrides[key] = rewritten
					}
				}
			}
		}
	}
}

func localRoleDefaults(root, serviceRoot string, provider provider) map[string]string {
	values := readExampleEnvironment(filepath.Join(root, ".env.example"))
	if serviceRoot != "" && serviceRoot != "." {
		for key, value := range readExampleEnvironment(filepath.Join(root, serviceRoot, ".env.example")) {
			values[key] = value
		}
	}
	if values["PGUSER_ADMIN"] == "" || values["PGPASSWORD_ADMIN"] == "" || values["PGDATABASE"] == "" ||
		values["PGUSER_ADMIN"] != provider.Environment["POSTGRES_USER"] ||
		values["PGPASSWORD_ADMIN"] != provider.Environment["POSTGRES_PASSWORD"] ||
		values["PGDATABASE"] != provider.Environment["POSTGRES_DB"] {
		return nil
	}
	defaults := map[string]string{"PGDATABASE": values["PGDATABASE"]}
	for key, user := range values {
		if !strings.HasPrefix(key, "PGUSER_") || strings.Contains(key, "TEST") {
			continue
		}
		suffix := strings.TrimPrefix(key, "PGUSER_")
		passwordKey := "PGPASSWORD_" + suffix
		if user != "" && values[passwordKey] != "" {
			defaults[key] = user
			defaults[passwordKey] = values[passwordKey]
		}
	}
	return defaults
}

func readExampleEnvironment(path string) map[string]string {
	data, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	result := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSpace(strings.TrimPrefix(line, "#"))
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if !ok || !relevantConnectionKey(key) || !literalExampleValue(value) {
			continue
		}
		result[key] = value
	}
	return result
}

func literalExampleValue(value string) bool {
	if value == "" || strings.ContainsAny(value, "$<>{}") {
		return false
	}
	return !strings.ContainsAny(value, " \t\r\n")
}

func applyLocalRoleDefaults(overrides, values, defaults map[string]string) {
	if strings.EqualFold(values["NODE_ENV"], "production") {
		return
	}
	if values["PGDATABASE"] == "" && defaults["PGDATABASE"] != "" {
		overrides["PGDATABASE"] = defaults["PGDATABASE"]
	}
	for key, user := range defaults {
		if !strings.HasPrefix(key, "PGUSER_") {
			continue
		}
		passwordKey := "PGPASSWORD_" + strings.TrimPrefix(key, "PGUSER_")
		password := defaults[passwordKey]
		if password == "" || (values[key] != "" && values[key] != user) || (values[passwordKey] != "" && values[passwordKey] != password) {
			continue
		}
		if values[key] == "" {
			overrides[key] = user
		}
		if values[passwordKey] == "" {
			overrides[passwordKey] = password
		}
	}
}

func consumerUsesLocalProvider(values map[string]string, hostKey string, provider provider, urlKey func(string) bool, schemes ...string) bool {
	if host := strings.TrimSpace(values[hostKey]); host != "" && !localProviderHost(host, provider.Name) {
		return false
	}
	for key, value := range values {
		if !urlKey(key) || strings.TrimSpace(value) == "" {
			continue
		}
		if _, ok := rewriteLocalURL(value, provider.Port, provider.Name, schemes...); !ok {
			return false
		}
	}
	return true
}

func localProviderHost(host, provider string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") || discovery.NormalizeName(host) == discovery.NormalizeName(provider)
}

func databaseURLKey(key string) bool {
	return !strings.Contains(key, "TEST") && (key == "DATABASE_URL" || strings.HasSuffix(key, "_DATABASE_URL"))
}

func redisURLKey(key string) bool {
	return key == "REDIS_URL" || strings.HasSuffix(key, "_REDIS_URL")
}

func rewriteLocalURL(value string, port int, providerHost string, schemes ...string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" {
		return "", false
	}
	allowedScheme := false
	for _, scheme := range schemes {
		allowedScheme = allowedScheme || parsed.Scheme == scheme || strings.HasPrefix(parsed.Scheme, scheme+"+")
	}
	host := strings.ToLower(parsed.Hostname())
	local := localProviderHost(host, providerHost)
	if !allowedScheme || !local {
		return "", false
	}
	parsed.Host = net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return parsed.String(), true
}
