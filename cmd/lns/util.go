package main

import (
	"fmt"
	"net"
	"os"
	"time"

	"lns/internal/config"
	"lns/internal/models"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
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

func formatServiceURL(hostname string, httpPort int) string {
	return formatServiceURLWithTLS(hostname, httpPort, false)
}

func formatServiceURLWithTLS(hostname string, proxyPort int, https bool) string {
	scheme := "http"
	defaultPort := 80
	if https {
		scheme = "https"
		defaultPort = 443
	}
	if proxyPort == defaultPort {
		return fmt.Sprintf("%s://%s/", scheme, hostname)
	}
	return fmt.Sprintf("%s://%s:%d/", scheme, hostname, proxyPort)
}

func loadSettingsOrDefault() config.Settings {
	settings, err := config.LoadSettings()
	if err != nil {
		return config.DefaultSettings()
	}
	return settings
}

func resolvedServiceURL(project *models.Project, service models.Service, proxyPort int, https bool) string {
	return formatServiceURLWithTLS(project.GetServiceHostname(service), proxyPort, https)
}
