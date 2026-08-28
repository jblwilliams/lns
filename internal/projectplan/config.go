package projectplan

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lns/internal/discovery"
	"lns/internal/models"
)

const configFilename = "lns.json"

type overrideConfig struct {
	Name     string                     `json:"name"`
	Prefix   string                     `json:"prefix,omitempty"`
	Services map[string]overrideService `json:"services"`
}

type overrideService struct {
	Root     string         `json:"root,omitempty"`
	Script   string         `json:"script,omitempty"`
	Command  []string       `json:"command,omitempty"`
	Profile  models.Profile `json:"profile,omitempty"`
	Hostname string         `json:"hostname,omitempty"`
}

func configExists(root string) bool {
	_, err := os.Stat(filepath.Join(root, configFilename))
	return err == nil
}

func loadConfigProject(root string) (models.Project, error) {
	path := filepath.Join(root, configFilename)
	file, err := os.Open(path)
	if err != nil {
		return models.Project{}, err
	}
	defer file.Close()
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	var config overrideConfig
	if err := decoder.Decode(&config); err != nil {
		return models.Project{}, fmt.Errorf("decode %s: %w", path, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return models.Project{}, fmt.Errorf("decode %s: expected one JSON object", path)
	}
	config.Name = discovery.NormalizeName(config.Name)
	config.Prefix = discovery.NormalizeName(config.Prefix)
	if config.Name == "" {
		return models.Project{}, fmt.Errorf("name is required")
	}
	if len(config.Services) == 0 {
		return models.Project{}, fmt.Errorf("services must define at least one runnable service")
	}
	names := make([]string, 0, len(config.Services))
	for name := range config.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	project := models.Project{Name: config.Name, Prefix: config.Prefix}
	seen := map[string]bool{}
	for _, inputName := range names {
		name := discovery.NormalizeName(inputName)
		if name == "" || seen[name] {
			return models.Project{}, fmt.Errorf("service name %q is empty or duplicates another normalized name", inputName)
		}
		seen[name] = true
		service := config.Services[inputName]
		service.Root = strings.TrimSpace(service.Root)
		if service.Root == "" {
			service.Root = "."
		}
		service.Root = filepath.Clean(service.Root)
		if filepath.IsAbs(service.Root) || service.Root == ".." || strings.HasPrefix(service.Root, ".."+string(filepath.Separator)) {
			return models.Project{}, fmt.Errorf("service %q root must stay inside the project", name)
		}
		service.Script = strings.TrimSpace(service.Script)
		for index := range service.Command {
			service.Command[index] = strings.TrimSpace(service.Command[index])
			if service.Command[index] == "" {
				return models.Project{}, fmt.Errorf("service %q command contains an empty argument", name)
			}
		}
		if (service.Script == "") == (len(service.Command) == 0) {
			return models.Project{}, fmt.Errorf("service %q must set exactly one of script or command", name)
		}
		if service.Profile == "" {
			if detected, ok := discovery.InspectServiceRoot(root, service.Root); ok {
				service.Profile = detected.Profile
			}
			if service.Profile == "" {
				service.Profile = models.ProfileStandard
			}
		}
		if service.Profile != models.ProfileHMR && service.Profile != models.ProfileStandard {
			return models.Project{}, fmt.Errorf("service %q profile must be hmr or standard", name)
		}
		hostname, err := localHostname(service.Hostname)
		if err != nil {
			return models.Project{}, fmt.Errorf("service %q hostname: %w", name, err)
		}
		project.Services = append(project.Services, models.Service{
			Name: name, Root: service.Root, Script: service.Script, Command: service.Command,
			Profile: service.Profile, Hostname: hostname, Status: models.StatusResolved,
		})
	}
	return project, nil
}

func localHostname(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(value, ".")))
	if value == "" {
		return "", nil
	}
	if !strings.HasSuffix(value, ".localhost") || strings.ContainsAny(value, "/:\\ ") {
		return "", fmt.Errorf("must be a hostname ending in .localhost")
	}
	return value, nil
}
