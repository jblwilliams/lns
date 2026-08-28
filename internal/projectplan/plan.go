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
	"lns/internal/projectconfig"
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
	PortFixed      PortStrategy = "fixed"
	PortUnresolved PortStrategy = "unresolved"
)

type ServiceState string

const (
	StateManaged    ServiceState = "managed"
	StateExternal   ServiceState = "external"
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
	Script      string               `json:"script,omitempty"`
	Command     []string             `json:"command,omitempty"`
	Profile     models.Profile       `json:"profile"`
	State       ServiceState         `json:"state"`
	Hostname    string               `json:"hostname"`
	URL         string               `json:"url"`
	Port        Port                 `json:"port"`
	Environment []EnvironmentBinding `json:"environment,omitempty"`
	Evidence    []string             `json:"evidence"`
}

type BindingKind string

const (
	BindingPort BindingKind = "port"
	BindingURL  BindingKind = "url"
)

type EnvironmentBinding struct {
	Name     string      `json:"name"`
	Kind     BindingKind `json:"kind"`
	Target   string      `json:"target"`
	Path     string      `json:"path,omitempty"`
	Evidence string      `json:"evidence"`
}

type Port struct {
	Strategy PortStrategy   `json:"strategy"`
	Fixed    int            `json:"fixed,omitempty"`
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
	if projectconfig.Exists(absRoot) {
		cfg, err := projectconfig.Load(absRoot)
		if err != nil {
			return Plan{}, fmt.Errorf("load %s: %w", projectconfig.Filename, err)
		}
		if errs := projectconfig.Validate(cfg); len(errs) > 0 {
			return Plan{}, fmt.Errorf("invalid %s: %s", projectconfig.Filename, joinValidationErrors(errs))
		}
		name = cfg.Name
		source = SourceConfig
		compiled = cfg.ToProject(absRoot)
	} else {
		detected := discovery.DetectServices(absRoot)
		evidenceByService = make(map[string]evidence, len(detected))
		compiled = models.Project{Name: name, Path: absRoot, Services: make([]models.Service, 0, len(detected))}
		for _, service := range detected {
			if len(detected) == 1 && service.Root == "." {
				service.Name = name
			}
			evidenceByService[service.Name] = evidence{
				all:  append([]string(nil), service.Evidence...),
				port: service.PortEvidence,
			}
			compiled.Services = append(compiled.Services, models.Service{
				Name:    service.Name,
				Root:    service.Root,
				Port:    service.Port,
				Script:  service.Script,
				Profile: service.Profile,
				Source:  service.Source,
				Status:  service.Status,
			})
		}
	}

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
		hostname := devrun.ApplyWorktreePrefix(compiled.GetServiceHostname(service), plan.Project.Worktree)
		serviceEvidence := evidenceByService[service.Name]
		if source == SourceConfig {
			serviceEvidence = evidence{all: []string{projectconfig.Filename}, port: projectconfig.Filename}
		}
		serviceEvidence.all = stableStrings(serviceEvidence.all)
		state := serviceState(service)
		port := Port{Strategy: PortDynamic}
		if service.Port > 0 {
			port.Observed = []ObservedPort{{Value: service.Port, Evidence: serviceEvidence.port}}
		}
		switch state {
		case StateManaged:
		case StateExternal:
			port.Strategy = PortFixed
			port.Fixed = service.Port
		case StateUnresolved:
			port.Strategy = PortUnresolved
		default:
			panic(fmt.Sprintf("unhandled service state %q", state))
		}
		plan.Services = append(plan.Services, Service{
			Name:     service.Name,
			Root:     service.Root,
			Script:   service.Script,
			Command:  append([]string(nil), service.Command...),
			Profile:  service.EffectiveProfile(),
			State:    state,
			Hostname: hostname,
			URL:      route.url(hostname),
			Port:     port,
			Evidence: serviceEvidence.all,
		})
		if state == StateUnresolved {
			recovery := "run `lns init`, then resolve the service explicitly"
			if source == SourceConfig {
				recovery = "edit lns.json and set a valid profile plus script, command, or port"
			}
			plan.Warnings = append(plan.Warnings, Warning{
				Code:     "unresolved-service",
				Message:  fmt.Sprintf("service %q is unresolved and will not run", service.Name),
				Recovery: recovery,
			})
		}
	}

	sort.Slice(plan.Services, func(i, j int) bool { return plan.Services[i].Name < plan.Services[j].Name })
	inferEnvironment(absRoot, &plan)
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

func joinValidationErrors(errs []projectconfig.ValidationError) string {
	messages := make([]string, len(errs))
	for i, err := range errs {
		messages[i] = err.Error()
	}
	return strings.Join(messages, "; ")
}

func serviceState(service models.Service) ServiceState {
	if !service.IsResolved() {
		return StateUnresolved
	}
	if service.CanRun() {
		return StateManaged
	}
	return StateExternal
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
