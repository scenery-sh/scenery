package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func runHarnessUIStaticStep(repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "ui static architecture",
		Command: []string{"scenery", "harness", "self", "internal:ui-static-check", repoRoot},
		Summary: map[string]any{
			"registry_namespace": "@scenery",
		},
	}
	var summary uiStaticSummary
	diagnostics := checkUIStatic(repoRoot, &summary)
	errors, warnings := countDiagnosticsBySeverity(diagnostics)
	step.Summary["checked_files"] = summary.CheckedFiles
	step.Summary["registry_items"] = summary.RegistryItems
	step.Summary["script_checks"] = summary.ScriptChecks
	step.Summary["class_warnings"] = summary.ClassWarnings
	step.Summary["errors"] = errors
	step.Summary["warnings"] = warnings
	step.Diagnostics = diagnostics
	step.OK = errors == 0
	step.DurationMS = time.Since(started).Milliseconds()
	return step
}

type uiStaticSummary struct {
	CheckedFiles  int
	RegistryItems int
	ScriptChecks  int
	ClassWarnings int
}

func checkUIStatic(repoRoot string, summary *uiStaticSummary) []checkDiagnostic {
	uiRoot := filepath.Join(repoRoot, "ui")
	var diagnostics []checkDiagnostic
	diagnostics = append(diagnostics, checkUIComponentsJSON(uiRoot, summary)...)
	diagnostics = append(diagnostics, checkUIPackageScripts(uiRoot, summary)...)
	diagnostics = append(diagnostics, checkUIRegistryItems(uiRoot, summary)...)
	sourceDiagnostics, err := checkUISourceBoundaries(uiRoot, summary)
	if err != nil {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(uiRoot),
			Message:         err.Error(),
			SuggestedAction: "Fix the UI source walk error, then rerun `scenery harness self -o json`.",
		})
	} else {
		diagnostics = append(diagnostics, sourceDiagnostics...)
	}
	return diagnostics
}

func checkUIComponentsJSON(uiRoot string, summary *uiStaticSummary) []checkDiagnostic {
	path := filepath.Join(uiRoot, "components.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         err.Error(),
			SuggestedAction: "Create `ui/components.json` with the approved @scenery registry.",
		}}
	}
	var payload struct {
		Aliases    map[string]string          `json:"aliases"`
		Registries map[string]json.RawMessage `json:"registries"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         err.Error(),
			SuggestedAction: "Fix `ui/components.json` JSON syntax.",
		}}
	}
	var diagnostics []checkDiagnostic
	if len(payload.Registries) != 1 {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         fmt.Sprintf("components.json must configure exactly one registry namespace, found %d", len(payload.Registries)),
			SuggestedAction: "Configure only the @scenery registry namespace.",
		})
	}
	if _, ok := payload.Registries["@scenery"]; !ok {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         "components.json does not configure @scenery",
			SuggestedAction: "Add the approved @scenery registry and remove other registry namespaces.",
		})
	}
	for namespace := range payload.Registries {
		if namespace != "@scenery" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         "unapproved shadcn registry namespace: " + namespace,
				SuggestedAction: "Use @scenery only.",
			})
		}
	}
	if payload.Aliases["ui"] != "@/components/vendor/shadcn" {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         "components.json aliases.ui must point at @/components/vendor/shadcn",
			SuggestedAction: "Keep generated shadcn-derived files under the vendor layer.",
		})
	}
	return diagnostics
}

func checkUIPackageScripts(uiRoot string, summary *uiStaticSummary) []checkDiagnostic {
	path := filepath.Join(uiRoot, "package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         err.Error(),
			SuggestedAction: "Restore `ui/package.json`.",
		}}
	}
	var payload struct {
		Scripts map[string]string `json:"scripts"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         err.Error(),
			SuggestedAction: "Fix `ui/package.json` JSON syntax.",
		}}
	}
	var diagnostics []checkDiagnostic
	for name, script := range payload.Scripts {
		summary.ScriptChecks++
		normalized := strings.Join(strings.Fields(script), " ")
		if uiRawShadcnAddPattern.MatchString(normalized) {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         "package script uses raw shadcn add: " + name,
				SuggestedAction: "Use `node scripts/scenery-shadcn.mjs` so installs are constrained to @scenery/*.",
			})
		}
	}
	if payload.Scripts["shadcn:add"] != "node scripts/scenery-shadcn.mjs" {
		diagnostics = append(diagnostics, checkDiagnostic{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(path),
			Message:         "missing approved shadcn:add wrapper script",
			SuggestedAction: "Set `shadcn:add` to `node scripts/scenery-shadcn.mjs`.",
		})
	}
	return diagnostics
}

