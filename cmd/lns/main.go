package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"lns/internal/caddy"
	"lns/internal/config"
	"lns/internal/discovery"
	"lns/internal/models"
	"lns/internal/projectconfig"
	"lns/internal/registry"
)

var version = "0.3.0"

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "lns",
	Short: "Run local services at stable HTTPS names",
	Args:  cobra.NoArgs,
	Long: `lns discovers or reads repo-local service configuration, leases dynamic
development ports, and routes stable HTTPS names through Caddy.`,
	Example: `  lns
  lns plan
  lns run web`,
}

func init() {
	rootCmd.AddCommand(versionCmd)
	rootCmd.AddCommand(initCmd)
	rootCmd.AddCommand(syncCmd)
	rootCmd.AddCommand(serviceCmd)
	rootCmd.AddCommand(statusCmd)
	rootCmd.AddCommand(checkCmd)
	rootCmd.AddCommand(startCmd)
	rootCmd.AddCommand(stopCmd)
	rootCmd.AddCommand(reloadCmd)
	rootCmd.AddCommand(exportCmd)
	rootCmd.AddCommand(setupCmd)
	rootCmd.AddCommand(configCmd)
	rootCmd.AddCommand(doctorCmd)
	serviceCmd.AddCommand(serviceAddCmd)

	initCmd.Flags().StringP("path", "p", "", "Project root directory (default: current directory)")
	initCmd.Flags().String("prefix", "", "Hostname prefix (default: project name)")

	syncCmd.Flags().StringP("path", "p", "", "Project root directory (default: current directory)")

	serviceAddCmd.Flags().IntP("port", "p", 0, "Canonical service port")
	serviceAddCmd.Flags().Int("container-port", 0, "Stable container port for Docker/staging/production")
	serviceAddCmd.Flags().String("script", "", "package.json script to run dynamically (for example: dev)")
	serviceAddCmd.Flags().StringArray("command", nil, "command argument to run dynamically; repeat for each argument")
	serviceAddCmd.Flags().String("profile", "", "Proxy profile (hmr or standard)")
	serviceAddCmd.Flags().StringP("hostname", "H", "", "Explicit hostname")
	serviceAddCmd.Flags().BoolP("docker", "d", false, "Service runs in Docker")
	serviceAddCmd.Flags().StringP("container", "c", "", "Docker container name")

	statusCmd.Flags().Bool("global", false, "Show the global registry view even if lns.json exists in the current repo")

	exportCmd.Flags().StringP("output", "o", "", "Output file path")
	exportCmd.Flags().BoolP("standalone", "s", true, "Generate standalone Caddyfile")
	exportCmd.Flags().Bool("docker-compose", false, "Generate a docker-compose snippet for running Caddy in Docker")
	exportCmd.Flags().String("upstream", "host", "Upstream mode for generated Caddyfile (host or docker)")
	exportCmd.Flags().Int("proxy-port", 80, "Listener port in the exported Caddy/Docker config")
	_ = exportCmd.Flags().MarkDeprecated("standalone", "use --docker-compose to output a docker-compose snippet")

	startCmd.Flags().Bool("setup", false, "Run interactive setup before starting")
	startCmd.Flags().Int("http-port", 0, "Proxy HTTP port (saves to settings)")
	startCmd.Flags().Bool("no-tls", false, "Use plain HTTP instead of local HTTPS")
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("lns version %s\n", version)
	},
}

