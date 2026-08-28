package devrun

import (
	"sort"
	"strings"
)

// OverlayEnvironment returns one entry per key. Later base entries win, then
// explicit overrides win. Existing key order is retained and new keys sort.
func OverlayEnvironment(base []string, overrides map[string]string) []string {
	values := make(map[string]string, len(base)+len(overrides))
	order := make([]string, 0, len(base)+len(overrides))
	seen := make(map[string]bool, len(base)+len(overrides))
	for _, item := range base {
		key, value, ok := strings.Cut(item, "=")
		if !ok || key == "" {
			continue
		}
		if !seen[key] {
			order = append(order, key)
			seen[key] = true
		}
		values[key] = value
	}
	keys := make([]string, 0, len(overrides))
	for key := range overrides {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if !seen[key] {
			order = append(order, key)
		}
		values[key] = overrides[key]
	}

	result := make([]string, 0, len(order))
	for _, key := range order {
		result = append(result, key+"="+values[key])
	}
	return result
}
