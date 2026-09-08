package repoinfo

import (
	"path/filepath"
	"scenery.sh/internal/harnessreport"
	"sort"
	"strings"
)

func PopulateChangedArea(repoRoot string, report *harnessreport.ChangedAreaReport, changes []harnessreport.ChangedFile, packages []PackageInfo, diagnostics []harnessreport.Diagnostic) {
	report.Diagnostics = append(report.Diagnostics, diagnostics...)
	packageSet := map[string]bool{}
	commandSet := map[string]bool{}
	docSet := map[string]bool{}
	riskSet := map[string]bool{}

	for _, change := range changes {
		if IsLocalArtifact(change.Path) {
			change.Category = "local-artifact"
			report.IgnoredFiles = append(report.IgnoredFiles, change)
			continue
		}
		change.Category = classifyHarnessChangedFile(change.Path)
		if strings.HasSuffix(change.Path, ".go") {
			if pkg, ok := harnessPackageForFile(repoRoot, change.Path, packages); ok {
				change.Package = pkg.ImportPath
				packageSet[pkg.ImportPath] = true
				if pkg.RelDir == "." {
					commandSet["go test ."] = true
				} else {
					commandSet["go test ./"+filepath.ToSlash(pkg.RelDir)] = true
				}
			}
		}
		addHarnessChangedAreaKnowledge(change.Path, change.Category, docSet, riskSet, commandSet)
		report.ChangedFiles = append(report.ChangedFiles, change)
	}

	report.AffectedPackages = SortedStringSet(packageSet)
	report.ValidationClasses = addHarnessChangedAreaValidation(report, commandSet, docSet)
	report.RecommendedCommands = SortedStringSet(commandSet)
	report.RelevantDocs = SortedStringSet(docSet)
	report.RiskFlags = SortedStringSet(riskSet)
	sort.Slice(report.ChangedFiles, func(i, j int) bool {
		if report.ChangedFiles[i].Path == report.ChangedFiles[j].Path {
			return report.ChangedFiles[i].Status < report.ChangedFiles[j].Status
		}
		return report.ChangedFiles[i].Path < report.ChangedFiles[j].Path
	})
}

func IsLocalArtifact(path string) bool {
	path = filepath.ToSlash(strings.TrimSpace(path))
	base := filepath.Base(path)
	switch {
	case path == "":
		return false
	case strings.HasPrefix(path, "docs/schemas/"):
		return false
	case path == ".claude" || strings.HasPrefix(path, ".claude/"):
		return true
	case strings.HasPrefix(path, ".scenery/"):
		return true
	case strings.HasPrefix(path, "coverage/"):
		return true
	case strings.HasPrefix(path, "test-results/"):
		return true
	case strings.Contains(base, ".harness") && strings.HasSuffix(base, ".json"):
		return true
	case strings.HasPrefix(base, "scenery-harness-self-") && strings.HasSuffix(base, ".json"):
		return true
	default:
		return false
	}
}

func harnessPackageForFile(repoRoot, relPath string, packages []PackageInfo) (PackageInfo, bool) {
	abs := filepath.Join(repoRoot, filepath.FromSlash(relPath))
	for _, pkg := range packages {
		rel, err := filepath.Rel(pkg.Dir, abs)
		if err != nil {
			continue
		}
		if rel == "." || (!strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != "..") {
			return pkg, true
		}
	}
	return PackageInfo{}, false
}

func classifyHarnessChangedFile(path string) string {
	switch {
	case path == "AGENTS.md" || strings.HasSuffix(path, "/AGENTS.md"):
		return "docs"
	case strings.HasPrefix(path, "cmd/scenery/"):
		return "cli"
	case strings.HasPrefix(path, "internal/"):
		return "internal"
	case harnessPublicRuntimePath(path):
		return "runtime"
	case strings.HasPrefix(path, "ui/"), strings.HasPrefix(path, "apps/console/"):
		return "ui"
	case strings.HasPrefix(path, "docs/schemas/"):
		return "schema"
	case strings.HasPrefix(path, "docs/plans/"):
		return "exec-plan"
	case strings.HasPrefix(path, "docs/") || path == "SKILL.md" || path == "PLAN.md" || path == "PLANS.md":
		return "docs"
	case strings.HasPrefix(path, "testdata/"):
		return "fixture"
	case strings.HasPrefix(path, "scripts/"):
		return "script"
	case path == "go.mod" || path == "go.sum":
		return "dependency"
	default:
		return "other"
	}
}