var initCmd = &cobra.Command{
	Use:   "init [project-name]",
	Short: "Bootstrap canonical repo config in lns.json",
	Args:  cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		root, err := resolveRoot(cmd)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}

		projectName := filepath.Base(root)
		if len(args) == 1 {
			projectName = strings.TrimSpace(args[0])
		}
		if projectName == "" {
			printError("project name could not be inferred")
			os.Exit(1)
		}

		if projectconfig.Exists(root) {
			printWarning("Config already exists: %s", projectconfig.Path(root))
			fmt.Println("Run `lns sync` to compile the existing manifest.")
			return
		}

		prefix, _ := cmd.Flags().GetString("prefix")
		cfg := discovery.BootstrapConfig(projectName, root, prefix)
		if err := projectconfig.Save(root, cfg); err != nil {
			printError("Failed to write lns.json: %v", err)
			os.Exit(1)
		}

		printSuccess("Created %s", projectconfig.Path(root))
		fmt.Printf("  Project: %s\n", cfg.Name)
		if cfg.Prefix != "" {
			fmt.Printf("  Prefix: %s\n", cfg.Prefix)
		}

		fmt.Printf("  Services: %d\n", len(cfg.Services))
		unresolved := unresolvedServices(cfg)
		if len(unresolved) > 0 {
			fmt.Println()
			color.Yellow("Unresolved services:")
			for _, name := range unresolved {
				svc := cfg.Services[name]
				serviceRoot := svc.Root
				if serviceRoot == "" {
					serviceRoot = "."
				}
				fmt.Printf("  %s\n", color.CyanString("lns service add %s %s --port <port>", name, serviceRoot))
			}
			fmt.Println()
			fmt.Println("Fill the missing port, then run `lns sync`.")
			return
		}

		fmt.Println()
		fmt.Println("Next:")
		fmt.Printf("  %s\n", color.CyanString("lns sync"))
	},
}

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Compile lns.json into registry and Caddy state",
	Run: func(cmd *cobra.Command, args []string) {
		root, err := resolveRoot(cmd)
		if err != nil {
			printError("%v", err)
			os.Exit(1)
		}

		cfg, err := projectconfig.Load(root)
		if err != nil {
			printError("Failed to load %s: %v", projectconfig.Path(root), err)
			fmt.Println("Run `lns init` first if this repo has not been bootstrapped yet.")
			os.Exit(1)
		}

		if err := syncProject(root, cfg); err != nil {
			printError("%v", err)
			os.Exit(1)
		}
	},
}

var serviceCmd = &cobra.Command{
	Use:   "service",
	Short: "Manage services in the canonical repo config",
}

