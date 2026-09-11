package nativedurable

import (
	"fmt"
	"strings"
	"unicode"
)

func NormalizeServiceName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("durable store: service name is required")
	}
	if strings.ContainsAny(name, `\:`) || strings.HasPrefix(name, "/") {
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
