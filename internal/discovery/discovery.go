package discovery

import (
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
	Name     string
	Root     string
	Port     int
	Profile  models.Profile
	Status   models.ServiceStatus
	Source   models.ServiceSource
	Evidence []string
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
			Name:     "app",
			Root:     ".",
			Status:   models.StatusUnresolved,
			Source:   models.SourceDetected,
			Evidence: []string{"no services detected from common repo signals"},
		}}
	}

	for _, service := range detected {
		cfg.Services[service.Name] = projectconfig.Service{
			Root:    service.Root,
			Port:    service.Port,
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

		name := uniqueName(service.Name, usedNames)
		service.Name = name
		services = append(services, service)
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

	if profile == "" && port == 0 {
		return DetectedService{}, false
	}

	evidence := []string{}
	if profileSource != "" {
		evidence = append(evidence, profileSource)
	}
	if portSource != "" {
		evidence = append(evidence, portSource)
	}

	status := models.StatusUnresolved
	if profile != "" && port > 0 {
		status = models.StatusResolved
	}

	return DetectedService{
		Name:     deriveServiceName(relRoot, profile),
		Root:     relRoot,
		Port:     port,
		Profile:  profile,
		Status:   status,
		Source:   models.SourceDetected,
		Evidence: evidence,
	}, true
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
		case strings.Contains(lower, `"express"`):
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

func deriveServiceName(relRoot string, profile models.Profile) string {
	if relRoot == "." {
		if profile == models.ProfileHMR {
			return "web"
		}
		return "app"
	}

	base := strings.ToLower(filepath.Base(relRoot))
	switch base {
	case "web", "www", "client", "frontend":
		return "web"
	case "backend", "server":
		return "api"
	}

	name := sanitizeName(base)
	if name == "" {
		if profile == models.ProfileHMR {
			return "web"
		}
		return "app"
	}
	return name
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
	base := sanitizeName(name)
	if base == "" {
		base = "service"
	}

	used[base]++
	if used[base] == 1 {
		return base
	}
	return base + "-" + itoa(used[base])
}

func sanitizeName(name string) string {
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