var serviceAddCmd = &cobra.Command{
	Use:   "add <name> <root>",
	Short: "Add or resolve a service in lns.json",
	Args:  cobra.ExactArgs(2),
	Run: func(cmd *cobra.Command, args []string) {
		root, err := os.Getwd()
		if err != nil {
			printError("Failed to resolve current directory: %v", err)
			os.Exit(1)
		}

		cfg, err := projectconfig.Load(root)
		if err != nil {
			printError("Failed to load %s: %v", projectconfig.Path(root), err)
			fmt.Println("Run `lns init` first.")
			os.Exit(1)
		}

		name := strings.TrimSpace(args[0])
		if name == "" {
			printError("service name is required")
			os.Exit(1)
		}

		rootValue := cleanRelativeRoot(args[1])
		port, _ := cmd.Flags().GetInt("port")
		containerPort, _ := cmd.Flags().GetInt("container-port")
		script, _ := cmd.Flags().GetString("script")
		command, _ := cmd.Flags().GetStringArray("command")
		profileValue, _ := cmd.Flags().GetString("profile")
		hostname, _ := cmd.Flags().GetString("hostname")
		docker, _ := cmd.Flags().GetBool("docker")
		container, _ := cmd.Flags().GetString("container")

		if existing, exists := cfg.Services[name]; exists && existing.Status == models.StatusResolved {
			printError("service %q already exists; edit lns.json directly if you need to change it", name)
			os.Exit(1)
		}

		profile := models.Profile(strings.ToLower(strings.TrimSpace(profileValue)))
		if profile == "" {
			if detected, ok := discovery.InspectServiceRoot(root, rootValue); ok && detected.Profile != "" {
				profile = detected.Profile
			} else {
				profile = models.ProfileStandard
			}
		}
		if profile != models.ProfileHMR && profile != models.ProfileStandard {
			printError("invalid profile %q (expected hmr or standard)", profileValue)
			os.Exit(1)
		}

		if hostname != "" {
			normalized, err := normalizeHostname(hostname)
			if err != nil {
				printError("invalid hostname: %v", err)
				os.Exit(1)
			}
			hostname = normalized
		}

		cfg.Services[name] = projectconfig.Service{
			Root:          rootValue,
			Port:          port,
			ContainerPort: containerPort,
			Script:        script,
			Command:       command,
			Profile:       profile,
			Hostname:      hostname,
			Source:        models.SourceManual,
			Status:        models.StatusResolved,
			Docker:        docker,
			ContainerName: container,
		}
		cfg.Normalize()

		if errs := projectconfig.Validate(cfg); len(errs) > 0 {
			printValidationErrors(errs)
			os.Exit(1)
		}

		if err := projectconfig.Save(root, cfg); err != nil {
			printError("Failed to update %s: %v", projectconfig.Path(root), err)
			os.Exit(1)
		}

		printSuccess("Updated %s", projectconfig.Path(root))
		fmt.Printf("  Service: %s\n", name)
		fmt.Printf("  Root: %s\n", rootValue)
		if port > 0 {
			fmt.Printf("  Fixed port: %d\n", port)
		}
		if containerPort > 0 {
			fmt.Printf("  Container port: %d\n", containerPort)
		}
		if script != "" {
			fmt.Printf("  Script: %s\n", script)
		}
		if len(command) > 0 {
			fmt.Printf("  Command: %s\n", strings.Join(command, " "))
		}
		fmt.Printf("  Profile: %s\n", profile)
		if hostname != "" {
			fmt.Printf("  Hostname: %s\n", hostname)
		}

		fmt.Println()
		fmt.Println("Next:")
		fmt.Printf("  %s\n", color.CyanString("lns sync"))
	},
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show repo-local service status or the global registry view",
	Run: func(cmd *cobra.Command, args []string) {
		forceGlobal, _ := cmd.Flags().GetBool("global")
		root, err := os.Getwd()
		if err != nil {
			printError("Failed to resolve current directory: %v", err)
			os.Exit(1)
		}

		if !forceGlobal && projectconfig.Exists(root) {
			showRepoStatus(root)
			return
		}

		showGlobalStatus()
	},
}

