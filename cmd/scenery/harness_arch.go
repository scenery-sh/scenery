package main

import (
	"bytes"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"golang.org/x/mod/modfile"
)

const (
	architectureWarnLines  = 1000
	architectureErrorLines = 2500
)

var allowedDirectGoDeps = map[string]string{
	"github.com/fsnotify/fsnotify":       "file watching for scenery up live rebuilds",
	"github.com/golang-jwt/jwt/v5":       "JWT signing and verification for standard auth",
	"github.com/google/uuid":             "UUID generation and parsing for standard auth database records",
	"github.com/gorilla/websocket":       "dashboard JSON-RPC websocket transport",
	"github.com/jackc/pgx/v5":            "Postgres pgxpool compatibility wrapper for scenery apps",
	"github.com/lib/pq":                  "Postgres database explorer and psql URL handling",
	"go.temporal.io/api":                 "Temporal API types used by deployment and scheduling integrations",
	"go.temporal.io/sdk":                 "Temporal client and worker SDK for the durable execution runtime",
	"go.temporal.io/sdk/contrib/sysinfo": "Temporal-recommended host and cgroup resource reporting for worker heartbeats",
	"golang.org/x/crypto":                "password hashing primitives for standard auth",
	"golang.org/x/mod":                   "Go module parsing for self-harness dependency checks",
	"golang.org/x/sys":                   "portable OS syscalls for doctor disk and memory readiness probes",
	"golang.org/x/tools":                 "Go package loading/parser pipeline",
	"gopkg.in/yaml.v3":                   "SQLC generator graph inspection from sqlc.yaml without shell parsing",
}

var forbiddenSourceImports = map[string]string{
	"github.com/julienschmidt/httprouter": "scenery uses the standard-library router/runtime routing instead of httprouter.",
	"github.com/spf13/cobra":              "scenery CLI intentionally stays hand-rolled to avoid framework surface area.",
	"github.com/urfave/cli":               "scenery CLI intentionally stays hand-rolled to avoid framework surface area.",
	"github.com/fatih/color":              "scenery terminal styling uses internal/termstyle instead of a color dependency.",
	"github.com/charmbracelet/lipgloss":   "scenery terminal styling uses internal/termstyle instead of a UI framework dependency.",
}

var removedAgentTransportTerms = []string{
	"m" + "cp",
	"r" + "m" + "cp",
	"model context" + " protocol",
	"m" + "cp_host",
	"m" + "cpservers",
	"m" + "cp_servers",
	"experimental_use_r" + "m" + "cp_client",
	"chrome-devtools-" + "m" + "cp",
	"sse" + "?appid",
}

var removedAgentTransportToken = "m" + "cp"
var removedAgentTransportTokenWithPrefix = "r" + removedAgentTransportToken

type packageLayerRule struct {
	Name             string
	PathPrefixes     []string
	ForbiddenImports []string
}

var packageLayerRules = []packageLayerRule{
	{
		Name:         "runtime packages stay independent from CLI/dev dashboard",
		PathPrefixes: []string{"runtime/"},
		ForbiddenImports: []string{
			"scenery.sh/cmd/scenery",
			"scenery.sh/internal/devdash",
		},
	},
	{
		Name:         "internal/build stays below CLI",
		PathPrefixes: []string{"internal/build/"},
		ForbiddenImports: []string{
			"scenery.sh/cmd/scenery",
		},
	},
	{
		Name:         "runtimeapp excludes dev-only packages",
		PathPrefixes: []string{"runtimeapp/"},
		ForbiddenImports: []string{
			"scenery.sh/cmd/scenery",
			"scenery.sh/internal/devdash",
		},
	},
	{
		Name:         "localproxy stays independent from app build/runtime internals",
		PathPrefixes: []string{"internal/localproxy/"},
		ForbiddenImports: []string{
			"scenery.sh/cmd/scenery",
			"scenery.sh/internal/build",
			"scenery.sh/runtimeapp",
		},
	},
}

