package models

import "strconv"

type Profile string

const (
	ProfileHMR      Profile = "hmr"
	ProfileStandard Profile = "standard"
)

func ValidProfiles() []string {
	return []string{
		string(ProfileHMR),
		string(ProfileStandard),
	}
}

type ServiceSource string

const (
	SourceConfig   ServiceSource = "config"
	SourceManual   ServiceSource = "manual"
	SourceDetected ServiceSource = "detected"
)

type ServiceStatus string

const (
	StatusResolved   ServiceStatus = "resolved"
	StatusUnresolved ServiceStatus = "unresolved"
)

type Service struct {
	Name          string        `json:"name"`
	Root          string        `json:"root,omitempty"`
	Port          int           `json:"port,omitempty"`
	Profile       Profile       `json:"profile,omitempty"`
	Hostname      string        `json:"hostname,omitempty"`
	Source        ServiceSource `json:"source,omitempty"`
	Status        ServiceStatus `json:"status,omitempty"`
	Docker        bool          `json:"docker,omitempty"`
	ContainerName string        `json:"container_name,omitempty"`
}

func (s Service) EffectiveProfile() Profile {
	if s.Profile == "" {
		return ProfileStandard
	}
	return s.Profile
}

func (s Service) EffectiveSource() ServiceSource {
	if s.Source == "" {
		return SourceConfig
	}
	return s.Source
}

func (s Service) EffectiveStatus() ServiceStatus {
	if s.Status != "" {
		return s.Status
	}
	if s.Root != "" && s.Port > 0 && s.Profile != "" {
		return StatusResolved
	}
	return StatusUnresolved
}

func (s Service) IsResolved() bool {
	return s.EffectiveStatus() == StatusResolved
}

func (s *Service) GetHostname(projectPrefix string) string {
	if s.Hostname != "" {
		return s.Hostname
	}
	return projectPrefix + "-" + s.Name + ".localhost"
}

func (s *Service) GetUpstream() string {
	return "localhost:" + strconv.Itoa(s.Port)
}

func (s *Service) GetDockerUpstream() string {
	if s.Docker && s.ContainerName != "" {
		return s.ContainerName + ":" + strconv.Itoa(s.Port)
	}
	return s.GetUpstream()
}

type Project struct {
	Name          string    `json:"name"`
	Prefix        string    `json:"prefix,omitempty"`
	Path          string    `json:"path,omitempty"`
	Services      []Service `json:"services"`
	DockerNetwork string    `json:"docker_network,omitempty"`
}

func (p *Project) GetPrefix() string {
	if p.Prefix != "" {
		return p.Prefix
	}
	return p.Name
}

func (p *Project) GetServiceHostname(service Service) string {
	if service.Hostname != "" {
		return service.Hostname
	}

	prefix := p.GetPrefix()
	if len(p.Services) == 1 {
		return prefix + ".localhost"
	}

	return prefix + "-" + service.Name + ".localhost"
}

type Registry struct {
	Version             string             `json:"version"`
	Projects            map[string]Project `json:"projects"`
	PortAssignments     map[int]string     `json:"port_assignments"`
	HostnameAssignments map[string]string  `json:"hostname_assignments,omitempty"`
}

func NewRegistry() *Registry {
	return &Registry{
		Version:             "2.0",
		Projects:            make(map[string]Project),
		PortAssignments:     make(map[int]string),
		HostnameAssignments: make(map[string]string),
	}
}