var checkCmd = &cobra.Command{
	Use:   "check <port>",
	Short: "Check whether a canonical port is already owned",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		port, err := strconv.Atoi(args[0])
		if err != nil || port < 1 || port > 65535 {
			printError("Invalid port: %s", args[0])
			os.Exit(1)
		}

		mgr, err := registry.NewManager()
		if err != nil {
			printError("Failed to load registry: %v", err)
			os.Exit(1)
		}

		if owner := mgr.CheckPortConflict(port); owner != "" {
			color.Yellow("Port %d is owned by %s", port, owner)
			return
		}

		color.Green("Port %d is available", port)
	},
}

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the global Caddy proxy",
	Run: func(cmd *cobra.Command, args []string) {
		if err := config.EnsureConfigDirs(); err != nil {
			printError("Failed to create config directories: %v", err)
			os.Exit(1)
		}

		forceSetup, _ := cmd.Flags().GetBool("setup")
		httpPortFlag, _ := cmd.Flags().GetInt("http-port")
		noTLS, _ := cmd.Flags().GetBool("no-tls")

		settingsPath := config.GetSettingsPath()
		settingsExists := fileExists(settingsPath)

		settings, err := config.LoadSettings()
		if err != nil {
			if settingsExists && isTerminal(os.Stdin) {
				settingsExists = false
				forceSetup = true
				settings = config.DefaultSettings()
			} else {
				printError("Failed to load settings: %v", err)
				os.Exit(1)
			}
		}

		if httpPortFlag != 0 {
			if httpPortFlag < 1 || httpPortFlag > 65535 {
				printError("Invalid --http-port: %d", httpPortFlag)
				os.Exit(1)
			}
			settings.HTTPPort = httpPortFlag
			if err := config.SaveSettings(settings); err != nil {
				printError("Failed to save settings: %v", err)
				os.Exit(1)
			}
			settingsExists = true
		}
		if noTLS {
			settings.HTTPS = false
			if httpPortFlag == 0 && settings.HTTPPort == config.DefaultHTTPPort {
				settings.HTTPPort = 80
			}
			if err := config.SaveSettings(settings); err != nil {
				printError("Failed to save settings: %v", err)
				os.Exit(1)
			}
		}

		if forceSetup || (!settingsExists && isTerminal(os.Stdin)) {
			if err := runInteractiveSetup("lns start"); err != nil {
				printError("%v", err)
				os.Exit(1)
			}
			settings, err = config.LoadSettings()
			if err != nil {
				printError("Failed to load settings: %v", err)
				os.Exit(1)
			}
		}

		paths, err := caddy.RegenerateAllCaddyfiles()
		if err != nil {
			printError("Failed to generate Caddyfiles: %v", err)
			os.Exit(1)
		}
		printSuccess("Generated %d Caddyfile(s)", len(paths))

		caddyfilePath := config.GetGlobalCaddyfilePath()

		if _, err := exec.LookPath("caddy"); err != nil {
			printError("Caddy is not installed or not in PATH")
			fmt.Println()
			fmt.Println("Install Caddy:")
			fmt.Printf("  macOS:  %s\n", color.CyanString("brew install caddy"))
			fmt.Printf("  Linux:  %s or see https://caddyserver.com/docs/install\n", color.CyanString("sudo apt install caddy"))
			os.Exit(1)
		}

		if isTCPListening(settings.AdminAddr) {
			color.Yellow("LNS proxy is already running. Use 'lns reload' to apply changes.")
			return
		}

		fmt.Printf("Starting Caddy with config: %s\n", caddyfilePath)
		fmt.Println()
		if settings.HTTPPort < 1024 {
			color.New(color.Faint).Printf("Note: Port %d may require extra local setup on some macOS/Linux machines.\n", settings.HTTPPort)
			color.New(color.Faint).Printf("If binding fails, rerun setup or use an unprivileged port like %d.\n", config.FallbackHTTPPort)
			color.New(color.Faint).Println("Linux example: sudo setcap 'cap_net_bind_service=+ep' $(which caddy)")
			fmt.Println()
		}

		execCmd := exec.Command("caddy", "start", "--config", caddyfilePath)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			printError("Error starting Caddy: %v", err)
			os.Exit(1)
		}

		printSuccess("Caddy started successfully")
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop",
	Short: "Stop the global Caddy proxy",
	Run: func(cmd *cobra.Command, args []string) {
		settings, err := config.LoadSettings()
		if err != nil {
			printError("Failed to load settings: %v", err)
			os.Exit(1)
		}

		if !isTCPListening(settings.AdminAddr) {
			color.Yellow("LNS proxy is not running.")
			return
		}

		execCmd := exec.Command("caddy", "stop", "--address", settings.AdminAddr)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			printError("Error stopping Caddy: %v", err)
			os.Exit(1)
		}

		printSuccess("Caddy stopped")
	},
}