func runHarnessArchitectureStep(repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "architecture checks",
		Command: []string{"scenery", "harness", "self", "internal:architecture-check", repoRoot},
		Summary: map[string]any{
			"max_warning_lines": architectureWarnLines,
			"max_error_lines":   architectureErrorLines,
		},
	}

	var diagnostics []checkDiagnostic
	summary := architectureSummary{}
	diagnostics = append(diagnostics, checkArchitectureDependencies(repoRoot, &summary)...)
	sourceDiagnostics, err := checkArchitectureSource(repoRoot, &summary)
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           step.Name,
			Severity:        "error",
			Message:         err.Error(),
			SuggestedAction: "Fix the source walk error, then rerun `scenery harness self --json`.",
		})
	} else {
		diagnostics = append(diagnostics, sourceDiagnostics...)
	}
	diagnostics = append(diagnostics, checkArchitectureGeneratedHygiene(repoRoot, &summary)...)

	errorCount, warningCount := countDiagnosticsBySeverity(diagnostics)
	step.Summary["checked_files"] = summary.CheckedFiles
	step.Summary["source_files"] = summary.SourceFiles
	step.Summary["direct_dependencies"] = summary.DirectDependencies
	step.Summary["indirect_dependencies"] = summary.IndirectDependencies
	step.Summary["large_files"] = summary.LargeFiles
	step.Summary["warnings"] = warningCount
	step.Summary["errors"] = errorCount
	step.Diagnostics = diagnostics
	step.OK = errorCount == 0
	step.DurationMS = time.Since(started).Milliseconds()
	return step
}

type architectureSummary struct {
	CheckedFiles         int
	SourceFiles          int
	DirectDependencies   int
	IndirectDependencies int
	LargeFiles           int
}

func checkArchitectureDependencies(repoRoot string, summary *architectureSummary) []checkDiagnostic {
	data, err := os.ReadFile(filepath.Join(repoRoot, "go.mod"))
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "architecture checks",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "go.mod")),
			Message:         err.Error(),
			SuggestedAction: "Restore go.mod, then rerun `scenery harness self --json`.",
		}}
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "architecture checks",
			Severity:        "error",
			File:            filepath.ToSlash(filepath.Join(repoRoot, "go.mod")),
			Message:         err.Error(),
			SuggestedAction: "Fix go.mod syntax, then rerun `scenery harness self --json`.",
		}}
	}
	var diagnostics []checkDiagnostic
	for _, req := range parsed.Require {
		if req.Indirect {
			summary.IndirectDependencies++
			continue
		}
		summary.DirectDependencies++
		if _, ok := allowedDirectGoDeps[req.Mod.Path]; !ok {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            filepath.ToSlash(filepath.Join(repoRoot, "go.mod")),
				Message:         "direct Go dependency is not in the architecture allowlist: " + req.Mod.Path,
				SuggestedAction: "Either remove the dependency or add it to allowedDirectGoDeps with a concrete rationale.",
			})
		}
	}
	return diagnostics
}

