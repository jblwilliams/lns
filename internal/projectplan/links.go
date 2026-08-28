package projectplan

import (
	"bufio"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"lns/internal/discovery"
	"lns/internal/models"
)

var (
	environmentAssignment = regexp.MustCompile(`^\s*-?\s*([A-Z][A-Z0-9_]*)\s*(?::|=)\s*(.*?)\s*$`)
	defaultExpansion      = regexp.MustCompile(`\$\{[A-Z][A-Z0-9_]*:-([^}]+)\}`)
)

type environmentSignal struct {
	name     string
	value    string
	evidence string
}

func inferEnvironment(root string, plan *Plan) {
	signals := readEnvironmentSignals(root, plan.Services)
	applyNamedPorts(plan, signals)
	for _, signal := range signals {
		endpoint, ok := localHTTPEndpoint(signal.value)
		if !ok || !isServiceLinkName(signal.name) {
			continue
		}
		target := resolveEndpointTarget(plan.Services, signal.name, endpoint)
		if target == "" {
			continue
		}
		binding := EnvironmentBinding{
			Name: signal.name, Kind: BindingURL, Target: target,
			Path: endpoint.EscapedPath(), Evidence: signal.evidence,
		}
		for _, index := range bindingConsumers(plan.Services, signal.name) {
			addBinding(&plan.Services[index], binding)
		}
	}
	for index := range plan.Services {
		sort.Slice(plan.Services[index].Environment, func(i, j int) bool {
			return plan.Services[index].Environment[i].Name < plan.Services[index].Environment[j].Name
		})
	}
}

func readEnvironmentSignals(root string, services []Service) []environmentSignal {
	files := []string{
		".env.development.local", ".env.local", ".env.development", ".env", ".env.example",
		"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml",
	}
	seenRoots := map[string]bool{}
	var signals []environmentSignal
	for _, service := range append([]Service{{Root: "."}}, services...) {
		serviceRoot := filepath.Clean(filepath.Join(root, service.Root))
		if seenRoots[serviceRoot] {
			continue
		}
		seenRoots[serviceRoot] = true
		for _, name := range files {
			path := filepath.Join(serviceRoot, name)
			file, err := os.Open(path)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			scanner := bufio.NewScanner(file)
			for scanner.Scan() {
				match := environmentAssignment.FindStringSubmatch(scanner.Text())
				if len(match) != 3 {
					continue
				}
				value := cleanEnvironmentValue(match[2])
				if isNamedPort(match[1], value) || isLocalHTTPValue(value) {
					signals = append(signals, environmentSignal{name: match[1], value: value, evidence: filepath.ToSlash(rel)})
				}
			}
			_ = file.Close()
		}
	}
	return signals
}

func applyNamedPorts(plan *Plan, signals []environmentSignal) {
	for _, signal := range signals {
		if !strings.HasSuffix(signal.name, "_PORT") {
			continue
		}
		port, err := strconv.Atoi(signal.value)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		target := resolvePortTarget(plan.Services, strings.TrimSuffix(signal.name, "_PORT"), port)
		if target == "" {
			continue
		}
		for index := range plan.Services {
			if plan.Services[index].Name == target {
				plan.Services[index].Port.Observed = []ObservedPort{{Value: port, Evidence: signal.evidence + ":" + signal.name}}
			}
			addBinding(&plan.Services[index], EnvironmentBinding{
				Name: signal.name, Kind: BindingPort, Target: target, Evidence: signal.evidence,
			})
		}
	}
}

func resolvePortTarget(services []Service, prefix string, port int) string {
	normalized := discovery.NormalizeName(prefix)
	if target := uniqueService(services, func(service Service) bool {
		return discovery.NormalizeName(service.Name) == normalized
	}); target != "" {
		return target
	}
	if isFrontendToken(normalized) {
		if target := uniqueService(services, namedFrontend); target != "" {
			return target
		}
	}
	if isBackendToken(normalized) {
		if target := uniqueService(services, namedBackend); target != "" {
			return target
		}
	}
	return uniqueService(services, func(service Service) bool {
		return observedPort(service) == port
	})
}

func resolveEndpointTarget(services []Service, key string, endpoint *url.URL) string {
	if port, err := strconv.Atoi(endpoint.Port()); err == nil && port > 0 {
		if target := uniqueService(services, func(service Service) bool { return observedPort(service) == port }); target != "" {
			return target
		}
	}
	host := discovery.NormalizeName(strings.TrimSuffix(endpoint.Hostname(), ".localhost"))
	if target := uniqueService(services, func(service Service) bool {
		name := discovery.NormalizeName(service.Name)
		return name != "" && (host == name || strings.HasSuffix(host, "-"+name))
	}); target != "" {
		return target
	}
	upper := strings.ToUpper(key + "_" + host)
	if strings.Contains(upper, "API") || strings.Contains(upper, "SERVER") || strings.Contains(upper, "BACKEND") {
		return uniqueService(services, namedBackend)
	}
	if strings.Contains(upper, "WEB") || strings.Contains(upper, "CLIENT") || strings.Contains(upper, "FRONTEND") {
		return uniqueService(services, namedFrontend)
	}
	return ""
}

