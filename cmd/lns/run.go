package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"lns/internal/caddy"
	"lns/internal/config"
	"lns/internal/dependencyruntime"
	"lns/internal/devrun"
	"lns/internal/devruntime"
	"lns/internal/models"
	"lns/internal/projectplan"
)

type serviceRun struct {
	Name    string
	Root    string
	Command []string
	URL     string
	Leases  []devruntime.Lease
	Env     []string
}

var runCmd = &cobra.Command{
	Use:   "run [service] [-- command...]",
	Short: "Run one service on a dynamic port through the local proxy",
	Example: `  lns run web
  lns run api -- pnpm run dev:api`,
	Args: cobra.ArbitraryArgs,
	RunE: runOneCommand,
}

func init() {
	rootCmd.AddCommand(runCmd)
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runConfiguredServices(nil, nil, 0)
	}
	runCmd.Flags().IntP("port", "p", 0, "Use a fixed development port for this run")
}

func runOneCommand(cmd *cobra.Command, args []string) error {
	dash := cmd.ArgsLenAtDash()
	before, override := args, []string(nil)
	if dash >= 0 {
		before, override = args[:dash], args[dash:]
	}
	if len(before) > 1 {
		return fmt.Errorf("expected at most one service name before --")
	}
	var names []string
	if len(before) == 1 {
		names = before
	}
	port, _ := cmd.Flags().GetInt("port")
	if port < 0 || port > 65535 {
		return fmt.Errorf("development port must be between 1 and 65535")
	}
	return runConfiguredServices(names, override, port)
}

func runConfiguredServices(names, override []string, requestedPort int) error {
	ctx, stopSignals := signal.NotifyContext(context.Background(), handledSignals()...)
	defer stopSignals()
	return runConfiguredServicesContext(ctx, names, override, requestedPort)
}

func runConfiguredServicesContext(ctx context.Context, names, override []string, requestedPort int) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	plan, err := buildRunPlan(root)
	if err != nil {
		return err
	}
	if len(override) > 0 {
		if len(names) == 0 {
			selected, err := defaultRunnableService(root, plan)
			if err != nil {
				return err
			}
			names = []string{selected}
		}
		service, err := serviceByName(plan, names[0])
		if err != nil {
			return err
		}
		service.Script = ""
		service.Command = append([]string(nil), override...)
		service.State = projectplan.StateManaged
		replaceService(&plan, service)
	}
	if len(names) == 0 {
		names = runnableServiceNames(plan)
	}
	if len(names) == 0 {
		return fmt.Errorf("no unambiguous default services; run `lns plan`, then choose one with `lns run <service>`")
	}
	requestedNames := strings.Join(names, ", ")
	selected, err := servicesByName(plan, names)
	if err != nil {
		return err
	}
	selected, err = expandServiceClosure(plan, selected)
	if err != nil {
		return err
	}
	names = serviceNames(selected)
	if requestedPort > 0 && len(names) != 1 {
		return fmt.Errorf("--port cannot be used because %q requires the service graph %s; omit --port to use dynamic ports", requestedNames, strings.Join(names, ", "))
	}
	for _, service := range selected {
		if service.State != projectplan.StateManaged {
			return fmt.Errorf("service %q is %s and cannot be started; run `lns plan` for recovery", service.Name, service.State)
		}
	}
	dependencies, err := dependencyruntime.Start(ctx, dependencyruntime.Request{
		Root: root, Project: plan.Project.Name, Worktree: plan.Project.Worktree, Services: selected,
	})
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := dependencies.Close(cleanupCtx); err != nil {
			printWarning("Could not stop Docker dependencies: %v", err)
		}
	}()
	if ctx.Err() != nil {
		return nil
	}

	store := devruntime.NewStore()
	activeLeases, err := store.Load()
	if err != nil {
		return err
	}
	runs := make([]serviceRun, 0, len(names))
	usedPorts := map[int]bool{}
	ports := make(map[string]int)
	for _, lease := range activeLeases {
		usedPorts[lease.Port] = true
	}
	for _, name := range names {
		service, err := serviceByName(plan, name)
		if err != nil {
			return err
		}
		for _, listener := range service.Listeners {
			port := 0
			if requestedPort > 0 && listener.Public {
				port = requestedPort
			}
			if port == 0 {
				port, err = allocatePort(usedPorts)
				if err != nil {
					return fmt.Errorf("find port for %s/%s: %w", name, listener.Name, err)
				}
			}
			if usedPorts[port] {
				return fmt.Errorf("port %d is already reserved", port)
			}
			usedPorts[port] = true
			ports[projectplan.EndpointKey(projectplan.EndpointRef{Service: name, Listener: listener.Name})] = port
		}
	}
	for _, name := range names {
		service, err := serviceByName(plan, name)
		if err != nil {
			return err
		}
		run, err := prepareServiceRun(root, plan, service, ports, os.Getpid())
		if err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
		run.Env = devrun.OverlayEnvironment(run.Env, dependencies.Overrides(name))
		runs = append(runs, run)
	}

	registered := make([]serviceRun, 0, len(runs))
	defer func() { cleanupRuns(store, registered) }()
	for _, run := range runs {
		for _, lease := range run.Leases {
			if ctx.Err() != nil {
				return nil
			}
			if err := store.Add(lease); err != nil {
				return err
			}
			registered = append(registered, serviceRun{Name: run.Name, Leases: []devruntime.Lease{lease}})
		}
	}
	if ctx.Err() != nil {
		return nil
	}
	if err := ensureProxyReady(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}

	for _, run := range runs {
		printSuccess("%s", run.URL)
		fmt.Printf("  %s\n", strings.Join(run.Command, " "))
	}
	return executeRuns(ctx, runs)
}

