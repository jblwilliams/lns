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

	"lns/internal/devrun"
	"lns/internal/discovery"
	"lns/internal/models"
)

var (
	environmentAssignment    = regexp.MustCompile(`^\s*-?\s*([A-Z][A-Z0-9_]*)\s*(?::|=)\s*(.*?)\s*$`)
	defaultExpansion         = regexp.MustCompile(`\$\{[A-Z][A-Z0-9_]*:-([^}]+)\}`)
	inlineEnvironmentDefault = regexp.MustCompile(`process\.env\.([A-Z][A-Z0-9_]*)[^\n]{0,80}?(?:\|\||\?\?)\s*["']([^"']+)["']`)
)

type environmentSignal struct {
	name     string
	value    string
	evidence string
	consumer string
}

func inferEnvironment(root string, route Route, plan *Plan) {
	signals := readEnvironmentSignals(root, plan.Services)
	localHosts := knownLocalHTTPHosts(root, plan.Services)
	applyNamedPorts(plan, signals)
	for _, signal := range signals {
		endpoint, ok := localHTTPEndpoint(signal.value, localHosts)
		if !ok || !isServiceLinkName(signal.name) {
			continue
		}
		target := resolveEndpointTarget(plan.Services, signal.name, endpoint)
		if target.Service == "" {
			continue
		}
		if target.Listener == "http" && strings.HasSuffix(strings.ToLower(endpoint.Hostname()), ".localhost") {
			applyRouteAlias(plan, route, target, strings.ToLower(endpoint.Hostname()))
		}
		network := NetworkRoute
		if target.Listener != "http" {
			network = NetworkLoopback
		}
		binding := EnvironmentBinding{
			Name: signal.name, Kind: BindingURL, Target: target,
			Scheme: endpoint.Scheme, Path: endpointPath(endpoint), Evidence: signal.evidence, Network: network,
		}
		for _, index := range bindingConsumers(plan.Services, signal) {
			binding.Required = bindingRequiresTarget(signal, plan.Services[index], target)
			addBinding(&plan.Services[index], binding)
		}
	}
	for index := range plan.Services {
		sort.Slice(plan.Services[index].Environment, func(i, j int) bool {
			return plan.Services[index].Environment[i].Name < plan.Services[index].Environment[j].Name
		})
	}
}

func endpointPath(endpoint *url.URL) string {
	path := endpoint.EscapedPath()
	if endpoint.RawQuery != "" {
		path += "?" + endpoint.RawQuery
	}
	return path
}

func readEnvironmentSignals(root string, services []Service) []environmentSignal {
	files := []string{".env.development.local", ".env.local", ".env.development", ".env", ".env.example"}
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
				if isNamedPort(match[1], value) || isServiceLinkName(match[1]) {
					signals = append(signals, environmentSignal{name: match[1], value: value, evidence: filepath.ToSlash(rel), consumer: service.Name})
				}
			}
			_ = file.Close()
		}
		for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts", "vite.config.mjs"} {
			path := filepath.Join(serviceRoot, name)
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			for _, match := range inlineEnvironmentDefault.FindAllStringSubmatch(string(data), -1) {
				value := cleanEnvironmentValue(match[2])
				if isNamedPort(match[1], value) || isServiceLinkName(match[1]) {
					signals = append(signals, environmentSignal{name: match[1], value: value, evidence: filepath.ToSlash(rel), consumer: service.Name})
				}
			}
		}
	}
	signals = append(signals, readComposeEnvironmentSignals(root, services)...)
	return prioritizeSignals(signals)
}

func prioritizeSignals(signals []environmentSignal) []environmentSignal {
	process := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if ok && (isServiceLinkName(name) || strings.HasSuffix(name, "_PORT")) {
			process[name] = value
		}
	}
	processNames := make([]string, 0, len(process))
	for name := range process {
		processNames = append(processNames, name)
	}
	sort.Strings(processNames)
	result := make([]environmentSignal, 0, len(signals)+len(processNames))
	for _, name := range processNames {
		result = append(result, environmentSignal{name: name, value: process[name], evidence: "process environment"})
	}
	seen := map[string]bool{}
	global := map[string]bool{}
	for _, signal := range signals {
		if _, overridden := process[signal.name]; overridden || global[signal.name] {
			continue
		}
		key := signal.consumer + "\x00" + signal.name
		if seen[key] {
			continue
		}
		seen[key] = true
		if signal.consumer == "" {
			global[signal.name] = true
		}
		result = append(result, signal)
	}
	return result
}