func bindingConsumers(services []Service, key string) []int {
	var indexes []int
	upper := strings.ToUpper(key)
	for index, service := range services {
		if service.State != StateManaged {
			continue
		}
		switch {
		case strings.HasPrefix(upper, "VITE_"), strings.Contains(upper, "API_PROXY"), strings.Contains(upper, "API_TARGET"):
			if service.Profile == models.ProfileHMR {
				indexes = append(indexes, index)
			}
		case strings.Contains(upper, "CORS"), strings.Contains(upper, "FRONTEND_ORIGIN"), strings.Contains(upper, "CLIENT_ORIGIN"):
			if service.Profile == models.ProfileStandard {
				indexes = append(indexes, index)
			}
		default:
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func addBinding(service *Service, binding EnvironmentBinding) {
	for index, existing := range service.Environment {
		if existing.Name == binding.Name {
			service.Environment[index] = binding
			return
		}
	}
	service.Environment = append(service.Environment, binding)
}

func uniqueService(services []Service, matches func(Service) bool) string {
	match := ""
	for _, service := range services {
		if service.State == StateUnresolved || !matches(service) {
			continue
		}
		if match != "" {
			return ""
		}
		match = service.Name
	}
	return match
}

func namedFrontend(service Service) bool {
	name := discovery.NormalizeName(service.Name)
	return service.Profile == models.ProfileHMR && containsToken(name, "web", "client", "frontend", "ui")
}

func namedBackend(service Service) bool {
	name := discovery.NormalizeName(service.Name)
	return service.Profile == models.ProfileStandard && containsToken(name, "api", "server", "backend")
}

func observedPort(service Service) int {
	if len(service.Port.Observed) == 1 {
		return service.Port.Observed[0].Value
	}
	return 0
}

func containsToken(value string, tokens ...string) bool {
	parts := strings.Split(value, "-")
	for _, part := range parts {
		for _, token := range tokens {
			if part == token {
				return true
			}
		}
	}
	return false
}

func isFrontendToken(value string) bool {
	return containsToken(value, "web", "client", "frontend", "ui", "vite")
}
func isBackendToken(value string) bool { return containsToken(value, "api", "server", "backend") }

func cleanEnvironmentValue(value string) string {
	value = strings.TrimSpace(strings.SplitN(value, " #", 2)[0])
	value = strings.Trim(value, `"'`)
	return defaultExpansion.ReplaceAllString(value, "$1")
}

func isNamedPort(name, value string) bool {
	if !strings.HasSuffix(name, "_PORT") {
		return false
	}
	port, err := strconv.Atoi(value)
	return err == nil && port > 0 && port <= 65535
}

func isLocalHTTPValue(value string) bool {
	_, ok := localHTTPEndpoint(value)
	return ok
}

func localHTTPEndpoint(value string) (*url.URL, bool) {
	endpoint, err := url.Parse(value)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https" && endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return nil, false
	}
	host := strings.ToLower(endpoint.Hostname())
	if host == "localhost" || host == "127.0.0.1" || strings.HasSuffix(host, ".localhost") || (!strings.Contains(host, ".") && host != "") {
		return endpoint, true
	}
	return nil, false
}

func ignoredLinkName(name string) bool {
	upper := strings.ToUpper(name)
	for _, token := range []string{"DATABASE", "POSTGRES", "REDIS", "MYSQL", "MONGO"} {
		if strings.Contains(upper, token) {
			return true
		}
	}
	return false
}

func isServiceLinkName(name string) bool {
	if ignoredLinkName(name) {
		return false
	}
	upper := strings.ToUpper(name)
	if strings.Contains(upper, "CORS") || strings.Contains(upper, "API_PROXY") || strings.Contains(upper, "API_TARGET") {
		return true
	}
	if strings.HasPrefix(upper, "VITE_") && (strings.Contains(upper, "API") || strings.Contains(upper, "SERVER") || strings.Contains(upper, "BACKEND")) {
		return true
	}
	for _, prefix := range []string{"FRONTEND_", "CLIENT_", "WEB_"} {
		if strings.HasPrefix(upper, prefix) && (strings.HasSuffix(upper, "_URL") || strings.HasSuffix(upper, "_ORIGIN")) {
			return true
		}
	}
	return false
}

func composeProjectName(root string) string {
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		file, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
				continue
			}
			key, value, ok := strings.Cut(line, ":")
			if ok && strings.TrimSpace(key) == "name" {
				_ = file.Close()
				return discovery.NormalizeName(strings.Trim(strings.TrimSpace(value), `"'`))
			}
		}
		_ = file.Close()
	}
	return ""
}