func checkUIRegistryItems(uiRoot string, summary *uiStaticSummary) []checkDiagnostic {
	registryRoot := filepath.Join(uiRoot, "registry", "scenery")
	entries, err := os.ReadDir(registryRoot)
	if err != nil {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(registryRoot),
			Message:         err.Error(),
			SuggestedAction: "Create the scenery registry under `ui/registry/scenery`.",
		}}
	}
	var diagnostics []checkDiagnostic
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || entry.Name() == "registry.json" {
			continue
		}
		summary.RegistryItems++
		path := filepath.Join(registryRoot, entry.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the unreadable registry item.",
			})
			continue
		}
		var item struct {
			Name                 string               `json:"name"`
			Type                 string               `json:"type"`
			RegistryDependencies []string             `json:"registryDependencies"`
			Files                []uiRegistryItemFile `json:"files"`
		}
		if err := json.Unmarshal(data, &item); err != nil {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         err.Error(),
				SuggestedAction: "Fix the registry item JSON syntax.",
			})
			continue
		}
		if item.Name == "" || item.Type == "" {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "error",
				File:            filepath.ToSlash(path),
				Message:         "registry item must include name and type",
				SuggestedAction: "Add the required shadcn registry item metadata.",
			})
		}
		for _, dep := range item.RegistryDependencies {
			if !strings.HasPrefix(dep, "@scenery/") {
				diagnostics = append(diagnostics, checkDiagnostic{
					Stage:           "ui static architecture",
					Severity:        "error",
					File:            filepath.ToSlash(path),
					Message:         "registry item depends on non-scenery item: " + dep,
					SuggestedAction: "Promote the dependency into @scenery or remove it.",
				})
			}
		}
		diagnostics = append(diagnostics, checkUIRegistryFiles(uiRoot, path, item.Files)...)
	}
	return diagnostics
}

type uiRegistryItemFile struct {
	Path   string `json:"path"`
	Source string `json:"source"`
	Type   string `json:"type"`
	Target string `json:"target"`
}

func checkUIRegistryFiles(uiRoot, itemPath string, files []uiRegistryItemFile) []checkDiagnostic {
	var diagnostics []checkDiagnostic
	if len(files) == 0 {
		return []checkDiagnostic{{
			Stage:           "ui static architecture",
			Severity:        "error",
			File:            filepath.ToSlash(itemPath),
			Message:         "registry item must declare files",
			SuggestedAction: "Declare explicit registry files with safe source and target paths.",
		}}
	}
	for _, file := range files {
		diagnostics = append(diagnostics, checkUIRegistrySource(uiRoot, itemPath, file.Source)...)
		diagnostics = append(diagnostics, checkUIRegistryTarget(itemPath, file.Target)...)
	}
	return diagnostics
}

func checkUIRegistrySource(uiRoot, itemPath, source string) []checkDiagnostic {
	if source == "" {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file source is required", "Point source at an existing file under ui/src.")}
	}
	if filepath.IsAbs(source) || strings.HasPrefix(source, "~") || containsPathTraversal(source) {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file source must be a relative path under ui/src: "+source, "Use a path like src/components/primitives/Button.tsx.")}
	}
	cleanSource := filepath.Clean(filepath.FromSlash(source))
	sourcePath := filepath.Join(uiRoot, cleanSource)
	srcRoot := filepath.Join(uiRoot, "src")
	rel, err := filepath.Rel(srcRoot, sourcePath)
	if err != nil || rel == "." || strings.HasPrefix(filepath.ToSlash(rel), "../") || filepath.IsAbs(rel) {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file source must stay under ui/src: "+source, "Move the source under ui/src or add a narrowly documented allowlist.")}
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file source does not exist: "+source, "Create the source file or fix the registry item.")}
	}
	if info.IsDir() {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file source must be a file: "+source, "Point source at a concrete file.")}
	}
	return nil
}

func checkUIRegistryTarget(itemPath, target string) []checkDiagnostic {
	if target == "" {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file target is required", "Use @components/, @ui/, @lib/, or @hooks/ target aliases.")}
	}
	if filepath.IsAbs(target) || strings.HasPrefix(target, "~") || containsPathTraversal(target) {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file target must not be absolute, root-relative, or traversing: "+target, "Use @components/, @ui/, @lib/, or @hooks/ target aliases.")}
	}
	allowedPrefix := false
	for _, prefix := range []string{"@components/", "@ui/", "@lib/", "@hooks/"} {
		if strings.HasPrefix(target, prefix) {
			allowedPrefix = true
			break
		}
	}
	if !allowedPrefix {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file target must start with @components/, @ui/, @lib/, or @hooks/: "+target, "Keep registry writes inside approved shadcn target aliases.")}
	}
	lower := strings.ToLower(filepath.ToSlash(target))
	for _, blocked := range []string{
		"package.json",
		"bun.lock",
		"package-lock.json",
		"pnpm-lock.yaml",
		"yarn.lock",
		"vite.config.ts",
		"vite.config.js",
		"tsconfig.json",
		"components.json",
	} {
		if strings.HasSuffix(lower, "/"+blocked) || strings.HasSuffix(lower, blocked) {
			return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file target may not write "+blocked, "Registry items may install source files only, not package/config/lock/script files.")}
		}
	}
	if strings.Contains(lower, "/scripts/") || strings.Contains(lower, "/.") {
		return []checkDiagnostic{uiRegistryFileDiag(itemPath, "registry file target may not write scripts or dotfiles: "+target, "Use an approved source target under components, ui, lib, or hooks.")}
	}
	return nil
}

