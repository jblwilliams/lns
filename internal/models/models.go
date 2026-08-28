package models

type Profile string

const (
	ProfileHMR      Profile = "hmr"
	ProfileStandard Profile = "standard"
)

type ServiceStatus string

const (
	StatusResolved   ServiceStatus = "resolved"
	StatusUnresolved ServiceStatus = "unresolved"
)

type Service struct {
	Name     string        `json:"name"`
	Root     string        `json:"root,omitempty"`
	Port     int           `json:"port,omitempty"`
	Script   string        `json:"script,omitempty"`
	Command  []string      `json:"command,omitempty"`
	Profile  Profile       `json:"profile,omitempty"`
	Hostname string        `json:"hostname,omitempty"`
	Status   ServiceStatus `json:"status,omitempty"`
}

func (s Service) EffectiveProfile() Profile {
	if s.Profile == "" {
		return ProfileStandard
	}
	return s.Profile
}

func (s Service) EffectiveStatus() ServiceStatus {
	if s.Status != "" {
		return s.Status
	}
	if s.Root != "" && s.CanRun() && s.Profile != "" {
		return StatusResolved
	}
	return StatusUnresolved
}

func (s Service) IsResolved() bool {
	return s.EffectiveStatus() == StatusResolved
}

func (s Service) CanRun() bool {
	return s.Script != "" || len(s.Command) > 0
}

type Project struct {
	Name     string    `json:"name"`
	Prefix   string    `json:"prefix,omitempty"`
	Services []Service `json:"services"`
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
	if len(p.Services) == 1 {
		return p.GetPrefix() + ".localhost"
	}
	return p.GetPrefix() + "-" + service.Name + ".localhost"
}
