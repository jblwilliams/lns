package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"lns/internal/config"
	"lns/internal/models"
)

type Manager struct {
	Registry *models.Registry
}

func NewManager() (*Manager, error) {
	reg, err := Load()
	if err != nil {
		return nil, err
	}
	return &Manager{Registry: reg}, nil
}

func Load() (*models.Registry, error) {
	path := config.GetRegistryPath()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return models.NewRegistry(), nil
		}
		return nil, err
	}

	var reg models.Registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return nil, fmt.Errorf("decode %s: %w", path, err)
	}

	normalizeRegistry(&reg)
	return &reg, nil
}

func (m *Manager) Save() error {
	if err := config.EnsureConfigDirs(); err != nil {
		return err
	}

	normalizeRegistry(m.Registry)

	data, err := json.MarshalIndent(m.Registry, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(config.GetRegistryPath(), append(data, '\n'), 0644)
}

func (m *Manager) GetProject(name string) (*models.Project, bool) {
	project, exists := m.Registry.Projects[name]
	if !exists {
		return nil, false
	}
	return &project, true
}

func (m *Manager) ListProjects() []models.Project {
	projects := make([]models.Project, 0, len(m.Registry.Projects))
	for _, project := range m.Registry.Projects {
		projects = append(projects, project)
	}
	return projects
}

func (m *Manager) CheckPortConflict(port int) string {
	return m.Registry.PortAssignments[port]
}

func (m *Manager) CheckHostnameConflict(hostname string) string {
	return m.Registry.HostnameAssignments[strings.ToLower(strings.TrimSpace(hostname))]
}

func (m *Manager) GetAllPortAssignments() map[int]string {
	result := make(map[int]string, len(m.Registry.PortAssignments))
	for port, owner := range m.Registry.PortAssignments {
		result[port] = owner
	}
	return result
}

func (m *Manager) RemoveProject(name string) error {
	if _, exists := m.Registry.Projects[name]; !exists {
		return fmt.Errorf("project %q not found", name)
	}

	delete(m.Registry.Projects, name)
	rebuildAssignments(m.Registry)
	return m.Save()
}

func (m *Manager) UpsertProject(project models.Project) error {
	if project.Name == "" {
		return fmt.Errorf("project name is required")
	}

	normalizeProject(&project)
	m.Registry.Projects[project.Name] = project
	rebuildAssignments(m.Registry)
	return m.Save()
}

func (m *Manager) ValidateProjectConflicts(project models.Project) []error {
	normalizeProject(&project)

	snapshot := cloneRegistry(m.Registry)
	delete(snapshot.Projects, project.Name)
	rebuildAssignments(snapshot)

	var errs []error
	seenPorts := map[int]string{}
	seenHostnames := map[string]string{}
	for _, service := range project.Services {
		if !service.IsResolved() {
			errs = append(errs, fmt.Errorf("service %q is unresolved", service.Name))
			continue
		}

		if owner := seenPorts[service.Port]; owner != "" {
			errs = append(errs, fmt.Errorf("port %d is already owned by %s", service.Port, owner))
		} else {
			seenPorts[service.Port] = project.Name + ":" + service.Name
		}
		if owner := snapshot.PortAssignments[service.Port]; owner != "" {
			errs = append(errs, fmt.Errorf("port %d is already owned by %s", service.Port, owner))
		}

		hostname := strings.ToLower(project.GetServiceHostname(service))
		if owner := seenHostnames[hostname]; owner != "" {
			errs = append(errs, fmt.Errorf("hostname %q is already owned by %s", hostname, owner))
		} else {
			seenHostnames[hostname] = project.Name + ":" + service.Name
		}
		if owner := snapshot.HostnameAssignments[hostname]; owner != "" {
			errs = append(errs, fmt.Errorf("hostname %q is already owned by %s", hostname, owner))
		}
	}

	return errs
}

type PortInfo struct {
	Port    int
	Project string
	Service string
}

func (m *Manager) GetPortList() []PortInfo {
	ports := make([]PortInfo, 0, len(m.Registry.PortAssignments))
	for port, owner := range m.Registry.PortAssignments {
		project, service := parseOwner(owner)
		ports = append(ports, PortInfo{
			Port:    port,
			Project: project,
			Service: service,
		})
	}

	sort.Slice(ports, func(i, j int) bool {
		return ports[i].Port < ports[j].Port
	})

	return ports
}

func parseOwner(owner string) (project, service string) {
	for i, c := range owner {
		if c == ':' {
			return owner[:i], owner[i+1:]
		}
	}
	return owner, ""
}

func normalizeRegistry(reg *models.Registry) {
	if reg.Version == "" {
		reg.Version = "2.0"
	}
	if reg.Projects == nil {
		reg.Projects = make(map[string]models.Project)
	}
	if reg.PortAssignments == nil {
		reg.PortAssignments = make(map[int]string)
	}
	if reg.HostnameAssignments == nil {
		reg.HostnameAssignments = make(map[string]string)
	}

	for name, project := range reg.Projects {
		if project.Name == "" {
			project.Name = name
		}
		normalizeProject(&project)
		reg.Projects[name] = project
	}

	rebuildAssignments(reg)
}

func normalizeProject(project *models.Project) {
	sort.Slice(project.Services, func(i, j int) bool {
		return project.Services[i].Name < project.Services[j].Name
	})
}

func rebuildAssignments(reg *models.Registry) {
	reg.PortAssignments = make(map[int]string)
	reg.HostnameAssignments = make(map[string]string)

	projectNames := make([]string, 0, len(reg.Projects))
	for name := range reg.Projects {
		projectNames = append(projectNames, name)
	}
	sort.Strings(projectNames)

	for _, projectName := range projectNames {
		project := reg.Projects[projectName]

		for _, service := range project.Services {
			if !service.IsResolved() {
				continue
			}

			reg.PortAssignments[service.Port] = projectName + ":" + service.Name
			reg.HostnameAssignments[strings.ToLower(project.GetServiceHostname(service))] = projectName + ":" + service.Name
		}
	}
}

func cloneRegistry(reg *models.Registry) *models.Registry {
	clone := models.NewRegistry()
	clone.Version = reg.Version
	for name, project := range reg.Projects {
		projectClone := project
		projectClone.Services = append([]models.Service(nil), project.Services...)
		clone.Projects[name] = projectClone
	}
	rebuildAssignments(clone)
	return clone
}
