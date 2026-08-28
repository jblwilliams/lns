package discovery

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"lns/internal/models"
	"lns/internal/projectconfig"
)

var (
	envPortPattern        = regexp.MustCompile(`(?m)^\s*(?:PORT|VITE_PORT|NEXT_PORT|NUXT_PORT)\s*=\s*["']?(\d{2,5})["']?\s*$`)
	inlinePortPattern     = regexp.MustCompile(`(?:^|\s)(?:PORT|VITE_PORT|NEXT_PORT|NUXT_PORT)\s*=\s*(\d{2,5})(?:\s|$)`)
	cliPortPattern        = regexp.MustCompile(`(?:--port|-p)\s*(?:=|\s)\s*(\d{2,5})`)
	structuredPortPattern = regexp.MustCompile(`(?m)\bport\s*:\s*(\d{2,5})\b`)
	localhostPortPattern  = regexp.MustCompile(`localhost:(\d{2,5})`)
)

type DetectedService struct {
	Name         string
	Root         string
	Port         int
	PortEvidence string
	Script       string
	Profile      models.Profile
	Status       models.ServiceStatus
	Source       models.ServiceSource
	Evidence     []string
}

type Drift struct {
	Service    string
	Field      string
	Configured string
	Detected   string
	Evidence   string
}

func BootstrapConfig(projectName, projectRoot, prefix string) *projectconfig.Config {
	cfg := &projectconfig.Config{
		Name:     projectName,
		Prefix:   prefix,
		Services: map[string]projectconfig.Service{},
	}

	detected := DetectServices(projectRoot)
	if len(detected) == 0 {
		detected = []DetectedService{{
			Name:     serviceNameOrFallback(projectName),
			Root:     ".",
			Status:   models.StatusUnresolved,
			Source:   models.SourceDetected,
			Evidence: []string{"no services detected from common repo signals"},
		}}
	} else if len(detected) == 1 && detected[0].Root == "." {
		if name := NormalizeName(projectName); name != "" {
			detected[0].Name = name
		}
	}

	for _, service := range detected {
		cfg.Services[service.Name] = projectconfig.Service{
			Root:    service.Root,
			Port:    service.Port,
			Script:  service.Script,
			Profile: service.Profile,
			Source:  service.Source,
			Status:  service.Status,
		}
	}

	cfg.Normalize()
	return cfg
}

func DetectServices(projectRoot string) []DetectedService {
	roots := discoverCandidateRoots(projectRoot)
	services := make([]DetectedService, 0, len(roots))
	usedNames := map[string]int{}

	for _, root := range roots {
		service, ok := inspectRoot(projectRoot, root)
		if !ok {
			continue
		}

		siblings := detectSiblingScriptServices(projectRoot, root, service.Script)
		if root == "." && service.Profile == models.ProfileHMR && len(siblings) > 0 {
			service.Name = "web"
		}
		name := uniqueName(service.Name, usedNames)
		service.Name = name
		services = append(services, service)
		for _, sibling := range siblings {
			sibling.Name = uniqueName(sibling.Name, usedNames)
			services = append(services, sibling)
		}
	}

	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})

	return services
}

func DetectDrift(projectRoot string, cfg *projectconfig.Config) []Drift {
	cfg.Normalize()

	var drifts []Drift
	for _, name := range cfg.SortedServiceNames() {
		service := cfg.Services[name]
		root := service.Root
		if root == "" {
			root = "."
		}

		detected, ok := inspectRoot(projectRoot, root)
		if !ok {
			continue
		}

		if service.Port > 0 && detected.Port > 0 && service.Port != detected.Port {
			drifts = append(drifts, Drift{
				Service:    name,
				Field:      "port",
				Configured: itoa(service.Port),
				Detected:   itoa(detected.Port),
				Evidence:   strings.Join(detected.Evidence, ", "),
			})
		}
		if service.Profile != "" && detected.Profile != "" && service.Profile != detected.Profile {
			drifts = append(drifts, Drift{
				Service:    name,
				Field:      "profile",
				Configured: string(service.Profile),
				Detected:   string(detected.Profile),
				Evidence:   strings.Join(detected.Evidence, ", "),
			})
		}
	}

	return drifts
}

func InspectServiceRoot(projectRoot, relRoot string) (DetectedService, bool) {
	return inspectRoot(projectRoot, relRoot)
}

