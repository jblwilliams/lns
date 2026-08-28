// Package projectplan builds the complete, read-only description of what LNS
// intends to run. Planning never reserves ports, mutates runtime state, or
// writes into the inspected repository.
package projectplan

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"lns/internal/devrun"
	"lns/internal/discovery"
	"lns/internal/models"
)

const SchemaVersion = 1

type Source string

const (
	SourceConfig     Source = "config"
	SourceDiscovered Source = "discovered"
)

type PortStrategy string

const (
	PortDynamic    PortStrategy = "dynamic"
	PortUnresolved PortStrategy = "unresolved"
)

type ServiceState string

const (
	StateManaged    ServiceState = "managed"
	StateUnresolved ServiceState = "unresolved"
)

type Route struct {
	Scheme string
	Port   int
}

type Plan struct {
	SchemaVersion int       `json:"schema_version"`
	Project       Project   `json:"project"`
	Services      []Service `json:"services"`
	Warnings      []Warning `json:"warnings"`
}

type Project struct {
	Name     string `json:"name"`
	Root     string `json:"root"`
	Source   Source `json:"source"`
	Worktree string `json:"worktree,omitempty"`
}

type Service struct {
	Name        string               `json:"name"`
	Root        string               `json:"root"`
	RunRoot     string               `json:"run_root,omitempty"`
	Script      string               `json:"script,omitempty"`
	Command     []string             `json:"command,omitempty"`
	State       ServiceState         `json:"state"`
	Default     bool                 `json:"default"`
	Listeners   []Listener           `json:"listeners"`
	Environment []EnvironmentBinding `json:"environment,omitempty"`
	Evidence    []string             `json:"evidence"`
}

type Listener struct {
	Name        string         `json:"name"`
	Profile     models.Profile `json:"profile"`
	Public      bool           `json:"public"`
	Hostname    string         `json:"hostname,omitempty"`
	URL         string         `json:"url,omitempty"`
	Environment []string       `json:"environment"`
	Port        Port           `json:"port"`
}

type BindingKind string

const (
	BindingPort BindingKind = "port"
	BindingURL  BindingKind = "url"
)

type EnvironmentBinding struct {
	Name     string         `json:"name"`
	Kind     BindingKind    `json:"kind"`
	Target   EndpointRef    `json:"target"`
	Scheme   string         `json:"scheme,omitempty"`
	Path     string         `json:"path,omitempty"`
	Evidence string         `json:"evidence"`
	Network  BindingNetwork `json:"network"`
}

type EndpointRef struct {
	Service  string `json:"service"`
	Listener string `json:"listener"`
}

type BindingNetwork string

const (
	NetworkRoute    BindingNetwork = "route"
	NetworkLoopback BindingNetwork = "loopback"
)

type Port struct {
	Strategy PortStrategy   `json:"strategy"`
	Observed []ObservedPort `json:"observed,omitempty"`
}

type ObservedPort struct {
	Value    int    `json:"value"`
	Evidence string `json:"evidence"`
}

type Warning struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Recovery string `json:"recovery,omitempty"`
}

