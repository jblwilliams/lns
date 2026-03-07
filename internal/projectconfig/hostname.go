package projectconfig

import (
	"fmt"
	"strings"
	"unicode"
)

func NormalizeHostnameInput(input string) (string, error) {
	s := strings.TrimSpace(input)
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSpace(s)

	if s == "" {
		return "", fmt.Errorf("hostname cannot be empty")
	}

	if host, port, ok := strings.Cut(s, ":"); ok {
		switch {
		case host == "":
			return "", fmt.Errorf("hostname cannot be empty")
		case port != "":
			s = host
		default:
			return "", fmt.Errorf("hostname must not include a trailing colon")
		}
	}

	if !strings.Contains(s, ".") {
		s += ".localhost"
	}

	return ValidateExplicitHostname(s)
}

func ValidateExplicitHostname(input string) (string, error) {
	s := strings.ToLower(strings.TrimSpace(input))
	if s == "" {
		return "", fmt.Errorf("hostname cannot be empty")
	}
	if strings.Contains(s, "://") {
		return "", fmt.Errorf("hostname must not include a URL scheme")
	}
	if strings.ContainsAny(s, "/\\") {
		return "", fmt.Errorf("hostname must not contain slashes")
	}
	if strings.ContainsAny(s, " \t\r\n") {
		return "", fmt.Errorf("hostname cannot contain whitespace")
	}
	if strings.Contains(s, ":") {
		return "", fmt.Errorf("hostname must not include a port")
	}
	if strings.HasPrefix(s, ".") || strings.HasSuffix(s, ".") {
		return "", fmt.Errorf("hostname must not start or end with a dot")
	}

	labels := strings.Split(s, ".")
	for _, label := range labels {
		if err := validateHostnameLabel(label); err != nil {
			return "", err
		}
	}

	return s, nil
}

func validateHostnameLabel(label string) error {
	if label == "" {
		return fmt.Errorf("hostname labels cannot be empty")
	}
	if len(label) > 63 {
		return fmt.Errorf("hostname label %q is too long", label)
	}
	if label[0] == '-' || label[len(label)-1] == '-' {
		return fmt.Errorf("hostname label %q must not start or end with '-'", label)
	}

	for _, r := range label {
		if unicode.IsLower(r) || unicode.IsDigit(r) || r == '-' {
			continue
		}
		return fmt.Errorf("hostname label %q contains invalid characters", label)
	}

	return nil
}