func discoverCandidateRoots(projectRoot string) []string {
	seen := map[string]bool{}
	var roots []string

	add := func(rel string) {
		rel = cleanRoot(rel)
		if seen[rel] {
			return
		}

		abs := filepath.Join(projectRoot, rel)
		info, err := os.Stat(abs)
		if err != nil || !info.IsDir() {
			return
		}

		seen[rel] = true
		roots = append(roots, rel)
	}

	add(".")

	for _, parent := range []string{"apps", "services", "packages"} {
		parentPath := filepath.Join(projectRoot, parent)
		entries, err := os.ReadDir(parentPath)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				add(filepath.Join(parent, entry.Name()))
			}
		}
	}

	for _, pattern := range workspacePatterns(projectRoot) {
		matches, err := filepath.Glob(filepath.Join(projectRoot, filepath.FromSlash(pattern)))
		if err != nil {
			continue
		}
		for _, match := range matches {
			if rel, err := filepath.Rel(projectRoot, match); err == nil {
				add(rel)
			}
		}
	}

	entries, err := os.ReadDir(projectRoot)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && looksLikeServiceDir(entry.Name()) {
				add(entry.Name())
			}
		}
	}

	sort.Strings(roots)
	return roots
}

func inspectRoot(projectRoot, relRoot string) (DetectedService, bool) {
	relRoot = cleanRoot(relRoot)
	absRoot := filepath.Join(projectRoot, relRoot)

	profile, profileSource := detectProfile(absRoot)
	port, portSource := detectPort(absRoot)
	script, scriptSource := detectScript(absRoot)

	if profile == "" && port == 0 && script == "" {
		return DetectedService{}, false
	}

	evidence := []string{}
	if profileSource != "" {
		evidence = append(evidence, profileSource)
	}
	if portSource != "" {
		evidence = append(evidence, portSource)
	}
	if scriptSource != "" {
		evidence = append(evidence, scriptSource)
	}

	status := models.StatusUnresolved
	if profile != "" && (port > 0 || script != "") {
		status = models.StatusResolved
	}

	return DetectedService{
		Name:         deriveServiceName(projectRoot, relRoot),
		Root:         relRoot,
		Port:         port,
		PortEvidence: portSource,
		Script:       script,
		Profile:      profile,
		Status:       status,
		Source:       models.SourceDetected,
		Evidence:     evidence,
	}, true
}

func detectScript(root string) (string, string) {
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err != nil {
		return "", ""
	}

	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil || strings.TrimSpace(pkg.Scripts["dev"]) == "" {
		return "", ""
	}
	return "dev", "package.json script dev"
}

func detectSiblingScriptServices(projectRoot, relRoot, primaryScript string) []DetectedService {
	data, err := os.ReadFile(filepath.Join(projectRoot, relRoot, "package.json"))
	if err != nil {
		return nil
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(data, &pkg) != nil {
		return nil
	}

	candidates := []struct {
		script string
		name   string
	}{
		{script: "server", name: "server"},
		{script: "api", name: "api"},
		{script: "dev:server", name: "server"},
		{script: "dev:api", name: "api"},
	}
	seen := map[string]bool{}
	var services []DetectedService
	for _, candidate := range candidates {
		command := strings.TrimSpace(pkg.Scripts[candidate.script])
		if command == "" || candidate.script == primaryScript || seen[candidate.name] {
			continue
		}
		seen[candidate.name] = true
		port := findPort(command)
		portEvidence := ""
		if port > 0 {
			portEvidence = "package.json script " + candidate.script
		}
		services = append(services, DetectedService{
			Name:         candidate.name,
			Root:         cleanRoot(relRoot),
			Port:         port,
			PortEvidence: portEvidence,
			Script:       candidate.script,
			Profile:      models.ProfileStandard,
			Status:       models.StatusResolved,
			Source:       models.SourceDetected,
			Evidence:     []string{"package.json script " + candidate.script},
		})
	}
	return services
}

func detectProfile(root string) (models.Profile, string) {
	for _, name := range []string{"next.config.js", "next.config.mjs", "next.config.ts", "nuxt.config.js", "nuxt.config.mjs", "nuxt.config.ts", "vite.config.js", "vite.config.mjs", "vite.config.ts", "vite.config.mts", "vue.config.js"} {
		if fileExists(filepath.Join(root, name)) {
			return models.ProfileHMR, name
		}
	}

	if pkg := readFile(filepath.Join(root, "package.json")); pkg != "" {
		lower := strings.ToLower(pkg)
		switch {
		case strings.Contains(lower, `"next"`),
			strings.Contains(lower, `"nuxt"`),
			strings.Contains(lower, `"nuxi"`),
			strings.Contains(lower, `"vite"`),
			strings.Contains(lower, `"@vitejs/`),
			strings.Contains(lower, `"react-scripts"`),
			strings.Contains(lower, `"@vue/cli-service"`),
			strings.Contains(lower, `"vue-cli-service"`):
			return models.ProfileHMR, "package.json"
		case strings.Contains(lower, `"express"`),
			strings.Contains(lower, `"hono"`),
			strings.Contains(lower, `"fastify"`),
			strings.Contains(lower, `"koa"`):
			return models.ProfileStandard, "package.json"
		}
	}

	for _, file := range []string{"pyproject.toml", "requirements.txt"} {
		if text := strings.ToLower(readFile(filepath.Join(root, file))); text != "" {
			switch {
			case strings.Contains(text, "fastapi"),
				strings.Contains(text, "django"),
				strings.Contains(text, "flask"):
				return models.ProfileStandard, file
			}
		}
	}

	if gemfile := strings.ToLower(readFile(filepath.Join(root, "Gemfile"))); strings.Contains(gemfile, `gem "rails"`) || strings.Contains(gemfile, "gem 'rails'") {
		return models.ProfileStandard, "Gemfile"
	}

	for _, name := range []string{"main.py", "app.py", "manage.py", "server.js", "server.ts"} {
		if fileExists(filepath.Join(root, name)) {
			return models.ProfileStandard, name
		}
	}

	return "", ""
}

func workspacePatterns(root string) []string {
	var patterns []string
	data, err := os.ReadFile(filepath.Join(root, "package.json"))
	if err == nil {
		var pkg struct {
			Workspaces json.RawMessage `json:"workspaces"`
		}
		if json.Unmarshal(data, &pkg) == nil && len(pkg.Workspaces) > 0 {
			var declared []string
			if json.Unmarshal(pkg.Workspaces, &declared) == nil {
				patterns = append(patterns, declared...)
			} else {
				var object struct {
					Packages []string `json:"packages"`
				}
				if json.Unmarshal(pkg.Workspaces, &object) == nil {
					patterns = append(patterns, object.Packages...)
				}
			}
		}
	}
	patterns = append(patterns, pnpmWorkspacePatterns(filepath.Join(root, "pnpm-workspace.yaml"))...)

	seen := map[string]bool{}
	result := make([]string, 0, len(patterns))
	for _, pattern := range patterns {
		pattern = strings.Trim(strings.TrimSpace(pattern), `"'`)
		if pattern == "" || strings.HasPrefix(pattern, "!") || seen[pattern] {
			continue
		}
		seen[pattern] = true
		result = append(result, pattern)
	}
	return result
}

func pnpmWorkspacePatterns(path string) []string {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	inPackages := false
	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "\t") {
			inPackages = trimmed == "packages:"
			continue
		}
		if inPackages && strings.HasPrefix(trimmed, "-") {
			patterns = append(patterns, strings.TrimSpace(strings.TrimPrefix(trimmed, "-")))
		}
	}
	return patterns
}