func readComposeEnvironmentSignals(root string, services []Service) []environmentSignal {
	var signals []environmentSignal
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		file, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		inServices := false
		composeService := ""
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent == 0 {
				inServices = strings.HasPrefix(trimmed, "services:")
				composeService = ""
				continue
			}
			if !inServices {
				continue
			}
			if indent == 2 && strings.HasSuffix(trimmed, ":") {
				composeService = strings.Trim(strings.TrimSuffix(trimmed, ":"), `"'`)
				continue
			}
			if composeService == "" || indent < 4 {
				continue
			}
			match := environmentAssignment.FindStringSubmatch(trimmed)
			if len(match) != 3 {
				continue
			}
			value := cleanEnvironmentValue(match[2])
			if !isNamedPort(match[1], value) && !isServiceLinkName(match[1]) {
				continue
			}
			consumer := composeConsumer(composeService, services)
			if consumer == "" {
				continue
			}
			signals = append(signals, environmentSignal{
				name: match[1], value: value, evidence: name, consumer: consumer,
			})
		}
		_ = file.Close()
	}
	return signals
}

func composeConsumer(composeName string, services []Service) string {
	if target := uniqueService(services, func(service Service) bool {
		return composeMatchesPlanService(composeName, service.Name)
	}); target != "" {
		return target
	}
	normalized := discovery.NormalizeName(composeName)
	if isFrontendToken(normalized) {
		return uniqueService(services, namedFrontend)
	}
	if isBackendToken(normalized) {
		return uniqueService(services, namedBackend)
	}
	return ""
}

func composeMatchesPlanService(composeName, serviceName string) bool {
	composeName = discovery.NormalizeName(composeName)
	serviceName = discovery.NormalizeName(serviceName)
	return composeName == serviceName || strings.HasSuffix(composeName, "-"+serviceName)
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
		if target.Service == "" {
			continue
		}
		for index := range plan.Services {
			if plan.Services[index].Name != target.Service {
				continue
			}
			for listenerIndex := range plan.Services[index].Listeners {
				if plan.Services[index].Listeners[listenerIndex].Name == target.Listener {
					plan.Services[index].Listeners[listenerIndex].Port.Observed = []ObservedPort{{Value: port, Evidence: signal.evidence + ":" + signal.name}}
				}
			}
		}
		for _, index := range bindingConsumers(plan.Services, signal) {
			addBinding(&plan.Services[index], EnvironmentBinding{
				Name: signal.name, Kind: BindingPort, Target: target,
				Required: bindingRequiresTarget(signal, plan.Services[index], target),
				Evidence: signal.evidence, Network: NetworkLoopback,
			})
		}
	}
}

func bindingRequiresTarget(signal environmentSignal, consumer Service, target EndpointRef) bool {
	if consumer.Name == target.Service {
		return false
	}
	upper := strings.ToUpper(signal.name)
	if originBindingName(upper) {
		return false
	}
	if signal.consumer != "" {
		return true
	}
	return frontendBindingName(upper)
}

func resolvePortTarget(services []Service, prefix string, port int) EndpointRef {
	normalized := discovery.NormalizeName(prefix)
	if target := uniqueService(services, func(service Service) bool {
		return discovery.NormalizeName(service.Name) == normalized
	}); target != "" {
		return EndpointRef{Service: target, Listener: "http"}
	}
	if isFrontendToken(normalized) {
		if target := uniqueService(services, namedFrontend); target != "" {
			return EndpointRef{Service: target, Listener: "http"}
		}
	}
	if isBackendToken(normalized) {
		if target := uniqueService(services, namedBackend); target != "" {
			return EndpointRef{Service: target, Listener: "http"}
		}
	}
	target := uniqueService(services, func(service Service) bool {
		return observedPort(service) == port
	})
	if target == "" {
		return EndpointRef{}
	}
	return EndpointRef{Service: target, Listener: "http"}
}

func resolveEndpointTarget(services []Service, key string, endpoint *url.URL) EndpointRef {
	if port, err := strconv.Atoi(endpoint.Port()); err == nil && port > 0 {
		var match EndpointRef
		for _, service := range services {
			if listener, ok := listenerByObservedPort(service, port); ok {
				if match.Service != "" {
					return EndpointRef{}
				}
				match = EndpointRef{Service: service.Name, Listener: listener.Name}
			}
		}
		if match.Service != "" {
			return match
		}
		if target := uniqueService(services, func(service Service) bool { return observedPort(service) == port }); target != "" {
			return EndpointRef{Service: target, Listener: "http"}
		}
	}
	host := discovery.NormalizeName(strings.TrimSuffix(endpoint.Hostname(), ".localhost"))
	if target := uniqueService(services, func(service Service) bool {
		name := discovery.NormalizeName(service.Name)
		return name != "" && (host == name || strings.HasSuffix(host, "-"+name))
	}); target != "" {
		return EndpointRef{Service: target, Listener: "http"}
	}
	upper := strings.ToUpper(key + "_" + host)
	if strings.Contains(upper, "API") || strings.Contains(upper, "SERVER") || strings.Contains(upper, "BACKEND") {
		return endpointRef(uniqueService(services, namedBackend))
	}
	if strings.Contains(upper, "WEB") || strings.Contains(upper, "CLIENT") || strings.Contains(upper, "FRONTEND") {
		return endpointRef(uniqueService(services, namedFrontend))
	}
	return EndpointRef{}
}

