package envpolicy

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

type Reference struct {
	File  string `json:"file"`
	Kind  string `json:"kind"`
	Scope string `json:"scope"`
}

type ScanOptions struct {
	RepoRoot string
	SkipDir  func(relDir string) bool
}

type ScanResult struct {
	Variables map[string][]Reference
}

func Scan(opts ScanOptions) ScanResult {
	result := ScanResult{Variables: map[string][]Reference{}}
	_ = filepath.WalkDir(opts.RepoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		rel, relErr := filepath.Rel(opts.RepoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if rel != "." && opts.SkipDir != nil && opts.SkipDir(rel) {
				return filepath.SkipDir
			}
			if rel == "internal/envpolicy" {
				return filepath.SkipDir
			}
			return nil
		}
		if !scannableEnvFile(rel) {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		scope := fileScope(rel)
		for _, token := range extractEnvTokens(string(data)) {
			ref := Reference{File: rel, Kind: referenceKind(string(data), token), Scope: scope}
			result.Variables[token] = appendReference(result.Variables[token], ref)
		}
		return nil
	})
	for name := range result.Variables {
		sort.Slice(result.Variables[name], func(i, j int) bool {
			a, b := result.Variables[name][i], result.Variables[name][j]
			if a.File != b.File {
				return a.File < b.File
			}
			if a.Scope != b.Scope {
				return a.Scope < b.Scope
			}
			return a.Kind < b.Kind
		})
	}
	return result
}

func VariableNames(result ScanResult) []string {
	names := make([]string, 0, len(result.Variables))
	for name := range result.Variables {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func EffectiveScope(refs []Reference, name string) string {
	scope := ""
	for _, ref := range refs {
		switch ref.Scope {
		case "code":
			return "runtime"
		case "fixture":
			if scope == "" || scope == "docs" || scope == "test" {
				scope = "fixture"
			}
		case "test":
			if scope == "" || scope == "docs" {
				scope = "test"
			}
		case "docs":
			if scope == "" {
				scope = "docs"
			}
		}
	}
	if scope == "" {
		return "runtime"
	}
	if (strings.HasPrefix(name, "SCENERY_TEST_") || strings.HasPrefix(name, "SCENERY_INTEGRATION_")) && scope != "docs" {
		return "test"
	}
	return scope
}

func ScopeAllowedInRegistry(scope string) string {
	switch scope {
	case "runtime":
		return "code"
	case "fixture", "test":
		return "tests"
	default:
		return scope
	}
}

func scannableEnvFile(rel string) bool {
	switch filepath.Ext(rel) {
	case ".go", ".sh", ".mjs", ".js", ".ts", ".tsx", ".md", ".json", ".hcl", ".sql":
		return true
	default:
		return false
	}
}

func fileScope(rel string) string {
	if strings.HasPrefix(rel, "docs/") || rel == "README.md" || rel == "AGENTS.md" || rel == "SKILL.md" || rel == "PLANS.md" || rel == "PLAN.md" {
		return "docs"
	}
	if strings.HasPrefix(rel, "internal/testpostgres/") {
		return "test"
	}
	if strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "_test.") {
		return "test"
	}
	if strings.HasPrefix(rel, "testdata/") || strings.HasPrefix(rel, "benchmarks/") {
		return "fixture"
	}
	return "code"
}

func extractEnvTokens(text string) []string {
	set := map[string]struct{}{}
	for i := 0; i < len(text); i++ {
		ch := text[i]
		if !isEnvStart(ch) {
			continue
		}
		j := i + 1
		for j < len(text) && isEnvChar(text[j]) {
			j++
		}
		token := text[i:j]
		if looksLikeEnvName(token) {
			set[token] = struct{}{}
		}
		i = j
	}
	return sortedSet(set)
}

func looksLikeEnvName(token string) bool {
	if len(token) < 3 || !strings.Contains(token, "_") {
		return false
	}
	if strings.Trim(token, "_") == "" {
		return false
	}
	for _, prefix := range []string{
		"SCENERY_",
	} {
		if strings.HasPrefix(token, prefix) && token != prefix {
			return true
		}
	}
	switch token {
	case "DATABASE_URL", "API_BASE_URL", "PUBLIC_APP_URL", "AUTH_COOKIE_DOMAIN", "AUTH_EMAIL_FROM", "JWT_SECRET",
		"CLICOLOR_FORCE", "NO_COLOR",
		"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "OTEL_EXPORTER_OTLP_LOGS_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"VITE_API_BASE_URL":
		return true
	default:
		return false
	}
}

func referenceKind(text, token string) string {
	idx := strings.Index(text, token)
	if idx < 0 {
		return "mention"
	}
	start := max(idx-120, 0)
	end := min(idx+len(token)+120, len(text))
	context := text[start:end]
	switch {
	case strings.Contains(context, "Setenv") || strings.Contains(context, token+"=") || strings.Contains(context, "Env:") || strings.Contains(context, "Env ="):
		return "write"
	case strings.Contains(context, "Getenv") || strings.Contains(context, "LookupEnv") || strings.Contains(context, "process.env"):
		return "read"
	default:
		return "mention"
	}
}

func appendReference(refs []Reference, ref Reference) []Reference {
	if slices.Contains(refs, ref) {
		return refs
	}
	return append(refs, ref)
}

func sortedSet(set map[string]struct{}) []string {
	out := make([]string, 0, len(set))
	for value := range set {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func isEnvStart(ch byte) bool {
	return ch >= 'A' && ch <= 'Z'
}

func isEnvChar(ch byte) bool {
	return (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_'
}
