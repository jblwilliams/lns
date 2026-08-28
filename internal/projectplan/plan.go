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
	"strings"
	"unicode"

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
	PortDynamic PortStrategy = "dynamic"
	PortFixed   PortStrategy = "fixed"
)

type Plan struct {
	SchemaVersion int          `json:"schema_version"`
	Project       Project      `json:"project"`
	Services      []Service    `json:"services"`
	Dependencies  []Dependency `json:"dependencies"`
	Warnings      []Warning    `json:"warnings"`
}

type Project struct {
	Name   string `json:"name"`
	Root   string `json:"root"`
	Source Source `json:"source"`
}

type Service struct {
	Name        string            `json:"name"`
	Root        string            `json:"root"`
	Script      string            `json:"script,omitempty"`
	Command     []string          `json:"command,omitempty"`
	Profile     models.Profile    `json:"profile"`
	Hostname    string            `json:"hostname"`
	URL         string            `json:"url"`
	Port        Port              `json:"port"`
	Environment map[string]string `json:"environment,omitempty"`
	Evidence    []string          `json:"evidence"`
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

type Dependency struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type Warning struct {
	Code     string `json:"code"`
	Message  string `json:"message"`
	Recovery string `json:"recovery,omitempty"`
}

// Build returns a deterministic symbolic plan for root. An explicit lns.json
// is an override; without one, the plan is discovered entirely in memory.
func Build(root string) (Plan, error) {
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

	name := discoveredProjectName(absRoot)
	source := SourceDiscovered
	var compiled models.Project
	var detectedEvidence map[string][]string
	if projectconfig.Exists(absRoot) {
		cfg, loadErr := projectconfig.Load(absRoot)
		err = loadErr
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
		detectedEvidence = make(map[string][]string, len(detected))
		compiled = models.Project{Name: name, Path: absRoot, Services: make([]models.Service, 0, len(detected))}
		for _, service := range detected {
			if len(detected) == 1 && service.Root == "." {
				service.Name = name
			}
			detectedEvidence[service.Name] = append([]string(nil), service.Evidence...)
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
			Name:   name,
			Root:   absRoot,
			Source: source,
		},
		Services:     []Service{},
		Dependencies: []Dependency{},
		Warnings:     []Warning{},
	}
	for _, service := range compiled.Services {
		hostname := compiled.GetServiceHostname(service)
		evidence := detectedEvidence[service.Name]
		if source == SourceConfig {
			evidence = []string{projectconfig.Filename}
		}
		evidence = stableStrings(evidence)
		port := Port{Strategy: PortDynamic}
		if service.Port > 0 {
			port.Observed = []ObservedPort{{Value: service.Port, Evidence: firstEvidence(evidence)}}
		}
		if !service.CanRun() && service.Port > 0 {
			port.Strategy = PortFixed
			port.Fixed = service.Port
		}
		plan.Services = append(plan.Services, Service{
			Name:        service.Name,
			Root:        service.Root,
			Script:      service.Script,
			Command:     append([]string(nil), service.Command...),
			Profile:     service.EffectiveProfile(),
			Hostname:    hostname,
			URL:         "http://" + hostname,
			Port:        port,
			Environment: map[string]string{},
			Evidence:    evidence,
		})
	}

	sort.Slice(plan.Services, func(i, j int) bool { return plan.Services[i].Name < plan.Services[j].Name })
	runnable := 0
	for _, service := range plan.Services {
		if service.Script != "" || len(service.Command) > 0 {
			runnable++
		}
	}
	if runnable == 0 {
		plan.Warnings = append(plan.Warnings, Warning{
			Code:     "no-services",
			Message:  "no runnable HTTP services were discovered",
			Recovery: "run `lns init`, then describe the service explicitly",
		})
	}
	return plan, nil
}

func discoveredProjectName(root string) string {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err == nil {
		var pkg struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		if json.Unmarshal(data, &pkg) == nil && pkg.Private {
			if name := dnsLabel(pkg.Name); name != "" {
				return name
			}
		}
	}
	if name := dnsLabel(filepath.Base(root)); name != "" {
		return name
	}
	return "project"
}

func dnsLabel(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "@"))
	var out strings.Builder
	dash := false
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			out.WriteRune(r)
			dash = false
		} else if out.Len() > 0 && !dash {
			out.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(out.String(), "-")
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

func firstEvidence(evidence []string) string {
	if len(evidence) == 0 {
		return "observed project configuration"
	}
	return evidence[0]
}

func joinValidationErrors(errs []projectconfig.ValidationError) string {
	messages := make([]string, len(errs))
	for i, err := range errs {
		messages[i] = err.Error()
	}
	return strings.Join(messages, "; ")
}
