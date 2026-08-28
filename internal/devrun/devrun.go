package devrun

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"lns/internal/models"
)

var viteCommandPattern = regexp.MustCompile(`(?:^|[\s"';&|])vite(?:[\s"';&|]|$)`)

func ResolveCommand(projectRoot string, service models.Service, port int) ([]string, error) {
	serviceRoot := filepath.Join(projectRoot, service.Root)
	if len(service.Command) > 0 {
		args := replacePortPlaceholder(service.Command, port)
		return injectFrameworkPort(args, port), nil
	}
	if service.Script == "" {
		return nil, fmt.Errorf("service %q has no script or command", service.Name)
	}

	pkg, err := readPackage(serviceRoot)
	if err != nil {
		return nil, err
	}
	script := strings.TrimSpace(pkg.Scripts[service.Script])
	if script == "" {
		return nil, fmt.Errorf("package.json has no %q script", service.Script)
	}
	manager := packageManager(projectRoot, serviceRoot, pkg.PackageManager)
	parts := strings.Fields(script)
	if isSimpleFrameworkCommand(parts) {
		return append([]string{manager, "exec"}, injectFrameworkPort(parts, port)...), nil
	}
	return []string{manager, "run", service.Script}, nil
}

func FindFreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func PortEnvironment(projectRoot string, service models.Service, port int) []string {
	value := strconv.Itoa(port)
	result := []string{"LNS_PORT=" + value}
	if service.Script != "" {
		if pkg, err := readPackage(filepath.Join(projectRoot, service.Root)); err == nil {
			if viteCommandPattern.MatchString(pkg.Scripts[service.Script]) {
				return append(result, "VITE_PORT="+value)
			}
		}
	}

	result = append(result, "PORT="+value)
	if label := EnvironmentName(service.Name); label != "" && label != "PORT" {
		result = append(result, label+"_PORT="+value)
	}
	return result
}

func EnvironmentName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

func DetectWorktreePrefix(root string) string {
	gitDir, err := gitPath(root, "--git-dir")
	if err != nil {
		return ""
	}
	commonDir, err := gitPath(root, "--git-common-dir")
	if err != nil || samePath(gitDir, commonDir) {
		return ""
	}
	output, err := exec.Command("git", "-C", root, "branch", "--show-current").Output()
	if err != nil {
		return ""
	}
	return worktreeLabel(strings.TrimSpace(string(output)), root)
}

func worktreeLabel(branch, root string) string {
	if label := sanitizeLabel(branch); label != "" {
		return label
	}
	return sanitizeLabel(filepath.Base(filepath.Clean(root)))
}

func ApplyWorktreePrefix(hostname, prefix string) string {
	prefix = sanitizeLabel(prefix)
	if prefix == "" {
		return hostname
	}
	return prefix + "." + hostname
}

type packageJSON struct {
	PackageManager string            `json:"packageManager"`
	Scripts        map[string]string `json:"scripts"`
}

func readPackage(root string) (packageJSON, error) {
	path := filepath.Join(root, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return packageJSON{}, fmt.Errorf("read %s: %w", path, err)
	}
	var pkg packageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return packageJSON{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return pkg, nil
}

func packageManager(projectRoot, serviceRoot, declared string) string {
	if name := strings.Split(strings.TrimSpace(declared), "@")[0]; name == "npm" || name == "pnpm" || name == "yarn" || name == "bun" {
		return name
	}
	for dir := serviceRoot; ; dir = filepath.Dir(dir) {
		for file, manager := range map[string]string{
			"pnpm-lock.yaml":    "pnpm",
			"yarn.lock":         "yarn",
			"bun.lock":          "bun",
			"bun.lockb":         "bun",
			"package-lock.json": "npm",
		} {
			if _, err := os.Stat(filepath.Join(dir, file)); err == nil {
				return manager
			}
		}
		if samePath(dir, projectRoot) || filepath.Dir(dir) == dir {
			break
		}
	}
	return "npm"
}

func replacePortPlaceholder(args []string, port int) []string {
	result := make([]string, len(args))
	for i, arg := range args {
		result[i] = strings.ReplaceAll(arg, "{port}", strconv.Itoa(port))
	}
	return result
}

func injectFrameworkPort(args []string, port int) []string {
	if len(args) == 0 || !isSupportedFramework(filepath.Base(args[0])) {
		return args
	}

	framework := filepath.Base(args[0])
	hostFlag := "--host"
	if framework == "next" {
		hostFlag = "--hostname"
	}
	portValue := strconv.Itoa(port)
	result := make([]string, 0, len(args)+4)
	foundPort := false
	foundHost := false
	for i := 0; i < len(args); i++ {
		switch arg := args[i]; {
		case arg == "--port" || arg == "-p":
			result = append(result, arg, portValue)
			foundPort = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case strings.HasPrefix(arg, "--port="):
			result = append(result, "--port="+portValue)
			foundPort = true
		case arg == "--host" || arg == "--hostname" || arg == "-H":
			result = append(result, arg, "127.0.0.1")
			foundHost = true
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
			}
		case strings.HasPrefix(arg, "--host=") || strings.HasPrefix(arg, "--hostname="):
			result = append(result, strings.SplitN(arg, "=", 2)[0]+"=127.0.0.1")
			foundHost = true
		default:
			result = append(result, arg)
		}
	}
	if !foundPort {
		result = append(result, "--port", portValue)
	}
	if !foundHost {
		result = append(result, hostFlag, "127.0.0.1")
	}
	return result
}

func isSimpleFrameworkCommand(parts []string) bool {
	if len(parts) == 0 || !isSupportedFramework(filepath.Base(parts[0])) {
		return false
	}
	for _, part := range parts {
		if strings.ContainsAny(part, ";&|><") {
			return false
		}
	}
	return true
}

func isSupportedFramework(name string) bool {
	return name == "vite" || name == "next" || name == "nuxt" || name == "nuxi"
}

func gitPath(root, flag string) (string, error) {
	output, err := exec.Command("git", "-C", root, "rev-parse", flag).Output()
	if err != nil {
		return "", err
	}
	path := strings.TrimSpace(string(output))
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}
	return filepath.Abs(path)
}

func samePath(a, b string) bool {
	absA, _ := filepath.Abs(a)
	absB, _ := filepath.Abs(b)
	return filepath.Clean(absA) == filepath.Clean(absB)
}

func sanitizeLabel(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	previousDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			previousDash = false
		} else if !previousDash {
			b.WriteByte('-')
			previousDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}