// Build returns a deterministic symbolic plan for root. An explicit lns.json
// is an override; without one, the plan is discovered entirely in memory.
func Build(root string, route Route) (Plan, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return Plan{}, fmt.Errorf("resolve project root: %w", err)
	}
	info, err := os.Stat(absRoot)
	if err != nil {
		return Plan{}, fmt.Errorf("inspect project root: %w", err)
	}
	if !info.IsDir() {
		return Plan{}, fmt.Errorf("project root is not a directory: %s", absRoot)
	}
	route, err = route.normalized()
	if err != nil {
		return Plan{}, err
	}

	name := ProjectName(absRoot)
	source := SourceDiscovered
	var compiled models.Project
	type evidence struct {
		all  []string
		port string
	}
	var evidenceByService map[string]evidence
	runRootByService := map[string]string{}
	if configExists(absRoot) {
		configured, err := loadConfigProject(absRoot)
		if err != nil {
			return Plan{}, fmt.Errorf("load %s: %w", configFilename, err)
		}
		name = configured.Name
		source = SourceConfig
		compiled = configured
	} else {
		detected := discovery.DetectServices(absRoot)
		wrappers := rootWrappers(absRoot, detected)
		evidenceByService = make(map[string]evidence, len(detected))
		compiled = models.Project{Name: name, Services: make([]models.Service, 0, len(detected))}
		for _, service := range detected {
			if len(detected) == 1 && service.Root == "." {
				service.Name = name
			}
			serviceEvidence := append([]string(nil), service.Evidence...)
			if wrapper, ok := wrappers[service.Name]; ok {
				service.Script = wrapper.Script
				runRootByService[service.Name] = "."
				serviceEvidence = append(serviceEvidence, "package.json script "+wrapper.Script+" (root wrapper)")
			}
			evidenceByService[service.Name] = evidence{
				all:  serviceEvidence,
				port: service.PortEvidence,
			}
			compiled.Services = append(compiled.Services, models.Service{
				Name:    service.Name,
				Root:    service.Root,
				Port:    service.Port,
				Script:  service.Script,
				Profile: service.Profile,
				Status:  service.Status,
			})
		}
	}
	defaults := defaultServices(compiled.Services, source)
	defaultCount := len(defaults)

	plan := Plan{
		SchemaVersion: SchemaVersion,
		Project: Project{
			Name:     name,
			Root:     absRoot,
			Source:   source,
			Worktree: devrun.DetectWorktreePrefix(absRoot),
		},
		Services: []Service{},
		Warnings: []Warning{},
	}
	for _, service := range compiled.Services {
		serviceEvidence := evidenceByService[service.Name]
		if source == SourceConfig {
			serviceEvidence = evidence{all: []string{configFilename}, port: configFilename}
		}
		serviceEvidence.all = stableStrings(serviceEvidence.all)
		state := serviceState(service)
		isDefault := defaults[service.Name]
		hostname := compiled.GetServiceHostname(service)
		if isDefault && defaultCount == 1 && service.Hostname == "" {
			hostname = compiled.GetPrefix() + ".localhost"
		}
		hostname = devrun.ApplyWorktreePrefix(hostname, plan.Project.Worktree)
		port := Port{Strategy: PortDynamic}
		if service.Port > 0 {
			port.Observed = []ObservedPort{{Value: service.Port, Evidence: serviceEvidence.port}}
		}
		switch state {
		case StateManaged:
		case StateUnresolved:
			port.Strategy = PortUnresolved
		default:
			panic(fmt.Sprintf("unhandled service state %q", state))
		}
		plan.Services = append(plan.Services, Service{
			Name:    service.Name,
			Root:    service.Root,
			RunRoot: runRootByService[service.Name],
			Script:  service.Script,
			Command: append([]string(nil), service.Command...),
			State:   state,
			Default: isDefault,
			Listeners: []Listener{{
				Name: "http", Profile: service.EffectiveProfile(), Public: true,
				Hostname: hostname, URL: route.url(hostname),
				Environment: publicPortEnvironment(absRoot, service), Port: port,
			}},
			Evidence: serviceEvidence.all,
		})
		if state == StateUnresolved && source == SourceConfig {
			plan.Warnings = append(plan.Warnings, Warning{
				Code:     "unresolved-service",
				Message:  fmt.Sprintf("service %q is unresolved and will not run", service.Name),
				Recovery: "edit lns.json and set a valid profile plus script, command, or port",
			})
		}
	}

	sort.Slice(plan.Services, func(i, j int) bool { return plan.Services[i].Name < plan.Services[j].Name })
	inferListeners(absRoot, &plan)
	inferEnvironment(absRoot, route, &plan)
	managed, selectedByDefault := 0, 0
	for _, service := range plan.Services {
		if service.State == StateManaged {
			managed++
		}
		if service.Default {
			selectedByDefault++
		}
	}
	if managed > 0 && selectedByDefault == 0 {
		plan.Warnings = append(plan.Warnings, Warning{
			Code: "ambiguous-default", Message: "multiple runnable services exist but no default app entrypoint is unambiguous",
			Recovery: "choose one explicitly with `lns run <service>`",
		})
	}
	if len(plan.Services) == 0 {
		plan.Warnings = append(plan.Warnings, Warning{
			Code:     "no-services",
			Message:  "no runnable HTTP services were discovered",
			Recovery: "run `lns init`, then describe the service explicitly",
		})
	}
	sort.Slice(plan.Warnings, func(i, j int) bool {
		if plan.Warnings[i].Code == plan.Warnings[j].Code {
			return plan.Warnings[i].Message < plan.Warnings[j].Message
		}
		return plan.Warnings[i].Code < plan.Warnings[j].Code
	})
	return plan, nil
}

// ProjectName returns the canonical discovered project identity used by plan,
// init, and the bare run path.
func ProjectName(root string) string {
	if absolute, err := filepath.Abs(root); err == nil {
		root = absolute
	}
	if name := composeProjectName(root); name != "" {
		return name
	}
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err == nil {
		var pkg struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Private {
			if name := discovery.NormalizeName(pkg.Name); name != "" {
				return name
			}
		}
	}
	if name := discovery.NormalizeName(filepath.Base(root)); name != "" {
		return name
	}
	return "project"
}

func stableStrings(values []string) []string {
	seen := make(map[string]bool, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func serviceState(service models.Service) ServiceState {
	if !service.IsResolved() || !service.CanRun() {
		return StateUnresolved
	}
	return StateManaged
}

func (route Route) normalized() (Route, error) {
	route.Scheme = strings.ToLower(strings.TrimSpace(route.Scheme))
	if route.Scheme != "http" && route.Scheme != "https" {
		return Route{}, fmt.Errorf("proxy scheme must be http or https")
	}
	if route.Port < 1 || route.Port > 65535 {
		return Route{}, fmt.Errorf("proxy port must be between 1 and 65535")
	}
	return route, nil
}

func (route Route) url(hostname string) string {
	defaultPort := 80
	if route.Scheme == "https" {
		defaultPort = 443
	}
	if route.Port == defaultPort {
		return route.Scheme + "://" + hostname
	}
	return route.Scheme + "://" + hostname + ":" + strconv.Itoa(route.Port)
}
