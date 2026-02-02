package registry

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

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

	if reg.Projects == nil {
		reg.Projects = make(map[string]models.Project)
	}
	if reg.PortAssignments == nil {
		reg.PortAssignments = make(map[int]string)
	}

	return &reg, nil
}

func (m *Manager) Save() error {
	if err := config.EnsureConfigDirs(); err != nil {
		return err
	}

	data, err := json.MarshalIndent(m.Registry, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(config.GetRegistryPath(), data, 0644)
}

func (m *Manager) AddProject(name, path, prefix, dockerNetwork string) (*models.Project, error) {
	if _, exists := m.Registry.Projects[name]; exists {
		return nil, fmt.Errorf("project '%s' already exists", name)
	}

	project := models.Project{
		Name:          name,
		Path:          path,
		Prefix:        prefix,
		DockerNetwork: dockerNetwork,
		Services:      []models.Service{},
	}

	m.Registry.Projects[name] = project

	if err := m.Save(); err != nil {
		return nil, err
	}

	return &project, nil
}

func (m *Manager) RemoveProject(name string) error {
	project, exists := m.Registry.Projects[name]
	if !exists {
		return fmt.Errorf("project '%s' not found", name)
	}

	for _, service := range project.Services {
		delete(m.Registry.PortAssignments, service.Port)
	}

	delete(m.Registry.Projects, name)
	return m.Save()
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
	for _, p := range m.Registry.Projects {
		projects = append(projects, p)
	}
	return projects
}

func (m *Manager) AddService(projectName, serviceName string, framework models.Framework, port int, hostname string, docker bool, containerName, pathPrefix string) (*models.Service, int, error) {
	project, exists := m.Registry.Projects[projectName]
	if !exists {
		return nil, 0, fmt.Errorf("project '%s' not found", projectName)
	}

	for _, s := range project.Services {
		if s.Name == serviceName {
			return nil, 0, fmt.Errorf("service '%s' already exists in project '%s'", serviceName, projectName)
		}
	}

	assignedPort := port
	if port == 0 {
		assignedPort = m.FindAvailablePort(framework)
	} else {
		if owner := m.CheckPortConflict(port); owner != "" {
			return nil, 0, fmt.Errorf("port %d is already in use by %s", port, owner)
		}
	}

	service := models.Service{
		Name:          serviceName,
		Port:          assignedPort,
		Framework:     framework,
		Hostname:      hostname,
		Docker:        docker,
		ContainerName: containerName,
		PathPrefix:    pathPrefix,
	}

	m.Registry.PortAssignments[assignedPort] = projectName + ":" + serviceName
	project.Services = append(project.Services, service)
	m.Registry.Projects[projectName] = project

	if err := m.Save(); err != nil {
		return nil, 0, err
	}

	return &service, assignedPort, nil
}

func (m *Manager) RemoveService(projectName, serviceName string) error {
	project, exists := m.Registry.Projects[projectName]
	if !exists {
		return fmt.Errorf("project '%s' not found", projectName)
	}

	found := false
	newServices := make([]models.Service, 0, len(project.Services))
	for _, s := range project.Services {
		if s.Name == serviceName {
			delete(m.Registry.PortAssignments, s.Port)
			found = true
		} else {
			newServices = append(newServices, s)
		}
	}

	if !found {
		return fmt.Errorf("service '%s' not found in project '%s'", serviceName, projectName)
	}

	project.Services = newServices
	m.Registry.Projects[projectName] = project

	return m.Save()
}

func (m *Manager) CheckPortConflict(port int) string {
	return m.Registry.PortAssignments[port]
}

func (m *Manager) IsPortAvailable(port int) bool {
	_, exists := m.Registry.PortAssignments[port]
	return !exists
}

func (m *Manager) FindAvailablePort(framework models.Framework) int {
	info := models.GetFrameworkInfo(framework)

	for port := info.PortStart; port <= info.PortEnd; port++ {
		if m.IsPortAvailable(port) {
			return port
		}
	}

	for port := 9000; port <= 9999; port++ {
		if m.IsPortAvailable(port) {
			return port
		}
	}

	return 9999
}

func (m *Manager) GetAllPortAssignments() map[int]string {
	result := make(map[int]string)
	for k, v := range m.Registry.PortAssignments {
		result[k] = v
	}
	return result
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

func (m *Manager) SuggestPort(framework models.Framework) (int, int, int) {
	info := models.GetFrameworkInfo(framework)
	port := m.FindAvailablePort(framework)
	return port, info.PortStart, info.PortEnd
}

func (m *Manager) UpdateProjectPath(name, path string) error {
	project, exists := m.Registry.Projects[name]
	if !exists {
		return fmt.Errorf("project '%s' not found", name)
	}

	project.Path = path
	m.Registry.Projects[name] = project
	return m.Save()
}