var reloadCmd = &cobra.Command{
	Use:   "reload",
	Short: "Reload Caddy configuration from the compiled registry state",
	Run: func(cmd *cobra.Command, args []string) {
		caddyfilePath := config.GetGlobalCaddyfilePath()
		if _, err := os.Stat(caddyfilePath); os.IsNotExist(err) {
			color.Yellow("No Caddyfile found. Run 'lns start' first.")
			os.Exit(1)
		}

		settings, err := config.LoadSettings()
		if err != nil {
			printError("Failed to load settings: %v", err)
			os.Exit(1)
		}

		if !isTCPListening(settings.AdminAddr) {
			printError("LNS proxy is not running at %s", settings.AdminAddr)
			fmt.Println("Run `lns start` first, then use `lns reload` for later changes.")
			os.Exit(1)
		}

		paths, err := caddy.RegenerateAllCaddyfiles()
		if err != nil {
			printError("Failed to regenerate Caddyfiles: %v", err)
			os.Exit(1)
		}
		printSuccess("Regenerated %d Caddyfile(s)", len(paths))

		execCmd := exec.Command("caddy", "reload", "--config", caddyfilePath, "--address", settings.AdminAddr)
		execCmd.Stdout = os.Stdout
		execCmd.Stderr = os.Stderr

		if err := execCmd.Run(); err != nil {
			printError("Error reloading Caddy: %v", err)
			os.Exit(1)
		}

		printSuccess("Caddy configuration reloaded")
	},
}

var exportCmd = &cobra.Command{
	Use:   "export <project>",
	Short: "Export a project's compiled Caddy config",
	Args:  cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		projectName := args[0]
		output, _ := cmd.Flags().GetString("output")
		standalone, _ := cmd.Flags().GetBool("standalone")
		dockerCompose, _ := cmd.Flags().GetBool("docker-compose")
		upstreamStr, _ := cmd.Flags().GetString("upstream")
		proxyPort, _ := cmd.Flags().GetInt("proxy-port")
		if dockerCompose {
			standalone = false
		}
		if proxyPort < 1 || proxyPort > 65535 {
			printError("Invalid --proxy-port: %d", proxyPort)
			os.Exit(1)
		}

		mgr, err := registry.NewManager()
		if err != nil {
			printError("Failed to load registry: %v", err)
			os.Exit(1)
		}

		project, exists := mgr.GetProject(projectName)
		if !exists {
			printError("Project %q not found", projectName)
			os.Exit(1)
		}

		var content string
		if standalone {
			upstreamMode := caddy.UpstreamModeHost
			switch strings.ToLower(strings.TrimSpace(upstreamStr)) {
			case "", "host":
				upstreamMode = caddy.UpstreamModeHost
			case "docker", "docker-network":
				upstreamMode = caddy.UpstreamModeDockerNetwork
			default:
				printError("Invalid --upstream: %s (expected host or docker)", upstreamStr)
				os.Exit(1)
			}
			for _, service := range project.Services {
				port := service.Port
				if upstreamMode == caddy.UpstreamModeDockerNetwork {
					port = service.DeploymentPort()
				}
				if port == 0 {
					printError("Service %q has no port for this export; set port or container_port in lns.json", service.Name)
					os.Exit(1)
				}
			}

			content = caddy.GenerateStandaloneCaddyfile(project, proxyPort, upstreamMode)
		} else {
			content = caddy.GenerateDockerComposeSnippet(project, proxyPort)
		}

		if output != "" {
			if err := os.WriteFile(output, []byte(content), 0644); err != nil {
				printError("Failed to write file: %v", err)
				os.Exit(1)
			}
			if standalone {
				printSuccess("Exported Caddyfile to %s", output)
			} else {
				printSuccess("Exported docker-compose snippet to %s", output)
			}
			return
		}

		fmt.Print(content)
	},
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Show configuration directory and paths",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("Config directory:   %s\n", config.GetConfigDir())
		fmt.Printf("Global Caddyfile:   %s\n", config.GetGlobalCaddyfilePath())
		fmt.Printf("Projects directory: %s\n", config.GetCaddyConfigDir())
		fmt.Printf("Registry file:      %s\n", config.GetRegistryPath())
		fmt.Printf("Settings file:      %s\n", config.GetSettingsPath())
		fmt.Printf("Runtime leases:     %s\n", config.GetRuntimePath())
		if settings, err := config.LoadSettings(); err == nil {
			fmt.Printf("Proxy port:         %d\n", settings.HTTPPort)
			fmt.Printf("HTTPS:              %t\n", settings.HTTPS)
			fmt.Printf("Caddy admin:        %s\n", settings.AdminAddr)
		}
	},
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check system requirements and configuration",
	Run: func(cmd *cobra.Command, args []string) {
		var issues []string
		settings, err := config.LoadSettings()
		if err != nil {
			settings = config.DefaultSettings()
		}

		caddyPath, err := exec.LookPath("caddy")
		if err == nil {
			printSuccess("Caddy installed: %s", caddyPath)
			if out, err := exec.Command("caddy", "version").Output(); err == nil {
				fmt.Printf("  Version: %s", string(out))
			}
		} else {
			printFailed("Caddy not found in PATH")
			issues = append(issues, "Install Caddy: https://caddyserver.com/docs/install")
		}

		configDir := config.GetConfigDir()
		if _, err := os.Stat(configDir); err == nil {
			printSuccess("Config directory exists: %s", configDir)
		} else {
			printWarning("Config directory not created yet: %s", configDir)
		}

		if isTCPListening(settings.AdminAddr) {
			printSuccess("LNS proxy is running")
		} else {
			printWarning("LNS proxy is not running")
		}

		fmt.Println()
		if settings.HTTPPort < 1024 {
			color.New(color.Faint).Printf("Note: Port %d may require extra local setup on some macOS/Linux machines.\n", settings.HTTPPort)
			color.New(color.Faint).Printf("If binding fails, switch to an unprivileged port like %d.\n", config.FallbackHTTPPort)
			color.New(color.Faint).Println("Linux example: sudo setcap 'cap_net_bind_service=+ep' $(which caddy)")
		}

		if len(issues) > 0 {
			fmt.Println()
			color.Yellow("Issues found:")
			for _, issue := range issues {
				fmt.Printf("  - %s\n", issue)
			}
		}
	},
}

