package config

import (
	"os"
	"path/filepath"
)

const (
	DefaultHTTPPort = 80
	CaddyAdminAddr  = "127.0.0.1:20190"
)

func GetConfigDir() string {
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		return ".lns"
	}

	return filepath.Join(homeDir, ".lns")
}

func GetCaddyConfigDir() string {
	return filepath.Join(GetConfigDir(), "projects")
}

func GetGlobalCaddyfilePath() string {
	return filepath.Join(GetConfigDir(), "Caddyfile")
}

func GetRuntimePath() string {
	return filepath.Join(GetConfigDir(), "runtime.json")
}

func GetRuntimeCaddyfilePath() string {
	return filepath.Join(GetCaddyConfigDir(), "00-runtime.caddy")
}

func EnsureConfigDirs() error {
	dirs := []string{
		GetConfigDir(),
		GetCaddyConfigDir(),
	}

	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return err
		}
	}

	return nil
}