func prepareServiceRun(projectRoot string, plan projectplan.Plan, service projectplan.Service, ports map[string]int, pid int) (serviceRun, error) {
	public, ok := publicListener(service)
	if !ok {
		return serviceRun{}, fmt.Errorf("service %q has no public listener", service.Name)
	}
	port, ok := ports[projectplan.EndpointKey(projectplan.EndpointRef{Service: service.Name, Listener: public.Name})]
	if !ok {
		return serviceRun{}, fmt.Errorf("service %q has no allocated public port", service.Name)
	}
	runRoot := service.Root
	if service.RunRoot != "" {
		runRoot = service.RunRoot
	}
	model := models.Service{
		Name: service.Name, Root: runRoot, Script: service.Script,
		Command: append([]string(nil), service.Command...), Profile: public.Profile,
		Status: models.StatusResolved,
	}
	command, err := devrun.ResolveCommand(projectRoot, model, port)
	if err != nil {
		return serviceRun{}, err
	}
	overrides := map[string]string{"HOST": "127.0.0.1", "LNS_URL": public.URL, "LNS_PORT": fmt.Sprint(port)}
	for _, listener := range service.Listeners {
		listenerPort, exists := ports[projectplan.EndpointKey(projectplan.EndpointRef{Service: service.Name, Listener: listener.Name})]
		if !exists {
			return serviceRun{}, fmt.Errorf("service %q listener %q has no allocated port", service.Name, listener.Name)
		}
		for _, name := range listener.Environment {
			overrides[name] = fmt.Sprint(listenerPort)
		}
	}
	overrides[devrun.EnvironmentName(service.Name)+"_PORT"] = fmt.Sprint(port)
	for _, related := range plan.Services {
		relatedListener, exists := publicListener(related)
		if !exists {
			continue
		}
		key := devrun.EnvironmentName(related.Name)
		overrides["LNS_"+key+"_URL"] = relatedListener.URL
		overrides["VITE_LNS_"+key+"_URL"] = relatedListener.URL
		if relatedPort, exists := ports[projectplan.EndpointKey(projectplan.EndpointRef{Service: related.Name, Listener: relatedListener.Name})]; exists {
			overrides[key+"_PORT"] = fmt.Sprint(relatedPort)
		}
	}
	for _, binding := range service.Environment {
		switch binding.Kind {
		case projectplan.BindingPort:
			if targetPort, exists := ports[projectplan.EndpointKey(binding.Target)]; exists {
				overrides[binding.Name] = fmt.Sprint(targetPort)
			}
		case projectplan.BindingURL:
			target, err := serviceByName(plan, binding.Target.Service)
			if err != nil {
				return serviceRun{}, err
			}
			targetListener, ok := listenerByName(target, binding.Target.Listener)
			if !ok {
				return serviceRun{}, fmt.Errorf("listener %s/%s is not in the project plan", binding.Target.Service, binding.Target.Listener)
			}
			if binding.Network == projectplan.NetworkLoopback {
				targetPort, exists := ports[projectplan.EndpointKey(binding.Target)]
				if exists {
					scheme := binding.Scheme
					if scheme == "" {
						scheme = "http"
					}
					overrides[binding.Name] = fmt.Sprintf("%s://127.0.0.1:%d%s", scheme, targetPort, binding.Path)
				}
			} else {
				overrides[binding.Name] = strings.TrimSuffix(targetListener.URL, "/") + binding.Path
			}
		default:
			return serviceRun{}, fmt.Errorf("unsupported environment binding %q", binding.Kind)
		}
	}
	env := devrun.OverlayEnvironment(os.Environ(), overrides)
	return serviceRun{
		Name:    service.Name,
		Root:    filepath.Join(projectRoot, runRoot),
		Command: command,
		URL:     public.URL + "/",
		Leases: []devruntime.Lease{{
			Project:  plan.Project.Name,
			Service:  service.Name,
			Root:     filepath.Join(projectRoot, runRoot),
			Hostname: public.Hostname,
			Port:     port,
			PID:      pid,
			Worktree: plan.Project.Worktree,
			Profile:  public.Profile,
		}},
		Env: env,
	}, nil
}