func uiRegistryFileDiag(path, message, suggestion string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "ui static architecture",
		Severity:        "error",
		File:            filepath.ToSlash(path),
		Message:         message,
		SuggestedAction: suggestion,
	}
}

func containsPathTraversal(value string) bool {
	for _, part := range strings.FieldsFunc(filepath.ToSlash(value), func(r rune) bool { return r == '/' }) {
		if part == ".." {
			return true
		}
	}
	return false
}

func checkUISourceBoundaries(uiRoot string, summary *uiStaticSummary) ([]checkDiagnostic, error) {
	srcRoot := filepath.Join(uiRoot, "src")
	var diagnostics []checkDiagnostic
	err := filepath.WalkDir(srcRoot, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".ts" && ext != ".tsx" {
			return nil
		}
		rel, err := filepath.Rel(uiRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		summary.CheckedFiles++
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(data)
		for _, spec := range uiImportSpecifiers(text) {
			if diag, ok := uiForbiddenImportDiagnostic(uiRoot, path, rel, spec); ok {
				diagnostics = append(diagnostics, diag)
			}
		}
		classDiagnostics := uiClassNameDiagnostics(uiRoot, path, rel, text)
		summary.ClassWarnings += len(classDiagnostics)
		diagnostics = append(diagnostics, classDiagnostics...)
		return nil
	})
	return diagnostics, err
}

func uiForbiddenImportDiagnostic(uiRoot, path, rel, spec string) (checkDiagnostic, bool) {
	allowLowLevel := strings.HasPrefix(rel, "src/components/primitives/") ||
		strings.HasPrefix(rel, "src/components/layouts/") ||
		strings.HasPrefix(rel, "src/components/registry/") ||
		strings.HasPrefix(rel, "src/components/vendor/shadcn/")
	if strings.Contains(spec, "/components/ui") || strings.HasPrefix(spec, "@/components/ui") {
		return uiBoundaryDiag(path, "import from legacy components/ui is forbidden: "+spec, "Import from @/components/primitives or @/components/layouts."), true
	}
	if strings.Contains(spec, "/components/vendor/shadcn") || strings.HasPrefix(spec, "@/components/vendor/shadcn") {
		if !allowLowLevel {
			return uiBoundaryDiag(path, "app screens must not import vendor shadcn directly: "+spec, "Wrap the vendor component in a Scenery primitive first."), true
		}
	}
	switch spec {
	case "class-variance-authority", "clsx", "tailwind-merge":
		if !allowLowLevel && rel != "src/lib/utils.ts" {
			return uiBoundaryDiag(path, "styling utility import is only allowed in scenery primitives/layouts/vendor: "+spec, "Expose a typed primitive or layout instead."), true
		}
	case "lucide-react":
		if !allowLowLevel && rel != "src/components/primitives/icons.tsx" && !strings.HasPrefix(rel, "src/components/layouts/") {
			return uiBoundaryDiag(path, "lucide-react imports must go through a Scenery icons wrapper", "Create or use @/components/primitives/icons."), true
		}
	case "radix-ui":
		if !allowLowLevel {
			return uiBoundaryDiag(path, "radix-ui imports are only allowed inside scenery primitives/layouts/vendor", "Wrap Radix behavior in a Scenery primitive."), true
		}
	default:
		if strings.HasPrefix(spec, "@radix-ui/") && !allowLowLevel {
			return uiBoundaryDiag(path, "Radix imports are only allowed inside scenery primitives/layouts/vendor: "+spec, "Wrap Radix behavior in a Scenery primitive."), true
		}
	}
	return checkDiagnostic{}, false
}

func uiBoundaryDiag(path, message, suggestion string) checkDiagnostic {
	return checkDiagnostic{
		Stage:           "ui static architecture",
		Severity:        "error",
		File:            filepath.ToSlash(path),
		Message:         message,
		SuggestedAction: suggestion,
	}
}

