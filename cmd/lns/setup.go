package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"lns/internal/config"
	"lns/internal/projectconfig"
)

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Interactive setup for proxy port and hostnames",
	Run: func(cmd *cobra.Command, args []string) {
		if err := runInteractiveSetup("lns start"); err != nil {
			printError("%v", err)
			os.Exit(1)
		}
	},
}

func runInteractiveSetup(nextCommand string) error {
	if err := config.EnsureConfigDirs(); err != nil {
		return fmt.Errorf("failed to create config directories: %w", err)
	}

	settings := config.DefaultSettings()
	if loaded, err := config.LoadSettings(); err == nil {
		settings = loaded
	}

	reader := bufio.NewReader(os.Stdin)

	fmt.Println(color.New(color.FgGreen, color.Bold).Sprint("LNS setup"))
	fmt.Println(color.New(color.Faint).Sprint("Press Enter to accept defaults."))
	fmt.Println()

	fmt.Println()
	color.New(color.Faint).Println("Hostnames now live in repo-local lns.json; setup only configures the proxy itself.")

	fmt.Println()
	https := promptConfirm(reader, "Use local HTTPS?", settings.HTTPS)
	settings.HTTPS = https
	printProxyPortHelp(settings.HTTPPort, https)
	httpPort, err := promptPort(reader, "Proxy port", settings.HTTPPort)
	if err != nil {
		return err
	}

	httpPort = maybeFallbackFromBusyPort(reader, httpPort)

	if httpPort < 1024 {
		fmt.Println()
		color.Yellow("Port %d is a privileged port.", httpPort)
		color.New(color.Faint).Println("On some macOS/Linux setups, Caddy may need elevated privileges or extra local setup to bind it.")
		color.New(color.Faint).Println("For local development this is usually fine once configured, but it is less portable across machines than 8888.")
		if !promptConfirm(reader, "Continue with this port?", false) {
			httpPort = config.FallbackHTTPPort
			color.New(color.Faint).Printf("Using %d instead.\n", httpPort)
		}
	}

	settings.HTTPPort = httpPort

	fmt.Println()
	color.New(color.Faint).Printf("Example app URL on port %d: %s\n", httpPort, formatServiceURLWithTLS("project-a.localhost", httpPort, https))
	fmt.Println()
	printAdminAddrHelp(settings.AdminAddr)
	adminAddr, err := promptAdminAddr(reader, "Caddy admin address", settings.AdminAddr)
	if err != nil {
		return err
	}
	settings.AdminAddr = adminAddr

	if err := config.SaveSettings(settings); err != nil {
		return fmt.Errorf("failed to save settings: %w", err)
	}
	printSuccess("Saved settings: %s", config.GetSettingsPath())

	if nextCommand != "" {
		fmt.Println()
		fmt.Println("Next:")
		fmt.Printf("  %s\n", color.CyanString(nextCommand))
	}

	return nil
}

func promptString(reader *bufio.Reader, label, defaultValue string) (string, error) {
	fmt.Printf("%s [%s]: ", label, defaultValue)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return defaultValue, nil
	}
	return line, nil
}

func promptPort(reader *bufio.Reader, label string, defaultPort int) (int, error) {
	input, err := promptString(reader, label, strconv.Itoa(defaultPort))
	if err != nil {
		return 0, err
	}
	port, err := strconv.Atoi(strings.TrimSpace(input))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid port: %q", input)
	}
	return port, nil
}

func printProxyPortHelp(defaultPort int, https bool) {
	color.New(color.Faint).Println("This is the port where the shared Caddy proxy listens for all *.localhost routes.")
	color.New(color.Faint).Printf("Default: %s\n", formatServiceURLWithTLS("project-a.localhost", config.DefaultHTTPPort, false))
	color.New(color.Faint).Printf("Unprivileged fallback: %s\n", formatServiceURLWithTLS("project-a.localhost", config.FallbackHTTPPort, https))
	if defaultPort != config.DefaultHTTPPort && defaultPort != config.FallbackHTTPPort {
		color.New(color.Faint).Printf("Current setting: %s\n", formatServiceURLWithTLS("project-a.localhost", defaultPort, https))
	}
}

func maybeFallbackFromBusyPort(reader *bufio.Reader, port int) int {
	if !isTCPListening(net.JoinHostPort("127.0.0.1", strconv.Itoa(port))) {
		return port
	}

	fmt.Println()
	color.Yellow("Port %d already appears to be in use on localhost.", port)
	if port == config.DefaultHTTPPort {
		if promptConfirm(reader, fmt.Sprintf("Use fallback port %d instead?", config.FallbackHTTPPort), true) {
			color.New(color.Faint).Printf("Using %d instead.\n", config.FallbackHTTPPort)
			return config.FallbackHTTPPort
		}
		return port
	}

	color.New(color.Faint).Println("Choose another port if you want to avoid a conflict.")
	return port
}

func promptAdminAddr(reader *bufio.Reader, label string, defaultAddr string) (string, error) {
	input, err := promptString(reader, label, defaultAddr)
	if err != nil {
		return "", err
	}

	addr := strings.TrimSpace(input)
	if addr == "" {
		addr = defaultAddr
	}

	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("invalid address: %q (expected host:port)", addr)
	}
	if host == "" {
		return "", fmt.Errorf("invalid address: %q (missing host)", addr)
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("invalid port in address: %q", addr)
	}

	return addr, nil
}

func printAdminAddrHelp(defaultAddr string) {
	color.New(color.Faint).Println("This is Caddy's local admin endpoint used by `lns reload` and `lns stop`.")
	color.New(color.Faint).Printf("Default: %s\n", defaultAddr)
	color.New(color.Faint).Println("It is not your app URL. Keep it on loopback if possible and do not expose it publicly.")
}

func promptConfirm(reader *bufio.Reader, label string, defaultYes bool) bool {
	defaultHint := "y/N"
	if defaultYes {
		defaultHint = "Y/n"
	}
	fmt.Printf("%s [%s]: ", label, defaultHint)
	line, err := reader.ReadString('\n')
	if err != nil {
		return defaultYes
	}
	line = strings.TrimSpace(strings.ToLower(line))
	if line == "" {
		return defaultYes
	}
	return line == "y" || line == "yes"
}

func normalizeHostname(input string) (string, error) {
	return projectconfig.NormalizeHostnameInput(input)
}