func buildRunPlan(root string) (projectplan.Plan, error) {
	return projectplan.Build(root, projectplan.Route{Scheme: "http", Port: config.DefaultHTTPPort})
}

func runnableServiceNames(plan projectplan.Plan) []string {
	var names []string
	for _, service := range plan.Services {
		if service.State == projectplan.StateManaged && service.Default {
			names = append(names, service.Name)
		}
	}
	sort.Strings(names)
	return names
}

func defaultRunnableService(root string, plan projectplan.Plan) (string, error) {
	names := runnableServiceNames(plan)
	if len(names) == 1 {
		return names[0], nil
	}
	for _, name := range names {
		service, _ := serviceByName(plan, name)
		serviceRoot := filepath.Clean(filepath.Join(root, service.Root))
		if sameFilePath(serviceRoot, root) {
			return name, nil
		}
	}
	return "", fmt.Errorf("choose a service: %s", strings.Join(names, ", "))
}

func serviceByName(plan projectplan.Plan, name string) (projectplan.Service, error) {
	for _, service := range plan.Services {
		if service.Name == name {
			return service, nil
		}
	}
	return projectplan.Service{}, fmt.Errorf("service %q is not in the project plan", name)
}

func servicesByName(plan projectplan.Plan, names []string) ([]projectplan.Service, error) {
	result := make([]projectplan.Service, 0, len(names))
	for _, name := range names {
		service, err := serviceByName(plan, name)
		if err != nil {
			return nil, err
		}
		result = append(result, service)
	}
	return result, nil
}