func showRepoStatus(root string) {
	cfg, err := projectconfig.Load(root)
	if err != nil {
		printError("Failed to load %s: %v", projectconfig.Path(root), err)
		os.Exit(1)
	}

	if errs := projectconfig.Validate(cfg); len(errs) > 0 {
		printValidationErrors(errs)
		os.Exit(1)
	}

	settings := loadSettingsOrDefault()
	drifts := discovery.DetectDrift(root, cfg)
	driftNotes := map[string][]string{}
	for _, drift := range drifts {
		driftNotes[drift.Service] = append(driftNotes[drift.Service], fmt.Sprintf("%s %s -> %s", drift.Field, drift.Configured, drift.Detected))
	}

	mgr, err := registry.NewManager()
	if err != nil {
		printError("Failed to load registry: %v", err)
		os.Exit(1)
	}

	project, synced := mgr.GetProject(cfg.Name)

	fmt.Printf("Repo: %s\n", root)
	fmt.Printf("Config: %s\n", projectconfig.Path(root))
	fmt.Printf("Proxy port: %d\n", settings.HTTPPort)
	if synced && project.Path == root {
		printSuccess("Synced project %q is present in the registry", cfg.Name)
	} else {
		printWarning("Project %q is not synced from this repo state", cfg.Name)
	}
	fmt.Println()

	projectView := cfg.ToProject(root)
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		"Service",
		"Status",
		"Source",
		"Root",
		"Port",
		"Profile",
		"URL",
	)

	for _, service := range projectView.Services {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			service.Name,
			service.EffectiveStatus(),
			service.EffectiveSource(),
			service.Root,
			optionalInt(service.Port),
			optionalString(string(service.EffectiveProfile())),
			resolvedServiceURL(&projectView, service, settings.HTTPPort, settings.HTTPS),
		)
	}
	_ = w.Flush()

	if len(driftNotes) > 0 {
		fmt.Println()
		color.Yellow("Drift:")
		names := make([]string, 0, len(driftNotes))
		for name := range driftNotes {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Printf("  - %s: %s\n", name, strings.Join(driftNotes[name], ", "))
		}
	}
}

