package projectplan

import (
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
	codePortName     = regexp.MustCompile(`process\.env\.(PORT|[A-Z][A-Z0-9_]*_PORT)`)
	numericPort      = regexp.MustCompile(`\b([0-9]{2,5})\b`)
	shellPortDefault = regexp.MustCompile(`\$\{(PORT|[A-Z][A-Z0-9_]*_PORT):-([0-9]{2,5})\}`)
	shellScriptPath  = regexp.MustCompile(`(?:^|\s)((?:scripts|tools)/[^\s"']+\.sh)(?:\s|$)`)
)

func publicPortEnvironment(root string, service models.Service) []string {
	data, _ := os.ReadFile(filepath.Join(root, service.Root, "package.json"))
	if service.EffectiveProfile() == models.ProfileHMR && strings.Contains(strings.ToLower(string(data)), "vite") {
		return []string{"VITE_PORT"}
	}
	return []string{"PORT"}
}

// inferListeners recognizes bounded multi-process dev scripts. It does not try
// to parse arbitrary shell: only a concurrently-owned package is expanded, and
// only explicit environment port defaults become private listeners.
func inferListeners(root string, plan *Plan) {
	for serviceIndex := range plan.Services {
		service := &plan.Services[serviceIndex]
		pkg, ok := readPackageFile(filepath.Join(root, service.Root, "package.json"))
		if !ok || !hasConcurrentDevScript(pkg) {
			continue
		}
		files := listenerEvidenceFiles(root, *service)
		defaults := map[string]ObservedPort{}
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			rel, _ := filepath.Rel(root, path)
			for _, line := range strings.Split(string(data), "\n") {
				name := codePortName.FindStringSubmatch(line)
				numbers := numericPort.FindAllStringSubmatch(line, -1)
				if len(name) == 2 && len(numbers) > 0 {
					port, _ := strconv.Atoi(numbers[len(numbers)-1][1])
					if port > 0 && port <= 65535 {
						defaults[name[1]] = ObservedPort{Value: port, Evidence: filepath.ToSlash(rel) + ":" + name[1]}
					}
				}
			}
			for _, match := range shellPortDefault.FindAllStringSubmatch(string(data), -1) {
				port, _ := strconv.Atoi(match[2])
				if port > 0 && port <= 65535 {
					defaults[match[1]] = ObservedPort{Value: port, Evidence: filepath.ToSlash(rel) + ":" + match[1]}
				}
			}
		}
		public := service.Listeners[0].Environment[0]
		if observed, ok := defaults[public]; ok {
			service.Listeners[0].Port.Observed = []ObservedPort{observed}
		}
		keys := make([]string, 0, len(defaults))
		for environment := range defaults {
			if environment != public {
				keys = append(keys, environment)
			}
		}
		sort.Strings(keys)
		for _, environment := range keys {
			service.Listeners = append(service.Listeners, Listener{
				Name: listenerName(environment), Profile: models.ProfileStandard,
				Public: false, Environment: []string{environment},
				Port: Port{Strategy: PortDynamic, Observed: []ObservedPort{defaults[environment]}},
			})
		}
	}
}

func hasConcurrentDevScript(pkg packageFile) bool {
	for script, command := range pkg.Scripts {
		if (script == "dev" || strings.HasSuffix(script, ":dev")) && strings.Contains(command, "concurrently") {
			return true
		}
	}
	return false
}

func listenerEvidenceFiles(root string, service Service) []string {
	serviceRoot := filepath.Join(root, service.Root)
	files := []string{
		filepath.Join(serviceRoot, "vite.config.ts"), filepath.Join(serviceRoot, "vite.config.js"),
		filepath.Join(serviceRoot, "vite.config.mts"), filepath.Join(serviceRoot, "vite.config.mjs"),
		filepath.Join(serviceRoot, "server.ts"), filepath.Join(serviceRoot, "server.js"),
		filepath.Join(serviceRoot, "server", "config.ts"), filepath.Join(serviceRoot, "server", "config.js"),
	}
	if service.RunRoot == "." && service.Script != "" {
		if pkg, ok := readPackageFile(filepath.Join(root, "package.json")); ok {
			for _, match := range shellScriptPath.FindAllStringSubmatch(pkg.Scripts[service.Script], -1) {
				files = append(files, filepath.Join(root, filepath.FromSlash(match[1])))
			}
		}
	}
	return files
}

func listenerName(environment string) string {
	name := strings.TrimSuffix(environment, "_PORT")
	name = strings.TrimSuffix(name, "PORT")
	name = strings.TrimPrefix(name, "SCRIBE_")
	if name == "" {
		return "api"
	}
	return discovery.NormalizeName(name)
}

func listenerByName(service Service, name string) (Listener, bool) {
	for _, listener := range service.Listeners {
		if listener.Name == name {
			return listener, true
		}
	}
	return Listener{}, false
}

func listenerByObservedPort(service Service, port int) (Listener, bool) {
	for _, listener := range service.Listeners {
		for _, observed := range listener.Port.Observed {
			if observed.Value == port {
				return listener, true
			}
		}
	}
	return Listener{}, false
}

func EndpointKey(ref EndpointRef) string {
	return ref.Service + "#" + ref.Listener
}
