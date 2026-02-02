package config

import (
	"os"
	"path/filepath"
)

func GetConfigDir() string {
	homeDir, _ := os.UserHomeDir()
	if homeDir == "" {
		return ".lns"
	}

	return filepath.Join(homeDir, ".lns")
}

func GetRegistryPath() string {
	return filepath.Join(GetConfigDir(), "registry.json")
}

func GetCaddyConfigDir() string {
	return filepath.Join(GetConfigDir(), "projects")
}

func GetGlobalCaddyfilePath() string {
	return filepath.Join(GetConfigDir(), "Caddyfile")
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