func detectPort(root string) (int, string) {
	for _, envFile := range []string{".env.development.local", ".env.local", ".env.development", ".env"} {
		if port := findPort(readFile(filepath.Join(root, envFile))); port > 0 {
			return port, envFile
		}
	}

	for _, file := range []string{
		"package.json",
		"vite.config.ts",
		"vite.config.js",
		"vite.config.mts",
		"vite.config.mjs",
		"nuxt.config.ts",
		"nuxt.config.js",
		"next.config.js",
		"next.config.mjs",
		"next.config.ts",
		"vue.config.js",
		"main.py",
		"app.py",
		"manage.py",
		"server.js",
		"server.ts",
	} {
		if port := findPort(readFile(filepath.Join(root, file))); port > 0 {
			return port, file
		}
	}

	return 0, ""
}

func findPort(text string) int {
	if text == "" {
		return 0
	}

	for _, pattern := range []*regexp.Regexp{envPortPattern, inlinePortPattern, cliPortPattern, structuredPortPattern, localhostPortPattern} {
		match := pattern.FindStringSubmatch(text)
		if len(match) == 2 {
			return atoi(match[1])
		}
	}

	return 0
}

func deriveServiceName(projectRoot, relRoot string) string {
	if relRoot == "." {
		return serviceNameOrFallback(filepath.Base(projectRoot))
	}
	return serviceNameOrFallback(filepath.Base(relRoot))
}

func serviceNameOrFallback(raw string) string {
	if name := NormalizeName(raw); name != "" {
		return name
	}
	return "service"
}

func looksLikeServiceDir(name string) bool {
	switch strings.ToLower(name) {
	case "api", "app", "apps", "backend", "client", "frontend", "packages", "server", "services", "web", "www":
		return true
	default:
		return false
	}
}

func uniqueName(name string, used map[string]int) string {
	base := NormalizeName(name)
	if base == "" {
		base = "service"
	}

	used[base]++
	if used[base] == 1 {
		return base
	}
	return base + "-" + itoa(used[base])
}

// NormalizeName converts an arbitrary project or service name into one ASCII
// DNS label. It is the canonical name normalizer used by discovery and plans.
func NormalizeName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	name = strings.ReplaceAll(name, "_", "-")

	var b strings.Builder
	lastDash := false
	for _, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}

	return strings.Trim(b.String(), "-")
}

func cleanRoot(root string) string {
	root = strings.TrimSpace(root)
	if root == "" || root == "." {
		return "."
	}
	return filepath.Clean(root)
}

func readFile(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func atoi(value string) int {
	port := 0
	for _, r := range value {
		if r < '0' || r > '9' {
			return 0
		}
		port = port*10 + int(r-'0')
	}
	return port
}

func itoa(value int) string {
	data, _ := json.Marshal(value)
	return string(data)
}
