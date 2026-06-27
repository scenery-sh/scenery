package store

import (
	"fmt"
	"path/filepath"
	"strings"
	"unicode"
)

// NormalizeServiceName returns the filesystem-safe service name used in durable DB filenames.
func NormalizeServiceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("durable store: service name is required")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf("durable store: service name %q must not be an absolute path", name)
	}
	if strings.ContainsAny(name, `\:`) {
		return "", fmt.Errorf("durable store: service name %q contains an unsafe character", name)
	}

	parts := strings.Split(name, "/")
	for _, part := range parts {
		if part == "" || part == "." || part == ".." {
			return "", fmt.Errorf("durable store: service name %q contains an unsafe path segment", name)
		}
		for _, r := range part {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_' || r == '.' {
				continue
			}
			return "", fmt.Errorf("durable store: service name %q contains unsupported character %q", name, r)
		}
	}

	return strings.Join(parts, "-"), nil
}

// DurableDBPath returns <stateRoot>/db/<service>.durable.sqlite.
func DurableDBPath(stateRoot, serviceName string) (string, error) {
	if strings.TrimSpace(stateRoot) == "" {
		return "", fmt.Errorf("durable store: state root is required")
	}
	name, err := NormalizeServiceName(serviceName)
	if err != nil {
		return "", err
	}
	return filepath.Join(stateRoot, "db", name+".durable.sqlite"), nil
}