func expandServiceClosure(plan projectplan.Plan, selected []projectplan.Service) ([]projectplan.Service, error) {
	included := make(map[string]bool, len(selected))
	queue := make([]projectplan.Service, 0, len(selected))
	for _, service := range selected {
		if !included[service.Name] {
			included[service.Name] = true
			queue = append(queue, service)
		}
	}
	for len(queue) > 0 {
		service := queue[0]
		queue = queue[1:]
		for _, binding := range service.Environment {
			if !binding.Required {
				continue
			}
			targetName := binding.Target.Service
			if targetName == "" || included[targetName] {
				continue
			}
			target, err := serviceByName(plan, targetName)
			if err != nil {
				return nil, fmt.Errorf("service %q requires %q: %w", service.Name, targetName, err)
			}
			if target.State != projectplan.StateManaged {
				return nil, fmt.Errorf("service %q requires %q, but it is %s; run `lns plan` for recovery", service.Name, targetName, target.State)
			}
			included[targetName] = true
			queue = append(queue, target)
		}
	}
	result := make([]projectplan.Service, 0, len(included))
	for _, service := range plan.Services {
		if included[service.Name] {
			result = append(result, service)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func serviceNames(services []projectplan.Service) []string {
	names := make([]string, len(services))
	for index, service := range services {
		names[index] = service.Name
	}
	return names
}

func publicListener(service projectplan.Service) (projectplan.Listener, bool) {
	for _, listener := range service.Listeners {
		if listener.Public {
			return listener, true
		}
	}
	return projectplan.Listener{}, false
}

func listenerByName(service projectplan.Service, name string) (projectplan.Listener, bool) {
	for _, listener := range service.Listeners {
		if listener.Name == name {
			return listener, true
		}
	}
	return projectplan.Listener{}, false
}

func allocatePort(used map[int]bool) (int, error) {
	for attempts := 0; attempts < 10; attempts++ {
		port, err := devrun.FindFreePort()
		if err != nil {
			return 0, err
		}
		if !used[port] {
			return port, nil
		}
	}
	return 0, fmt.Errorf("could not reserve a unique port")
}

func replaceService(plan *projectplan.Plan, replacement projectplan.Service) {
	for index := range plan.Services {
		if plan.Services[index].Name == replacement.Name {
			plan.Services[index] = replacement
			return
		}
	}
}

func ensureProxyReady(ctx context.Context) error {
	caddyPath, err := exec.LookPath("caddy")
	if err != nil {
		return fmt.Errorf("Caddy is not installed or not in PATH")
	}
	if _, err := caddy.RegenerateAllCaddyfiles(); err != nil {
		return fmt.Errorf("generate Caddy config: %w", err)
	}
	global := config.GetGlobalCaddyfilePath()
	if isTCPListening(config.CaddyAdminAddr) {
		command := exec.CommandContext(ctx, caddyPath, "reload", "--config", global, "--address", config.CaddyAdminAddr)
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("reload Caddy: %w", err)
		}
		return nil
	}
	return startCaddy(ctx, caddyPath, global, config.DefaultHTTPPort)
}

func executeRuns(ctx context.Context, runs []serviceRun) error {
	return executeRunsWithFactory(ctx, runs, exec.Command)
}

func executeRunsWithFactory(ctx context.Context, runs []serviceRun, newCommand func(string, ...string) *exec.Cmd) error {
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(runs))
	commands := make([]*exec.Cmd, 0, len(runs))
	for _, run := range runs {
		if ctx.Err() != nil {
			finishInterrupt := interruptCommands(commands)
			for range commands {
				<-results
			}
			finishInterrupt()
			return nil
		}
		command := newCommand(run.Command[0], run.Command[1:]...)
		command.Dir = run.Root
		command.Env = run.Env
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		configureChildProcess(command)
		if err := command.Start(); err != nil {
			finishInterrupt := interruptCommands(commands)
			for range commands {
				<-results
			}
			finishInterrupt()
			return fmt.Errorf("start %s: %w", run.Name, err)
		}
		commands = append(commands, command)
		go func(name string, child *exec.Cmd) { results <- result{name: name, err: child.Wait()} }(run.Name, command)
	}

	var first result
	select {
	case first = <-results:
		finishInterrupt := interruptCommands(commands)
		for i := 1; i < len(commands); i++ {
			<-results
		}
		finishInterrupt()
	case <-ctx.Done():
		finishInterrupt := interruptCommands(commands)
		for range commands {
			<-results
		}
		finishInterrupt()
		return nil
	}
	if first.err != nil && !interruptedExit(first.err) {
		return fmt.Errorf("%s exited: %w", first.name, first.err)
	}
	return nil
}

func interruptCommands(commands []*exec.Cmd) func() {
	for _, command := range commands {
		if command.Process == nil {
			continue
		}
		_ = interruptChildProcess(command)
	}
	timer := time.AfterFunc(2*time.Second, func() { killCommands(commands) })
	return func() {
		timer.Stop()
		// A command can exit before one of its descendants. Kill the detached
		// process group after every leader has been reaped so no watcher or
		// sidecar survives LNS cleanup.
		killCommands(commands)
	}
}

func killCommands(commands []*exec.Cmd) {
	for _, command := range commands {
		if command.Process != nil {
			_ = killChildProcess(command)
		}
	}
}

func cleanupRuns(store *devruntime.Store, runs []serviceRun) {
	for _, run := range runs {
		for _, lease := range run.Leases {
			if err := store.Remove(lease.Project, lease.Service, lease.Worktree, lease.PID); err != nil {
				printWarning("Could not remove runtime route for %s: %v", run.Name, err)
			}
		}
	}
	if _, err := caddy.RegenerateAllCaddyfiles(); err != nil {
		printWarning("Could not regenerate Caddy config during cleanup: %v", err)
		return
	}
	if isTCPListening(config.CaddyAdminAddr) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, "caddy", "reload", "--config", config.GetGlobalCaddyfilePath(), "--address", config.CaddyAdminAddr)
		if err := command.Run(); err != nil {
			printWarning("Could not reload Caddy during cleanup: %v", err)
		}
	}
}

func sameFilePath(a, b string) bool {
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return filepath.Clean(absA) == filepath.Clean(absB)
}