func showGlobalStatus() {
	mgr, err := registry.NewManager()
	if err != nil {
		printError("Failed to load registry: %v", err)
		os.Exit(1)
	}

	projects := mgr.ListProjects()
	settings := loadSettingsOrDefault()

	if len(projects) == 0 {
		color.Yellow("No synced projects found.")
		fmt.Println()
		fmt.Println("Get started:")
		fmt.Printf("  %s\n", color.CyanString("lns init"))
		fmt.Printf("  %s\n", color.CyanString("lns sync"))
		return
	}

	sort.Slice(projects, func(i, j int) bool {
		return projects[i].Name < projects[j].Name
	})

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
		"Project",
		"Service",
		"Port",
		"Profile",
		"Source",
		"URL",
		"Status",
	)

	for _, project := range projects {
		for index, service := range project.Services {
			projectLabel := ""
			if index == 0 {
				projectLabel = project.Name
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				projectLabel,
				service.Name,
				optionalInt(service.Port),
				service.EffectiveProfile(),
				service.EffectiveSource(),
				resolvedServiceURL(&project, service, settings.HTTPPort, settings.HTTPS),
				service.EffectiveStatus(),
			)
		}
	}
	_ = w.Flush()
}

func syncProject(root string, cfg *projectconfig.Config) error {
	return syncProjectWithOutput(root, cfg, true)
}

func syncProjectForRun(root string, cfg *projectconfig.Config) error {
	return syncProjectWithOutput(root, cfg, false)
}

func syncProjectWithOutput(root string, cfg *projectconfig.Config, verbose bool) error {
	if errs := projectconfig.Validate(cfg); len(errs) > 0 {
		return fmt.Errorf("invalid %s:\n%s", projectconfig.Filename, formatValidationErrors(errs))
	}

	unresolved := unresolvedServices(cfg)
	if len(unresolved) > 0 {
		return fmt.Errorf("sync blocked by unresolved services: %s", strings.Join(unresolved, ", "))
	}

	project := cfg.ToProject(root)
	mgr, err := registry.NewManager()
	if err != nil {
		return fmt.Errorf("load registry: %w", err)
	}

	if errs := mgr.ValidateProjectConflicts(project); len(errs) > 0 {
		lines := make([]string, 0, len(errs))
		for _, err := range errs {
			lines = append(lines, "- "+err.Error())
		}
		return fmt.Errorf("sync validation failed:\n%s", strings.Join(lines, "\n"))
	}

	registryPath := config.GetRegistryPath()
	projectCaddyPath := caddy.ProjectCaddyfilePath(project.Name)
	globalCaddyPath := config.GetGlobalCaddyfilePath()

	registrySnapshot, registryExists := readOptionalFile(registryPath)
	projectSnapshot, projectExists := readOptionalFile(projectCaddyPath)
	globalSnapshot, globalExists := readOptionalFile(globalCaddyPath)

	applied := false
	defer func() {
		if applied {
			return
		}
		restoreOptionalFile(registryPath, registrySnapshot, registryExists)
		restoreOptionalFile(projectCaddyPath, projectSnapshot, projectExists)
		restoreOptionalFile(globalCaddyPath, globalSnapshot, globalExists)
	}()

	if err := mgr.UpsertProject(project); err != nil {
		return fmt.Errorf("save registry: %w", err)
	}
	if _, err := caddy.WriteProjectCaddyfile(&project); err != nil {
		return fmt.Errorf("write project Caddyfile: %w", err)
	}
	if _, err := caddy.WriteGlobalCaddyfile(); err != nil {
		return fmt.Errorf("write global Caddyfile: %w", err)
	}

	applied = true

	settings := loadSettingsOrDefault()
	if verbose {
		printSuccess("Synced %s", cfg.Name)
		fmt.Printf("  Registry: %s\n", registryPath)
		fmt.Printf("  Project Caddyfile: %s\n", projectCaddyPath)
		fmt.Printf("  Global Caddyfile: %s\n", globalCaddyPath)
		fmt.Printf("  Proxy port: %d\n", settings.HTTPPort)
		fmt.Println("  URLs:")
		for _, service := range project.Services {
			fmt.Printf("    %s %s\n", service.Name, resolvedServiceURL(&project, service, settings.HTTPPort, settings.HTTPS))
		}
	}

	if drifts := discovery.DetectDrift(root, cfg); len(drifts) > 0 {
		fmt.Println()
		printWarning("Drift detected between lns.json and repo signals")
		for _, drift := range drifts {
			fmt.Printf("  - %s %s: config=%s detected=%s", drift.Service, drift.Field, drift.Configured, drift.Detected)
			if drift.Evidence != "" {
				fmt.Printf(" (%s)", drift.Evidence)
			}
			fmt.Println()
		}
	}

	if isTCPListening(settings.AdminAddr) {
		if _, err := caddy.RegenerateAllCaddyfiles(); err != nil {
			return fmt.Errorf("regenerate active Caddy config: %w", err)
		}
		reload := exec.Command("caddy", "reload", "--config", globalCaddyPath, "--address", settings.AdminAddr)
		reload.Stdout = os.Stdout
		reload.Stderr = os.Stderr
		if err := reload.Run(); err != nil {
			return fmt.Errorf("reload Caddy: %w", err)
		}
		if verbose {
			fmt.Println()
			printSuccess("Running proxy reloaded")
		}
	} else if verbose {
		fmt.Println()
		fmt.Println("Next:")
		fmt.Printf("  %s\n", color.CyanString("lns start"))
	}
	return nil
}

