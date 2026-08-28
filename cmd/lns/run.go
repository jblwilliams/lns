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
	"lns/internal/discovery"
	"lns/internal/models"
	"lns/internal/projectconfig"
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
	cfg, err := loadOrBootstrapConfig(root)
	if err != nil {
		return err
	}
	if len(override) > 0 {
		if len(names) == 0 {
			selected, err := defaultRunnableService(root, cfg)
			if err != nil {
				return err
			}
			names = []string{selected}
		}
		service := cfg.Services[names[0]]
		service.Script = ""
		service.Command = append([]string(nil), override...)
		service.Status = models.StatusResolved
		cfg.Services[names[0]] = service
	}
	if len(names) == 0 {
		names = runnableServiceNames(cfg)
	}
	if len(names) == 0 {
		return fmt.Errorf("no runnable services; add a script or command to %s", projectconfig.Filename)
	}
	if requestedPort > 0 && len(names) != 1 {
		return fmt.Errorf("--port can only be used when running one service")
	}

	if err := syncProjectForRun(root, cfg); err != nil {
		return err
	}
	settings, err := config.LoadSettings()
	if err != nil {
		return err
	}
	store := devruntime.NewStore()
	activeLeases, err := store.Load()
	if err != nil {
		return err
	}
	worktree := devrun.DetectWorktreePrefix(root)
	runs := make([]serviceRun, 0, len(names))
	usedPorts := map[int]bool{}
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
		run, err := prepareServiceRun(root, cfg, name, port, os.Getpid(), worktree, settings)
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

func prepareServiceRun(projectRoot string, cfg *projectconfig.Config, serviceName string, port, pid int, worktree string, settings config.Settings) (serviceRun, error) {
	serviceConfig, ok := cfg.Services[serviceName]
	if !ok {
		return serviceRun{}, fmt.Errorf("service %q is not defined in %s", serviceName, projectconfig.Filename)
	}
	project := cfg.ToProject(projectRoot)
	var serviceIndex = -1
	for i := range project.Services {
		if project.Services[i].Name == serviceName {
			serviceIndex = i
			break
		}
	}
	if serviceIndex < 0 {
		return serviceRun{}, fmt.Errorf("service %q could not be compiled", serviceName)
	}
	service := project.Services[serviceIndex]
	command, err := devrun.ResolveCommand(projectRoot, service, port)
	if err != nil {
		return serviceRun{}, err
	}
	hostname := devrun.ApplyWorktreePrefix(project.GetServiceHostname(service), worktree)
	url := formatServiceURLWithTLS(hostname, settings.HTTPPort, settings.HTTPS)
	root := filepath.Join(projectRoot, serviceConfig.Root)
	env := append(os.Environ(), devrun.PortEnvironment(projectRoot, service, port)...)
	env = append(env, "HOST=127.0.0.1", "LNS_URL="+strings.TrimSuffix(url, "/"))
	for _, related := range project.Services {
		relatedHostname := devrun.ApplyWorktreePrefix(project.GetServiceHostname(related), worktree)
		relatedURL := strings.TrimSuffix(formatServiceURLWithTLS(relatedHostname, settings.HTTPPort, settings.HTTPS), "/")
		key := devrun.EnvironmentName(related.Name)
		env = append(env, "LNS_"+key+"_URL="+relatedURL, "VITE_LNS_"+key+"_URL="+relatedURL)
	}
	return serviceRun{
		Name:    serviceName,
		Root:    root,
		Command: command,
		URL:     url,
		Lease: devruntime.Lease{
			Project:  cfg.Name,
			Service:  serviceName,
			Root:     root,
			Hostname: hostname,
			Port:     port,
			PID:      pid,
			Worktree: worktree,
			Profile:  service.Profile,
		},
		Env: env,
	}, nil
}

func loadOrBootstrapConfig(root string) (*projectconfig.Config, error) {
	if projectconfig.Exists(root) {
		cfg, err := projectconfig.Load(root)
		if err != nil {
			return nil, err
		}
		changed := false
		for name, service := range cfg.Services {
			if service.Script != "" || len(service.Command) > 0 {
				continue
			}
			if detected, ok := discovery.InspectServiceRoot(root, service.Root); ok && detected.Script != "" {
				service.Script = detected.Script
				if service.Profile == "" {
					service.Profile = detected.Profile
				}
				service.Status = models.StatusResolved
				cfg.Services[name] = service
				changed = true
			}
		}
		if changed {
			if err := projectconfig.Save(root, cfg); err != nil {
				return nil, err
			}
			printSuccess("Updated runnable scripts in %s", projectconfig.Path(root))
		}
		return cfg, nil
	}
	cfg := discovery.BootstrapConfig(projectplan.ProjectName(root), root, "")
	if err := projectconfig.Save(root, cfg); err != nil {
		return nil, err
	}
	printSuccess("Created %s", projectconfig.Path(root))
	return cfg, nil
}

func runnableServiceNames(cfg *projectconfig.Config) []string {
	project := cfg.ToProject("")
	var names []string
	for _, service := range project.Services {
		if service.CanRun() {
			names = append(names, service.Name)
		}
	}
	sort.Strings(names)
	return names
}

func defaultRunnableService(root string, cfg *projectconfig.Config) (string, error) {
	names := runnableServiceNames(cfg)
	if len(names) == 1 {
		return names[0], nil
	}
	for _, name := range names {
		serviceRoot := filepath.Clean(filepath.Join(root, cfg.Services[name].Root))
		if sameFilePath(serviceRoot, root) {
			return name, nil
		}
	}
	return "", fmt.Errorf("choose a service: %s", strings.Join(names, ", "))
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