func uiClassNameDiagnostics(uiRoot, path, rel, text string) []checkDiagnostic {
	if strings.HasPrefix(rel, "src/components/primitives/") ||
		strings.HasPrefix(rel, "src/components/layouts/") ||
		strings.HasPrefix(rel, "src/components/registry/") ||
		strings.HasPrefix(rel, "src/components/vendor/shadcn/") {
		return nil
	}
	var diagnostics []checkDiagnostic
	for _, value := range uiClassNameLiterals(text) {
		if len(value) > 180 {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "warning",
				File:            filepath.ToSlash(path),
				Message:         fmt.Sprintf("long className literal (%d chars) should move into a Scenery primitive or layout", len(value)),
				SuggestedAction: "When touching this file, compose existing scenery primitives/layouts instead of extending class soup.",
			})
			continue
		}
		if strings.Contains(value, "[&") || strings.Contains(value, "![") || strings.Contains(value, "!") {
			diagnostics = append(diagnostics, checkDiagnostic{
				Stage:           "ui static architecture",
				Severity:        "warning",
				File:            filepath.ToSlash(path),
				Message:         "advanced Tailwind-style className syntax outside primitives/layouts should be promoted",
				SuggestedAction: "Move the behavior into a Scenery primitive or layout before expanding it.",
			})
		}
	}
	return diagnostics
}

var (
	uiRawShadcnAddPattern  = regexp.MustCompile(`\bshadcn(?:@[^ ]+)?\s+add\b`)
	uiImportFromPattern    = regexp.MustCompile(`(?s)\bimport\s+(?:type\s+)?(?:[^;'"()]+?\s+from\s*)["']([^"']+)["']`)
	uiSideEffectPattern    = regexp.MustCompile(`(?m)\bimport\s+["']([^"']+)["']`)
	uiExportFromPattern    = regexp.MustCompile(`(?s)\bexport\s+(?:type\s+)?(?:[^;'"()]+?\s+from\s*)["']([^"']+)["']`)
	uiDynamicImportPattern = regexp.MustCompile(`\bimport\s*\(\s*["']([^"']+)["']\s*\)`)
	uiRequirePattern       = regexp.MustCompile(`\brequire\s*\(\s*["']([^"']+)["']\s*\)`)
)

func uiImportSpecifiers(text string) []string {
	var specs []string
	seen := map[string]bool{}
	for _, pattern := range []*regexp.Regexp{
		uiImportFromPattern,
		uiSideEffectPattern,
		uiExportFromPattern,
		uiDynamicImportPattern,
		uiRequirePattern,
	} {
		for _, match := range pattern.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 || match[1] == "" || seen[match[1]] {
				continue
			}
			seen[match[1]] = true
			specs = append(specs, match[1])
		}
	}
	return specs
}

func uiClassNameLiterals(text string) []string {
	var values []string
	for _, marker := range []string{`className="`, `className='`} {
		offset := 0
		for {
			idx := strings.Index(text[offset:], marker)
			if idx < 0 {
				break
			}
			start := offset + idx + len(marker)
			quote := marker[len(marker)-1]
			end := strings.IndexByte(text[start:], quote)
			if end < 0 {
				break
			}
			values = append(values, text[start:start+end])
			offset = start + end + 1
		}
	}
	offset := 0
	for {
		idx := strings.Index(text[offset:], "className={")
		if idx < 0 {
			break
		}
		start := offset + idx + len("className=")
		expr, next, ok := uiBalancedBraceExpression(text, start)
		if !ok {
			break
		}
		values = append(values, uiStringLiterals(expr)...)
		offset = next
	}
	return values
}

func uiBalancedBraceExpression(text string, start int) (string, int, bool) {
	if start >= len(text) || text[start] != '{' {
		return "", start, false
	}
	depth := 0
	quote := byte(0)
	escaped := false
	for i := start; i < len(text); i++ {
		ch := text[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}
		switch ch {
		case '\'', '"', '`':
			quote = ch
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return text[start+1 : i], i + 1, true
			}
		}
	}
	return "", len(text), false
}

func uiStringLiterals(text string) []string {
	var values []string
	for i := 0; i < len(text); i++ {
		quote := text[i]
		if quote != '\'' && quote != '"' && quote != '`' {
			continue
		}
		var b strings.Builder
		escaped := false
		for j := i + 1; j < len(text); j++ {
			ch := text[j]
			if escaped {
				b.WriteByte(ch)
				escaped = false
				continue
			}
			if ch == '\\' {
				escaped = true
				continue
			}
			if ch == quote {
				values = append(values, b.String())
				i = j
				break
			}
			b.WriteByte(ch)
		}
	}
	return values
}
