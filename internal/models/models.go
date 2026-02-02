package models

import "strconv"

type Framework string

const (
	FrameworkNextJS   Framework = "nextjs"
	FrameworkNuxt     Framework = "nuxt"
	FrameworkVite     Framework = "vite"
	FrameworkVueCLI   Framework = "vue-cli"
	FrameworkReactCRA Framework = "react-cra"
	FrameworkFastAPI  Framework = "fastapi"
	FrameworkDjango   Framework = "django"
	FrameworkExpress  Framework = "express"
	FrameworkRails    Framework = "rails"
	FrameworkFlask    Framework = "flask"
	FrameworkGeneric  Framework = "generic"
)

type FrameworkInfo struct {
	DefaultPort int
	PortStart   int
	PortEnd     int
	NeedsHMR    bool
}

var Frameworks = map[Framework]FrameworkInfo{
	FrameworkNextJS:   {DefaultPort: 3000, PortStart: 3000, PortEnd: 3099, NeedsHMR: true},
	FrameworkNuxt:     {DefaultPort: 3000, PortStart: 3100, PortEnd: 3199, NeedsHMR: true},
	FrameworkVite:     {DefaultPort: 5173, PortStart: 5173, PortEnd: 5272, NeedsHMR: true},
	FrameworkVueCLI:   {DefaultPort: 8080, PortStart: 8080, PortEnd: 8099, NeedsHMR: true},
	FrameworkReactCRA: {DefaultPort: 3000, PortStart: 3200, PortEnd: 3299, NeedsHMR: true},
	FrameworkFastAPI:  {DefaultPort: 8000, PortStart: 8000, PortEnd: 8079, NeedsHMR: false},
	FrameworkDjango:   {DefaultPort: 8000, PortStart: 8100, PortEnd: 8179, NeedsHMR: false},
	FrameworkExpress:  {DefaultPort: 3000, PortStart: 4000, PortEnd: 4099, NeedsHMR: false},
	FrameworkRails:    {DefaultPort: 3000, PortStart: 4100, PortEnd: 4199, NeedsHMR: false},
	FrameworkFlask:    {DefaultPort: 5000, PortStart: 5000, PortEnd: 5099, NeedsHMR: false},
	FrameworkGeneric:  {DefaultPort: 8080, PortStart: 9000, PortEnd: 9099, NeedsHMR: false},
}

func GetFrameworkInfo(f Framework) FrameworkInfo {
	if info, ok := Frameworks[f]; ok {
		return info
	}
	return Frameworks[FrameworkGeneric]
}

func ValidFrameworks() []string {
	return []string{
		string(FrameworkNextJS),
		string(FrameworkNuxt),
		string(FrameworkVite),
		string(FrameworkVueCLI),
		string(FrameworkReactCRA),
		string(FrameworkFastAPI),
		string(FrameworkDjango),
		string(FrameworkExpress),
		string(FrameworkRails),
		string(FrameworkFlask),
		string(FrameworkGeneric),
	}
}

type Service struct {
	Name          string    `json:"name" yaml:"name"`
	Port          int       `json:"port" yaml:"port"`
	Framework     Framework `json:"framework" yaml:"framework"`
	Hostname      string    `json:"hostname,omitempty" yaml:"hostname,omitempty"`
	Docker        bool      `json:"docker,omitempty" yaml:"docker,omitempty"`
	ContainerName string    `json:"container_name,omitempty" yaml:"container_name,omitempty"`
	PathPrefix    string    `json:"path_prefix,omitempty" yaml:"path_prefix,omitempty"`
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
	Name          string    `json:"name" yaml:"name"`
	Prefix        string    `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Path          string    `json:"path,omitempty" yaml:"path,omitempty"`
	Services      []Service `json:"services" yaml:"services"`
	DockerNetwork string    `json:"docker_network,omitempty" yaml:"docker_network,omitempty"`
}

func (p *Project) GetPrefix() string {
	if p.Prefix != "" {
		return p.Prefix
	}
	return p.Name
}

type Registry struct {
	Version         string             `json:"version"`
	Projects        map[string]Project `json:"projects"`
	PortAssignments map[int]string     `json:"port_assignments"`
}

func NewRegistry() *Registry {
	return &Registry{
		Version:         "1.0",
		Projects:        make(map[string]Project),
		PortAssignments: make(map[int]string),
	}
}
