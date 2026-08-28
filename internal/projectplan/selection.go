package projectplan

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"lns/internal/discovery"
	"lns/internal/models"
)

type rootWrapper struct {
	Script string
}

// rootWrappers retains root scripts that add meaningful setup around a
// workspace dev command. Pure package-manager delegation stays collapsed to
// the workspace so the plan has one canonical command owner.
func rootWrappers(root string, services []discovery.DetectedService) map[string]rootWrapper {
	pkg, ok := readPackageFile(filepath.Join(root, "package.json"))
	if !ok {
		return nil
	}
	result := map[string]rootWrapper{}
	for _, service := range services {
		if service.Root == "." {
			continue
		}
		target, _ := readPackageFile(filepath.Join(root, service.Root, "package.json"))
		var candidates []string
		for script, command := range pkg.Scripts {
			if !rootEntrypoint(script, service.Name) || !delegatesTo(command, service.Root, target.Name) || pureDelegation(command) {
				continue
			}
			candidates = append(candidates, script)
		}
		sort.Slice(candidates, func(i, j int) bool {
			left := pkg.Scripts[candidates[i]]
			right := pkg.Scripts[candidates[j]]
			leftDocker := strings.Contains(left, "docker compose")
			rightDocker := strings.Contains(right, "docker compose")
			if leftDocker != rightDocker {
				return !leftDocker
			}
			if len(candidates[i]) != len(candidates[j]) {
				return len(candidates[i]) < len(candidates[j])
			}
			return candidates[i] < candidates[j]
		})
		if len(candidates) > 0 {
			result[service.Name] = rootWrapper{Script: candidates[0]}
		}
	}
	return result
}

func defaultServices(services []models.Service, source Source) map[string]bool {
	defaults := map[string]bool{}
	var managed []models.Service
	for _, service := range services {
		if serviceState(service) == StateManaged {
			managed = append(managed, service)
		}
	}
	if source == SourceConfig {
		for _, service := range managed {
			defaults[service.Name] = true
		}
		return defaults
	}
	if len(managed) == 1 {
		defaults[managed[0].Name] = true
		return defaults
	}
	frontends := matchingModels(managed, modelFrontend)
	backends := matchingModels(managed, modelBackend)
	if len(frontends) == 1 && len(backends) == 1 {
		defaults[frontends[0].Name] = true
		defaults[backends[0].Name] = true
		return defaults
	}
	for _, service := range managed {
		if service.Root == "." {
			defaults[service.Name] = true
			return defaults
		}
	}
	return defaults
}

type packageFile struct {
	Name    string            `json:"name"`
	Scripts map[string]string `json:"scripts"`
}

func readPackageFile(path string) (packageFile, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return packageFile{}, false
	}
	var pkg packageFile
	if json.Unmarshal(data, &pkg) != nil {
		return packageFile{}, false
	}
	return pkg, true
}

func rootEntrypoint(script, service string) bool {
	script = discovery.NormalizeName(strings.ReplaceAll(script, ":", "-"))
	service = discovery.NormalizeName(service)
	for _, candidate := range []string{service, "dev-" + service, service + "-dev"} {
		if script == candidate {
			return true
		}
	}
	return false
}

func delegatesTo(command, root, packageName string) bool {
	command = strings.ReplaceAll(command, "'", "")
	command = strings.ReplaceAll(command, `"`, "")
	root = filepath.ToSlash(filepath.Clean(root))
	return strings.Contains(command, " -C "+root+" ") ||
		strings.Contains(command, " --dir "+root+" ") ||
		(packageName != "" && strings.Contains(command, " --filter "+packageName+" "))
}

func pureDelegation(command string) bool {
	command = strings.TrimSpace(command)
	if strings.ContainsAny(command, ";&|") || strings.Contains(command, "bash ") || strings.Contains(command, "sh ") {
		return false
	}
	fields := strings.Fields(command)
	return len(fields) >= 4 && (fields[0] == "pnpm" || fields[0] == "npm" || fields[0] == "yarn" || fields[0] == "bun")
}

func matchingModels(services []models.Service, match func(models.Service) bool) []models.Service {
	var result []models.Service
	for _, service := range services {
		if match(service) {
			result = append(result, service)
		}
	}
	return result
}

func modelFrontend(service models.Service) bool {
	return service.EffectiveProfile() == models.ProfileHMR && containsToken(discovery.NormalizeName(service.Name), "web", "client", "frontend")
}

func modelBackend(service models.Service) bool {
	return service.EffectiveProfile() == models.ProfileStandard && containsToken(discovery.NormalizeName(service.Name), "api", "server", "backend")
}