func resolveRoot(cmd *cobra.Command) (string, error) {
	root, _ := cmd.Flags().GetString("path")
	if root == "" {
		return os.Getwd()
	}
	return filepath.Abs(root)
}

func unresolvedServices(cfg *projectconfig.Config) []string {
	names := []string{}
	for _, name := range cfg.SortedServiceNames() {
		service := cfg.Services[name]
		status := service.Status
		if status == "" {
			status = models.StatusUnresolved
		}
		if status == models.StatusUnresolved {
			names = append(names, name)
		}
	}
	return names
}

func formatValidationErrors(errs []projectconfig.ValidationError) string {
	lines := make([]string, 0, len(errs))
	for _, err := range errs {
		lines = append(lines, "- "+err.Error())
	}
	return strings.Join(lines, "\n")
}

func printValidationErrors(errs []projectconfig.ValidationError) {
	printError("Invalid %s", projectconfig.Filename)
	for _, err := range errs {
		fmt.Printf("  - %s\n", err)
	}
}

func optionalString(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func optionalInt(value int) string {
	if value == 0 {
		return "-"
	}
	return strconv.Itoa(value)
}

func cleanRelativeRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		return "."
	}
	return filepath.Clean(root)
}

func readOptionalFile(path string) ([]byte, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	return data, true
}

func restoreOptionalFile(path string, data []byte, existed bool) {
	if existed {
		_ = os.MkdirAll(filepath.Dir(path), 0755)
		_ = os.WriteFile(path, data, 0644)
		return
	}
	_ = os.Remove(path)
}

func printSuccess(format string, args ...interface{}) {
	fmt.Printf("%s %s\n", color.GreenString("✓"), fmt.Sprintf(format, args...))
}

func printError(format string, args ...interface{}) {
	fmt.Printf("%s %s\n", color.RedString("✗"), fmt.Sprintf(format, args...))
}

func printWarning(format string, args ...interface{}) {
	fmt.Printf("%s %s\n", color.YellowString("!"), fmt.Sprintf(format, args...))
}

func printFailed(format string, args ...interface{}) {
	fmt.Printf("%s %s\n", color.RedString("✗"), fmt.Sprintf(format, args...))
}
