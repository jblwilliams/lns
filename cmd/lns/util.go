package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"
)

func startCaddy(caddyPath, caddyfile string, proxyPort int) error {
	args := []string{"start", "--config", caddyfile}
	direct := exec.Command(caddyPath, args...)
	output, err := direct.CombinedOutput()
	if err == nil {
		if len(output) > 0 {
			_, _ = os.Stdout.Write(output)
		}
		return nil
	}
	message := strings.TrimSpace(string(output))
	permissionFailure := strings.Contains(strings.ToLower(message), "permission denied") || strings.Contains(strings.ToLower(message), "operation not permitted")
	if proxyPort >= 1024 || !permissionFailure {
		return fmt.Errorf("start Caddy: %s", firstNonEmpty(message, err.Error()))
	}
	if !isTerminal(os.Stdin) {
		return fmt.Errorf("proxy port %d needs elevation; run `lns start` once in an interactive terminal", proxyPort)
	}
	sudoPath, lookupErr := exec.LookPath("sudo")
	if lookupErr != nil {
		return fmt.Errorf("proxy port %d needs elevation and sudo is unavailable", proxyPort)
	}
	fmt.Printf("Proxy port %d needs one-time elevation for this Caddy process.\n", proxyPort)
	elevated := exec.Command(sudoPath, append([]string{caddyPath}, args...)...)
	elevated.Stdin = os.Stdin
	elevated.Stdout = os.Stdout
	elevated.Stderr = os.Stderr
	if err := elevated.Run(); err != nil {
		return fmt.Errorf("start elevated Caddy: %w", err)
	}
	return nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return "unknown error"
}

func isTerminal(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return (info.Mode() & os.ModeCharDevice) != 0
}

func isTCPListening(addr string) bool {
	conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}
