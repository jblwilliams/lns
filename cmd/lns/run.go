package main

import (
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
	Lease   devruntime.Lease
	Env     []string
}

var runCmd = &cobra.Command{
	Use:   "run [service] [-- command...]",
	Short: "Run one service on a dynamic port through the local proxy",
	Args:  cobra.ArbitraryArgs,
	RunE:  runOneCommand,
}

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Run all configured development services",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runConfiguredServices(nil, nil, 0)
	},
}

func init() {
	rootCmd.AddCommand(runCmd, upCmd)
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
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	plan, err := buildRunPlan(root, settings)
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
		return fmt.Errorf("no managed services; run `lns plan` to inspect discovery and recovery steps")
	}
	if requestedPort > 0 && len(names) != 1 {
		return fmt.Errorf("--port can only be used when running one service")
	}

	store := devruntime.NewStore()
	activeLeases, err := store.Load()
	if err != nil {
		return err
	}
	runs := make([]serviceRun, 0, len(names))
	usedPorts := map[int]bool{}
	ports := make(map[string]int, len(names))
	for _, lease := range activeLeases {
		usedPorts[lease.Port] = true
	}
	for _, name := range names {
		port := requestedPort
		if port == 0 {
			for attempts := 0; attempts < 10; attempts++ {
				port, err = devrun.FindFreePort()
				if err != nil {
					return fmt.Errorf("find port for %s: %w", name, err)
				}
				if !usedPorts[port] {
					break
				}
			}
			if usedPorts[port] {
				return fmt.Errorf("could not reserve a unique port for %s", name)
			}
		}
		usedPorts[port] = true
		ports[name] = port
	}
	for _, name := range names {
		service, err := serviceByName(plan, name)
		if err != nil {
			return err
		}
		if service.State != projectplan.StateManaged {
			return fmt.Errorf("service %q is %s and cannot be started; run `lns plan` for recovery", name, service.State)
		}
		run, err := prepareServiceRun(root, plan, service, ports, os.Getpid())
		if err != nil {
			return fmt.Errorf("prepare %s: %w", name, err)
		}
		runs = append(runs, run)
	}

	registered := make([]serviceRun, 0, len(runs))
	for _, run := range runs {
		if err := store.Add(run.Lease); err != nil {
			cleanupRuns(store, registered, settings)
			return err
		}
		registered = append(registered, run)
	}
	if err := ensureProxyReady(settings); err != nil {
		cleanupRuns(store, registered, settings)
		return err
	}
	defer cleanupRuns(store, registered, settings)

	for _, run := range runs {
		printSuccess("%s", run.URL)
		fmt.Printf("  %s\n", strings.Join(run.Command, " "))
	}
	return executeRuns(runs)
}

func prepareServiceRun(projectRoot string, plan projectplan.Plan, service projectplan.Service, ports map[string]int, pid int) (serviceRun, error) {
	port, ok := ports[service.Name]
	if !ok {
		return serviceRun{}, fmt.Errorf("service %q has no allocated port", service.Name)
	}
	model := models.Service{
		Name: service.Name, Root: service.Root, Script: service.Script,
		Command: append([]string(nil), service.Command...), Profile: service.Profile,
		Status: models.StatusResolved,
	}
	command, err := devrun.ResolveCommand(projectRoot, model, port)
	if err != nil {
		return serviceRun{}, err
	}
	overrides := map[string]string{"HOST": "127.0.0.1", "LNS_URL": service.URL}
	for _, value := range devrun.PortEnvironment(projectRoot, model, port) {
		key, value, _ := strings.Cut(value, "=")
		overrides[key] = value
	}
	for _, related := range plan.Services {
		key := devrun.EnvironmentName(related.Name)
		overrides["LNS_"+key+"_URL"] = related.URL
		overrides["VITE_LNS_"+key+"_URL"] = related.URL
		if relatedPort, exists := ports[related.Name]; exists {
			overrides[key+"_PORT"] = fmt.Sprint(relatedPort)
		}
	}
	for _, binding := range service.Environment {
		switch binding.Kind {
		case projectplan.BindingPort:
			if targetPort, exists := ports[binding.Target]; exists {
				overrides[binding.Name] = fmt.Sprint(targetPort)
			}
		case projectplan.BindingURL:
			target, err := serviceByName(plan, binding.Target)
			if err != nil {
				return serviceRun{}, err
			}
			overrides[binding.Name] = strings.TrimSuffix(target.URL, "/") + binding.Path
		default:
			return serviceRun{}, fmt.Errorf("unsupported environment binding %q", binding.Kind)
		}
	}
	env := devrun.OverlayEnvironment(os.Environ(), overrides)
	return serviceRun{
		Name:    service.Name,
		Root:    filepath.Join(projectRoot, service.Root),
		Command: command,
		URL:     service.URL + "/",
		Lease: devruntime.Lease{
			Project:  plan.Project.Name,
			Service:  service.Name,
			Root:     filepath.Join(projectRoot, service.Root),
			Hostname: service.Hostname,
			Port:     port,
			PID:      pid,
			Worktree: plan.Project.Worktree,
			Profile:  model.Profile,
		},
		Env: env,
	}, nil
}