func checkArchitectureSource(repoRoot string, summary *architectureSummary) ([]checkDiagnostic, error) {
	var diagnostics []checkDiagnostic
	err := filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			if architectureSkipDir(rel) {
				return filepath.SkipDir
			}
			return nil
		}
		if architectureGeneratedOrVendored(rel) {
			return nil
		}
		ext := filepath.Ext(rel)
		if !slices.Contains([]string{".go", ".ts", ".tsx", ".md", ".json"}, ext) {
			return nil
		}
		summary.CheckedFiles++
		lineCount, err := countFileLines(path)
		if err != nil {
			return err
		}
		if lineCount >= architectureErrorLines && !architectureAllowsLongFile(rel) {
			summary.LargeFiles++
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            rel,
				Message:         fmt.Sprintf("file has %d lines, over hard limit %d", lineCount, architectureErrorLines),
				SuggestedAction: "Split the file before adding more behavior.",
			})
		} else if lineCount >= architectureWarnLines && !architectureAllowsLongFile(rel) {
			summary.LargeFiles++
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "warning",
				File:            rel,
				Message:         fmt.Sprintf("file has %d lines, over warning threshold %d", lineCount, architectureWarnLines),
				SuggestedAction: "Prefer splitting this file when editing the same area.",
			})
		}
		if ext == ".go" {
			summary.SourceFiles++
			importDiagnostics, err := checkArchitectureGoImports(path, rel)
			if err != nil {
				return err
			}
			diagnostics = append(diagnostics, importDiagnostics...)
		}
		diagnostics = append(diagnostics, checkRemovedAgentTransportTerms(path, rel)...)
		return nil
	})
	return diagnostics, err
}

func checkRemovedAgentTransportTerms(path, rel string) []checkDiagnostic {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	lines := strings.Split(string(data), "\n")
	var diagnostics []checkDiagnostic
	for index, line := range lines {
		lower := strings.ToLower(line)
		for _, term := range removedAgentTransportTerms {
			if containsRemovedAgentTransportTerm(lower, term) {
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "architecture checks",
					Severity:        "error",
					File:            rel,
					Line:            index + 1,
					Message:         "text references removed agent transport",
					SuggestedAction: "Delete the reference or rewrite the workflow to use scenery inspect/status/logs/db/run/check commands.",
				})
				break
			}
		}
	}
	return diagnostics
}

func containsRemovedAgentTransportTerm(line, term string) bool {
	if term == removedAgentTransportToken || term == removedAgentTransportTokenWithPrefix {
		offset := 0
		for {
			relative := strings.Index(line[offset:], term)
			if relative < 0 {
				return false
			}
			start := offset + relative
			end := start + len(term)
			if isTokenBoundary(line, start-1) && isTokenBoundary(line, end) {
				return true
			}
			if end >= len(line) {
				return false
			}
			offset = start + 1
		}
	}
	return strings.Contains(line, term)
}

func isTokenBoundary(line string, index int) bool {
	if index < 0 || index >= len(line) {
		return true
	}
	ch := line[index]
	return !(ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' || ch == '_')
}

func architectureAllowsLongFile(rel string) bool {
	rel = filepath.ToSlash(rel)
	switch filepath.Ext(rel) {
	case ".json":
		return true
	case ".md":
		return true
	default:
		return false
	}
}

func checkArchitectureGoImports(path, rel string) ([]checkDiagnostic, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
	if err != nil {
		return nil, err
	}
	var diagnostics []checkDiagnostic
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if reason, ok := forbiddenSourceImports[importPath]; ok {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            rel,
				Message:         "forbidden import " + importPath + ": " + reason,
				SuggestedAction: "Use the existing scenery standard-library/internal implementation instead.",
			})
		}
		if importPath == "C" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "warning",
				File:            rel,
				Message:         "cgo import detected",
				SuggestedAction: "Keep cgo isolated and document the native build requirement.",
			})
		}
		if importPath == "scenery.sh/cmd/scenery" && !strings.HasPrefix(rel, "cmd/scenery/") {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            rel,
				Message:         "non-CLI package imports scenery.sh/cmd/scenery",
				SuggestedAction: "Move shared code into internal/ instead of importing the CLI package.",
			})
		}
		for _, rule := range packageLayerRules {
			if !pathMatchesLayerRule(rel, rule) {
				continue
			}
			for _, forbidden := range rule.ForbiddenImports {
				if importPath == forbidden {
					diagnostics = append(diagnostics, checkDiagnostic{
						Stage:           "architecture checks",
						Severity:        "error",
						File:            rel,
						Message:         "package layer violation: " + rule.Name + " forbids import " + importPath,
						SuggestedAction: "Move shared code to a lower-level internal package or invert the dependency.",
					})
				}
			}
		}
	}
	return diagnostics, nil
}

