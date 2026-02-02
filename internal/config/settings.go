package config

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
)

const (
	DefaultHTTPPort  = 8888
	DefaultAdminAddr = "127.0.0.1:20190"
)

type Settings struct {
	HTTPPort  int    `json:"http_port"`
	AdminAddr string `json:"admin_addr"`
}

func GetSettingsPath() string {
	return filepath.Join(GetConfigDir(), "settings.json")
}

func DefaultSettings() Settings {
	return Settings{
		HTTPPort:  DefaultHTTPPort,
		AdminAddr: DefaultAdminAddr,
	}
}

func LoadSettings() (Settings, error) {
	path := GetSettingsPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultSettings(), nil
		}
		return Settings{}, err
	}

	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("decode %s: %w", path, err)
	}

	settings.applyDefaultsAndValidate()
	return settings, nil
}

func SaveSettings(settings Settings) error {
	if err := EnsureConfigDirs(); err != nil {
		return err
	}

	settings.applyDefaultsAndValidate()

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(GetSettingsPath(), data, 0644)
}

func (s *Settings) applyDefaultsAndValidate() {
	if s.HTTPPort < 1 || s.HTTPPort > 65535 {
		s.HTTPPort = DefaultHTTPPort
	}

	if s.AdminAddr == "" {
		s.AdminAddr = DefaultAdminAddr
	}

	if _, _, err := net.SplitHostPort(s.AdminAddr); err != nil {
		s.AdminAddr = DefaultAdminAddr
	}
}
