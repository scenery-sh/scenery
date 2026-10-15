package connectionapi

import (
	"fmt"
	"slices"
	"strings"
)

// NormalizeScopes preserves the case and meaning of opaque OAuth scope tokens.
// Each input is one RFC 6749 scope-token, not a whitespace-separated expression.
func NormalizeScopes(scopes []string) ([]string, error) {
	out := slices.Clone(scopes)
	for _, scope := range out {
		if scope == "" {
			return nil, fmt.Errorf("scope must not be empty")
		}
		for _, r := range scope {
			if r < 0x21 || r > 0x7e || r == '"' || r == '\\' {
				return nil, fmt.Errorf("invalid scope token")
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func ParseScopes(value string) ([]string, error) {
	if value == "" {
		return []string{}, nil
	}
	return NormalizeScopes(strings.Split(value, " "))
}

func ContainsScopes(have, required []string) bool {
	for _, scope := range required {
		if !slices.Contains(have, scope) {
			return false
		}
	}
	return true
}