func harnessPublicRuntimePath(path string) bool {
	for _, prefix := range []string{
		"auth/",
		"cron/",
		"datasource/",
		"db/",
		"durable/",
		"library/",
		"middleware/",
		"model/",
		"object/",
		"page/",
		"rlog/",
		"runtime/",
		"storage/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func addHarnessChangedAreaKnowledge(path, category string, docs, risks, commands map[string]bool) {
	if harnessDevEventReadPath(path) {
		docs["docs/local-contract.md"] = true
		risks["victoria-dev-event-read-path"] = true
		commands["scenery logs --limit 500 -o jsonl"] = true
	}
	switch category {
	case "cli":
		docs["docs/harness-engineering.md"] = true
		risks["cli-contract"] = true
		if strings.Contains(path, "harness") {
			risks["harness-contract"] = true
			docs["docs/plans/0051-harness-self-agent-oracle.md"] = true
		}
	case "schema":
		docs["docs/knowledge.json"] = true
		risks["json-schema-contract"] = true
	case "exec-plan":
		docs["PLANS.md"] = true
		docs["docs/plans/active.md"] = true
		risks["exec-plan"] = true
	case "ui":
		if strings.HasPrefix(path, "apps/console/") {
			risks["dashboard-ui"] = true
			docs["apps/console/AGENTS.md"] = true
			commands["cd apps/console && bun run lint && bun run typecheck && bun run build"] = true
		} else {
			risks["generated-ui-catalog"] = true
			docs["docs/ui-agent-contract.md"] = true
			commands["apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json"] = true
			commands["go test ./internal/generate"] = true
		}
	case "fixture":
		docs["docs/app-development-cookbook.md"] = true
		risks["fixture-contract"] = true
	case "dependency":
		docs["docs/harness-engineering.md"] = true
		risks["dependency-graph"] = true
	case "runtime":
		docs["docs/local-contract.md"] = true
		risks["runtime-contract"] = true
	case "internal":
		if strings.HasPrefix(path, "internal/build/") {
			docs["docs/plans/0050-test-suite-speed-hardening.md"] = true
			risks["build-cache"] = true
		}
	}
}

func harnessDevEventReadPath(path string) bool {
	switch path {
	case "cmd/scenery/logs.go",
		"cmd/scenery/logs_test.go",
		"cmd/scenery/dev_console.go",
		"cmd/scenery/dev_console_test.go",
		"internal/victoria/devlogs.go",
		"internal/devdash/dev_events.go",
		"internal/devdash/store.go",
		"internal/devdash/store_test.go":
		return true
	default:
		return false
	}
}

func OnlvImpactingPath(path string) bool {
	for _, needle := range []string{
		"onlv",
		"cmd/scenery/dev_session",
		"cmd/scenery/dev_services",
		"cmd/scenery/dev_supervisor",
		"cmd/scenery/edge",
		"cmd/scenery/db",
		"internal/agent",
		"internal/localproxy",
		"docs/plans/0045-",
		"docs/plans/0048-",
		"docs/plans/0049-",
		"docs/plans/0063-",
	} {
		if strings.Contains(path, needle) {
			return true
		}
	}
	return false
}

func SortedStringSet(values map[string]bool) []string {
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func AppendUniqueSorted(base []string, values ...string) []string {
	set := map[string]bool{}
	for _, value := range base {
		if value != "" {
			set[value] = true
		}
	}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return SortedStringSet(set)
}