func bindingConsumers(services []Service, signal environmentSignal) []int {
	var indexes []int
	if signal.consumer != "" {
		for index, service := range services {
			if service.Name == signal.consumer && service.State == StateManaged {
				return []int{index}
			}
		}
		return nil
	}
	upper := strings.ToUpper(signal.name)
	for index, service := range services {
		if service.State != StateManaged {
			continue
		}
		switch {
		case frontendBindingName(upper):
			if serviceProfile(service) == models.ProfileHMR {
				indexes = append(indexes, index)
			}
		case originBindingName(upper):
			if serviceProfile(service) == models.ProfileStandard {
				indexes = append(indexes, index)
			}
		default:
			indexes = append(indexes, index)
		}
	}
	return indexes
}

func frontendBindingName(upper string) bool {
	return strings.HasPrefix(upper, "VITE_") || strings.Contains(upper, "API_PROXY") || strings.Contains(upper, "API_TARGET")
}

func originBindingName(upper string) bool {
	return strings.Contains(upper, "CORS") || strings.Contains(upper, "FRONTEND_ORIGIN") || strings.Contains(upper, "CLIENT_ORIGIN")
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
	return serviceProfile(service) == models.ProfileHMR && containsToken(name, "web", "client", "frontend", "ui")
}

func namedBackend(service Service) bool {
	name := discovery.NormalizeName(service.Name)
	return serviceProfile(service) == models.ProfileStandard && containsToken(name, "api", "server", "backend")
}

func observedPort(service Service) int {
	if listener, ok := listenerByName(service, "http"); ok && len(listener.Port.Observed) == 1 {
		return listener.Port.Observed[0].Value
	}
	return 0
}

func endpointRef(service string) EndpointRef {
	if service == "" {
		return EndpointRef{}
	}
	return EndpointRef{Service: service, Listener: "http"}
}

func serviceProfile(service Service) models.Profile {
	if listener, ok := listenerByName(service, "http"); ok {
		return listener.Profile
	}
	return models.ProfileStandard
}

func applyRouteAlias(plan *Plan, route Route, target EndpointRef, hostname string) {
	hostname = devrun.ApplyWorktreePrefix(hostname, plan.Project.Worktree)
	for serviceIndex := range plan.Services {
		if plan.Services[serviceIndex].Name != target.Service {
			continue
		}
		for listenerIndex := range plan.Services[serviceIndex].Listeners {
			listener := &plan.Services[serviceIndex].Listeners[listenerIndex]
			if listener.Name == target.Listener && listener.Public {
				listener.Hostname = hostname
				listener.URL = route.url(hostname)
			}
		}
	}
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

func localHTTPEndpoint(value string, knownHosts map[string]bool) (*url.URL, bool) {
	endpoint, err := url.Parse(value)
	if err != nil || (endpoint.Scheme != "http" && endpoint.Scheme != "https" && endpoint.Scheme != "ws" && endpoint.Scheme != "wss") {
		return nil, false
	}
	host := strings.ToLower(endpoint.Hostname())
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || strings.HasSuffix(host, ".localhost") || knownHosts[discovery.NormalizeName(host)] {
		return endpoint, true
	}
	return nil, false
}

func knownLocalHTTPHosts(root string, services []Service) map[string]bool {
	hosts := map[string]bool{}
	for _, service := range services {
		if name := discovery.NormalizeName(service.Name); name != "" {
			hosts[name] = true
		}
	}
	for _, name := range []string{"compose.yml", "compose.yaml", "docker-compose.yml", "docker-compose.yaml"} {
		file, err := os.Open(filepath.Join(root, name))
		if err != nil {
			continue
		}
		inServices := false
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := scanner.Text()
			trimmed := strings.TrimSpace(line)
			if trimmed == "" || strings.HasPrefix(trimmed, "#") {
				continue
			}
			indent := len(line) - len(strings.TrimLeft(line, " \t"))
			if indent == 0 {
				inServices = strings.HasPrefix(trimmed, "services:")
				continue
			}
			if inServices && indent == 2 && strings.HasSuffix(trimmed, ":") {
				service := strings.Trim(strings.TrimSuffix(trimmed, ":"), `"'`)
				if service = discovery.NormalizeName(service); service != "" {
					hosts[service] = true
				}
			}
		}
		_ = file.Close()
	}
	return hosts
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
