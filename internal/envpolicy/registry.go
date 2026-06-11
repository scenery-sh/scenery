package envpolicy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

const (
	SchemaVersion = "scenery.environment.registry.v1"
	RedactedValue = "<redacted>"
)

type Registry struct {
	SchemaVersion string     `json:"schema_version"`
	Variables     []Variable `json:"variables"`

	exact    map[string]Variable
	patterns []Variable
}

type Variable struct {
	Name             string   `json:"name"`
	Match            string   `json:"match,omitempty"`
	Scope            string   `json:"scope"`
	Direction        string   `json:"direction"`
	Category         string   `json:"category"`
	Stability        string   `json:"stability"`
	Secret           bool     `json:"secret"`
	AllowedIn        []string `json:"allowed_in"`
	Owner            string   `json:"owner"`
	Rationale        string   `json:"rationale"`
	PreferredSurface string   `json:"preferred_surface"`
	Replacement      string   `json:"replacement,omitempty"`
	Sunset           string   `json:"sunset,omitempty"`
	Docs             []string `json:"docs"`
}

func RegistryPath(repoRoot string) string {
	return filepath.Join(repoRoot, "docs", "environment.registry.json")
}

func LoadRegistry(path string) (*Registry, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var registry Registry
	if err := json.Unmarshal(data, &registry); err != nil {
		return nil, err
	}
	if err := registry.Validate(); err != nil {
		return nil, err
	}
	registry.index()
	return &registry, nil
}

func (r *Registry) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return fmt.Errorf("environment registry schema_version = %q, want %q", r.SchemaVersion, SchemaVersion)
	}
	seen := map[string]struct{}{}
	for i, variable := range r.Variables {
		if strings.TrimSpace(variable.Name) == "" {
			return fmt.Errorf("environment registry variable %d has empty name", i)
		}
		match := variable.matchMode()
		if match != "exact" && match != "prefix" && match != "glob" {
			return fmt.Errorf("environment registry variable %s has invalid match %q", variable.Name, variable.Match)
		}
		key := match + ":" + variable.Name
		if _, ok := seen[key]; ok {
			return fmt.Errorf("environment registry duplicate variable entry: %s", variable.Name)
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(variable.Scope) == "" ||
			strings.TrimSpace(variable.Direction) == "" ||
			strings.TrimSpace(variable.Category) == "" ||
			strings.TrimSpace(variable.Stability) == "" ||
			strings.TrimSpace(variable.Owner) == "" ||
			strings.TrimSpace(variable.Rationale) == "" {
			return fmt.Errorf("environment registry variable %s is missing required metadata", variable.Name)
		}
		if len(variable.AllowedIn) == 0 {
			return fmt.Errorf("environment registry variable %s has no allowed_in scopes", variable.Name)
		}
	}
	return nil
}

func (r *Registry) Find(name string) (Variable, bool) {
	if r.exact == nil {
		r.index()
	}
	if variable, ok := r.exact[name]; ok {
		return variable, true
	}
	for _, variable := range r.patterns {
		if variable.matches(name) {
			return variable, true
		}
	}
	return Variable{}, false
}

func (r *Registry) RedactValue(name, value string) string {
	variable, registered := r.Find(name)
	if registered && variable.Secret {
		return RedactedValue
	}
	if SecretLikeName(name) {
		return RedactedValue
	}
	return value
}

func (r *Registry) index() {
	r.exact = map[string]Variable{}
	r.patterns = nil
	for _, variable := range r.Variables {
		switch variable.matchMode() {
		case "prefix", "glob":
			r.patterns = append(r.patterns, variable)
		default:
			r.exact[variable.Name] = variable
		}
	}
	sort.Slice(r.patterns, func(i, j int) bool {
		return len(r.patterns[i].Name) > len(r.patterns[j].Name)
	})
}

func (v Variable) Allows(scope string) bool {
	return slices.Contains(v.AllowedIn, scope)
}

func (v Variable) matchMode() string {
	if v.Match == "" {
		return "exact"
	}
	return v.Match
}

func (v Variable) matches(name string) bool {
	switch v.matchMode() {
	case "prefix":
		return strings.HasPrefix(name, v.Name)
	case "glob":
		return globMatch(v.Name, name)
	default:
		return v.Name == name
	}
}

func SecretLikeName(name string) bool {
	normalized := strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
	for _, part := range []string{"SECRET", "TOKEN", "API_KEY", "PASSWORD", "PRIVATE_KEY", "DATABASE_URL", "AUTH_DATABASE_URL", "JWT"} {
		if strings.Contains(normalized, part) {
			return true
		}
	}
	return false
}

func globMatch(pattern, value string) bool {
	if pattern == "*" {
		return true
	}
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == value
	}
	if !strings.HasPrefix(value, parts[0]) {
		return false
	}
	pos := len(parts[0])
	for _, part := range parts[1 : len(parts)-1] {
		next := strings.Index(value[pos:], part)
		if next < 0 {
			return false
		}
		pos += next + len(part)
	}
	last := parts[len(parts)-1]
	return last == "" || strings.HasSuffix(value[pos:], last)
}
