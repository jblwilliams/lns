// Package caddy renders the shared proxy from active, process-owned leases.
package caddy

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"lns/internal/config"
	"lns/internal/devruntime"
	"lns/internal/models"
	"lns/internal/state"
)

func RegenerateAllCaddyfiles() ([]string, error) {
	leases, err := devruntime.NewStore().Load()
	if err != nil {
		return nil, err
	}
	runtimePath, err := WriteRuntimeCaddyfile(leases)
	if err != nil {
		return nil, err
	}
	globalPath, err := WriteGlobalCaddyfile()
	if err != nil {
		return []string{runtimePath}, err
	}
	return []string{runtimePath, globalPath}, nil
}

func GenerateRuntimeCaddyfile(leases []devruntime.Lease, proxyPort int, https bool) string {
	var output strings.Builder
	output.WriteString("# Active development processes\n\n")
	for _, lease := range leases {
		scheme := "http"
		if https {
			scheme = "https"
		}
		fmt.Fprintf(&output, "%s://%s:%d {\n", scheme, lease.Hostname, proxyPort)
		if https {
			output.WriteString("\ttls internal\n")
		}
		fmt.Fprintf(&output, "\treverse_proxy 127.0.0.1:%d", lease.Port)
		if lease.Profile == models.ProfileHMR {
			output.WriteString(" {\n\t\theader_up Host {host}\n\t\theader_up X-Real-IP {remote_host}\n\t}\n")
		} else {
			output.WriteString("\n")
		}
		output.WriteString("}\n\n")
	}
	return output.String()
}

func WriteRuntimeCaddyfile(leases []devruntime.Lease) (string, error) {
	path := config.GetRuntimeCaddyfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	content := GenerateRuntimeCaddyfile(leases, config.DefaultHTTPPort, false)
	if err := state.WriteFileAtomic(path, []byte(content), 0644); err != nil {
		return "", err
	}
	return path, nil
}

func GenerateGlobalCaddyfile(proxyPort int, adminAddr string) string {
	return fmt.Sprintf(`# LNS shared local proxy
{
	admin %s
	auto_https off
	http_port %d
}

import %s
`, adminAddr, proxyPort, config.GetRuntimeCaddyfilePath())
}

func WriteGlobalCaddyfile() (string, error) {
	settings, err := config.LoadSettings()
	if err != nil {
		return "", err
	}
	path := config.GetGlobalCaddyfilePath()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return "", err
	}
	if err := state.WriteFileAtomic(path, []byte(GenerateGlobalCaddyfile(config.DefaultHTTPPort, settings.AdminAddr)), 0644); err != nil {
		return "", err
	}
	return path, nil
}