func buildRunPlan(root string, settings config.Settings) (projectplan.Plan, error) {
	scheme := "http"
	if settings.HTTPS {
		scheme = "https"
	}
	return projectplan.Build(root, projectplan.Route{Scheme: scheme, Port: settings.HTTPPort})
}

func runnableServiceNames(plan projectplan.Plan) []string {
	var names []string
	for _, service := range plan.Services {
		if service.State == projectplan.StateManaged {
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

func replaceService(plan *projectplan.Plan, replacement projectplan.Service) {
	for index := range plan.Services {
		if plan.Services[index].Name == replacement.Name {
			plan.Services[index] = replacement
			return
		}
	}
}

func ensureProxyReady(settings config.Settings) error {
	if _, err := exec.LookPath("caddy"); err != nil {
		return fmt.Errorf("Caddy is not installed or not in PATH")
	}
	if _, err := caddy.RegenerateAllCaddyfiles(); err != nil {
		return fmt.Errorf("generate Caddy config: %w", err)
	}
	global := config.GetGlobalCaddyfilePath()
	var command *exec.Cmd
	if isTCPListening(settings.AdminAddr) {
		command = exec.Command("caddy", "reload", "--config", global, "--address", settings.AdminAddr)
	} else {
		command = exec.Command("caddy", "start", "--config", global)
		detachProcess(command)
	}
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("start or reload Caddy: %w", err)
	}
	return nil
}

func executeRuns(runs []serviceRun) error {
	type result struct {
		name string
		err  error
	}
	results := make(chan result, len(runs))
	interrupts := make(chan os.Signal, 1)
	signal.Notify(interrupts, handledSignals()...)
	defer signal.Stop(interrupts)
	commands := make([]*exec.Cmd, 0, len(runs))
	for _, run := range runs {
		command := exec.Command(run.Command[0], run.Command[1:]...)
		command.Dir = run.Root
		command.Env = run.Env
		command.Stdin = os.Stdin
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Start(); err != nil {
			interruptCommands(commands)
			return fmt.Errorf("start %s: %w", run.Name, err)
		}
		commands = append(commands, command)
		go func(name string, child *exec.Cmd) { results <- result{name: name, err: child.Wait()} }(run.Name, command)
	}

	var first result
	select {
	case first = <-results:
		interruptCommands(commands)
		for i := 1; i < len(commands); i++ {
			<-results
		}
	case <-interrupts:
		interruptCommands(commands)
		for range commands {
			<-results
		}
		return nil
	}
	if first.err != nil && !interruptedExit(first.err) {
		return fmt.Errorf("%s exited: %w", first.name, first.err)
	}
	return nil
}

func interruptCommands(commands []*exec.Cmd) {
	for _, command := range commands {
		if command.Process == nil {
			continue
		}
		_ = command.Process.Signal(os.Interrupt)
		process := command.Process
		go func() {
			time.Sleep(2 * time.Second)
			_ = process.Kill()
		}()
	}
}

func cleanupRuns(store *devruntime.Store, runs []serviceRun, settings config.Settings) {
	for _, run := range runs {
		if err := store.Remove(run.Lease.Project, run.Lease.Service, run.Lease.Worktree, run.Lease.PID); err != nil {
			printWarning("Could not remove runtime route for %s: %v", run.Name, err)
		}
	}
	if _, err := caddy.RegenerateAllCaddyfiles(); err != nil {
		printWarning("Could not regenerate Caddy config during cleanup: %v", err)
		return
	}
	if isTCPListening(settings.AdminAddr) {
		command := exec.Command("caddy", "reload", "--config", config.GetGlobalCaddyfilePath(), "--address", settings.AdminAddr)
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
