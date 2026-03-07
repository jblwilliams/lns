package projectconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lns/internal/models"
)

const Filename = "lns.json"

type Config struct {
	Name          string             `json:"name"`
	Prefix        string             `json:"prefix,omitempty"`
	Services      map[string]Service `json:"services"`
	DockerNetwork string             `json:"docker_network,omitempty"`
}

type Service struct {
	Root          string               `json:"root,omitempty"`
	Port          int                  `json:"port,omitempty"`
	Profile       models.Profile       `json:"profile,omitempty"`
	Hostname      string               `json:"hostname,omitempty"`
	Source        models.ServiceSource `json:"source,omitempty"`
	Status        models.ServiceStatus `json:"status,omitempty"`
	Docker        bool                 `json:"docker,omitempty"`
	ContainerName string               `json:"container_name,omitempty"`
}

type ValidationError struct {
	Service string
	Field   string
	Message string
}

func (e ValidationError) Error() string {
	parts := []string{}
	if e.Service != "" {
		parts = append(parts, fmt.Sprintf("service %q", e.Service))
	}
	if e.Field != "" {
		parts = append(parts, e.Field)
	}
	if len(parts) == 0 {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", strings.Join(parts, " "), e.Message)
}

func Path(root string) string {
	return filepath.Join(root, Filename)
}

func Exists(root string) bool {
	_, err := os.Stat(Path(root))
	return err == nil
}

func Load(root string) (*Config, error) {
	path := Path(root)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	cfg.Normalize()
	return &cfg, nil
}

func Save(root string, cfg *Config) error {
	cfg.Normalize()

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(Path(root), append(data, '\n'), 0644)
}

func (c *Config) Normalize() {
	c.Name = strings.TrimSpace(c.Name)
	c.Prefix = strings.TrimSpace(c.Prefix)
	if c.Services == nil {
		c.Services = map[string]Service{}
	}

	normalized := make(map[string]Service, len(c.Services))
	for name, service := range c.Services {
		key := strings.TrimSpace(name)
		if key == "" {
			continue
		}

		service.Root = cleanRoot(service.Root)
		if service.Source == "" {
			service.Source = models.SourceConfig
		}
		if service.Status == "" {
			if service.Root != "" && service.Port > 0 && service.Profile != "" {
				service.Status = models.StatusResolved
			} else {
				service.Status = models.StatusUnresolved
			}
		}
		normalized[key] = service
	}
	c.Services = normalized
}

func (c *Config) SortedServiceNames() []string {
	names := make([]string, 0, len(c.Services))
	for name := range c.Services {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (c *Config) ToProject(root string) models.Project {
	project := models.Project{
		Name:          c.Name,
		Prefix:        c.Prefix,
		Path:          root,
		DockerNetwork: c.DockerNetwork,
		Services:      make([]models.Service, 0, len(c.Services)),
	}

	for _, name := range c.SortedServiceNames() {
		service := c.Services[name]
		project.Services = append(project.Services, models.Service{
			Name:          name,
			Root:          service.Root,
			Port:          service.Port,
			Profile:       service.Profile,
			Hostname:      service.Hostname,
			Source:        service.Source,
			Status:        service.Status,
			Docker:        service.Docker,
			ContainerName: service.ContainerName,
		})
	}

	return project
}

func Validate(cfg *Config) []ValidationError {
	cfg.Normalize()

	var errs []ValidationError
	if cfg.Name == "" {
		errs = append(errs, ValidationError{Field: "name", Message: "is required"})
	}
	if len(cfg.Services) == 0 {
		errs = append(errs, ValidationError{Field: "services", Message: "must define at least one service"})
		return errs
	}

	project := models.Project{
		Name:     cfg.Name,
		Prefix:   cfg.Prefix,
		Services: make([]models.Service, 0, len(cfg.Services)),
	}

	for _, name := range cfg.SortedServiceNames() {
		service := cfg.Services[name]
		status := service.Status
		if status == "" {
			status = models.StatusUnresolved
		}

		switch status {
		case models.StatusResolved:
			if service.Root == "" {
				errs = append(errs, ValidationError{Service: name, Field: "root", Message: "is required"})
			}
			if service.Port < 1 || service.Port > 65535 {
				errs = append(errs, ValidationError{Service: name, Field: "port", Message: "must be between 1 and 65535"})
			}
			if !isValidProfile(service.Profile) {
				errs = append(errs, ValidationError{Service: name, Field: "profile", Message: "must be one of hmr or standard"})
			}
		case models.StatusUnresolved:
			// Unresolved services are allowed in the manifest, but sync will block on them.
		default:
			errs = append(errs, ValidationError{Service: name, Field: "status", Message: "must be resolved or unresolved"})
		}

		if service.Hostname != "" {
			normalized, err := ValidateExplicitHostname(service.Hostname)
			if err != nil {
				errs = append(errs, ValidationError{Service: name, Field: "hostname", Message: err.Error()})
			} else {
				service.Hostname = normalized
			}
		}

		project.Services = append(project.Services, models.Service{
			Name:     name,
			Hostname: service.Hostname,
		})
	}

	if project.Name == "" {
		return errs
	}

	resolvedHostnames := map[string]string{}
	for _, service := range project.Services {
		hostname := strings.ToLower(project.GetServiceHostname(service))
		if owner, exists := resolvedHostnames[hostname]; exists {
			errs = append(errs, ValidationError{
				Service: service.Name,
				Field:   "hostname",
				Message: fmt.Sprintf("resolves to the same hostname as service %q", owner),
			})
			continue
		}
		resolvedHostnames[hostname] = service.Name
	}

	return errs
}

func cleanRoot(root string) string {
	root = strings.TrimSpace(root)
	switch root {
	case "", ".":
		return "."
	default:
		return filepath.Clean(root)
	}
}

func isValidProfile(profile models.Profile) bool {
	switch profile {
	case models.ProfileHMR, models.ProfileStandard:
		return true
	default:
		return false
	}
}