func pathMatchesLayerRule(rel string, rule packageLayerRule) bool {
	rel = filepath.ToSlash(rel)
	for _, prefix := range rule.PathPrefixes {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func checkArchitectureGeneratedHygiene(repoRoot string, summary *architectureSummary) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	requiredGitignore := []string{
		"/oracle/",
		"/coverage/",
		".scenery/",
		".DS_Store",
		"node_modules/",
	}
	gitignore := readOptionalText(filepath.Join(repoRoot, ".gitignore"))
	for _, pattern := range requiredGitignore {
		if !strings.Contains(gitignore, pattern) {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            ".gitignore",
				Message:         ".gitignore missing required pattern: " + pattern,
				SuggestedAction: "Add the missing ignore pattern so local/generated artifacts stay out of git.",
			})
		}
	}
	requiredAttributes := []string{
		"cmd/scenery/devdash_static/** -diff",
		"ui/public/assets/** -diff",
		"ui/dist/** -diff",
	}
	gitattributes := readOptionalText(filepath.Join(repoRoot, ".gitattributes"))
	for _, pattern := range requiredAttributes {
		if !strings.Contains(gitattributes, pattern) {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "architecture checks",
				Severity:        "error",
				File:            ".gitattributes",
				Message:         ".gitattributes missing generated/vendored marker: " + pattern,
				SuggestedAction: "Mark generated or vendored trees in .gitattributes to keep diffs reviewable.",
			})
		}
	}
	dsStoreCount := 0
	var dsStoreExamples []string
	_ = filepath.WalkDir(repoRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if architectureSkipDir(filepath.Dir(rel)) {
			return nil
		}
		if filepath.Base(rel) == ".DS_Store" {
			dsStoreCount++
			if len(dsStoreExamples) < 5 {
				dsStoreExamples = append(dsStoreExamples, rel)
			}
		}
		return nil
	})
	if dsStoreCount > 0 {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "architecture checks",
			Severity:        "warning",
			File:            "$REPO",
			Message:         fmt.Sprintf("%d macOS .DS_Store artifacts exist in the working tree: %s", dsStoreCount, strings.Join(dsStoreExamples, ", ")),
			SuggestedAction: "Delete .DS_Store artifacts when touching nearby files.",
		})
	}
	return diagnostics
}

func architectureSkipDir(rel string) bool {
	rel = filepath.ToSlash(rel)
	if rel == "." {
		return false
	}
	for _, part := range strings.Split(rel, "/") {
		if part == ".scenery" {
			return true
		}
	}
	for _, prefix := range []string{
		".claude",
		".git",
		".scenery",
		".codex-tmp",
		"coverage",
		"oracle",
		"node_modules",
		"ui/node_modules",
	} {
		if rel == prefix || strings.HasPrefix(rel, prefix+"/") {
			return true
		}
	}
	return false
}

func architectureGeneratedOrVendored(rel string) bool {
	rel = filepath.ToSlash(rel)
	for _, prefix := range []string{
		"cmd/scenery/devdash_static/",
		"ui/public/assets/",
		"ui/dist/",
	} {
		if strings.HasPrefix(rel, prefix) {
			return true
		}
	}
	return false
}

func countFileLines(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if len(data) == 0 {
		return 0, nil
	}
	count := bytes.Count(data, []byte{'\n'})
	if !bytes.HasSuffix(data, []byte{'\n'}) {
		count++
	}
	return count, nil
}

func readOptionalText(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(data)
}

func countDiagnosticsBySeverity(diagnostics []checkDiagnostic) (errors int, warnings int) {
	for _, diag := range diagnostics {
		switch diag.Severity {
		case "error":
			errors++
		case "warning":
			warnings++
		}
	}
	return errors, warnings
}
