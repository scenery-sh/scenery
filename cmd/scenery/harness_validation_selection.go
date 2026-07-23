package main

import "strings"

const (
	harnessValidationDocumentation      = "documentation-only"
	harnessValidationGoPackage          = "go-package"
	harnessValidationCLIJSONContract    = "cli-json-contract"
	harnessValidationCompilerGenerator  = "compiler-or-generator"
	harnessValidationUICatalog          = "ui-catalog"
	harnessValidationDashboard          = "dashboard"
	harnessValidationReleaseRuntime     = "release-sensitive-or-runtime"
	harnessValidationRepositoryFallback = "repository-fallback"

	harnessValidationQuickCommand = ".scenery/harness/bin/scenery harness self --quick --summary --write"
	harnessValidationFullCommand  = ".scenery/harness/bin/scenery harness self --summary --write"
	harnessValidationUICommand    = ".scenery/harness/bin/scenery harness ui -o json --write"
)

var harnessFixtureRegenerationCommands = []string{
	"go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json",
	"go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json",
}

// addHarnessChangedAreaValidation turns path and contract matches into the
// minimum command union for the current change. The classes are deliberately
// mechanical: agents should not have to decide whether a change feels large.
func addHarnessChangedAreaValidation(report *harnessChangedAreaReport, commands, docs map[string]bool) []string {
	if len(report.ChangedFiles) == 0 {
		return []string{}
	}

	classes := map[string]bool{}
	if harnessDocumentationOnlyChange(report.ChangedFiles) {
		classes[harnessValidationDocumentation] = true
		commands[harnessValidationQuickCommand] = true
		return sortedStringSet(classes)
	}

	if len(report.AffectedPackages) > 0 {
		classes[harnessValidationGoPackage] = true
		commands["go test ./..."] = true
	}

	for _, file := range report.ChangedFiles {
		if harnessCLIJSONContractPath(file.Path, file.Category) {
			classes[harnessValidationCLIJSONContract] = true
		}
		if file.Category == "docs" || file.Category == "exec-plan" {
			continue
		}
		if strings.HasPrefix(file.Path, "internal/compiler/") ||
			strings.HasPrefix(file.Path, "internal/generate/") ||
			strings.HasPrefix(file.Path, "cmd/scenery/generate") {
			classes[harnessValidationCompilerGenerator] = true
		}
		if strings.HasPrefix(file.Path, "ui/") {
			classes[harnessValidationUICatalog] = true
		}
		if strings.HasPrefix(file.Path, "apps/console/") {
			classes[harnessValidationDashboard] = true
		}
		if harnessReleaseSensitivePath(file.Path, file.Category) {
			classes[harnessValidationReleaseRuntime] = true
		}
	}

	if classes[harnessValidationCLIJSONContract] {
		commands["go test ./cmd/scenery"] = true
		commands[harnessValidationQuickCommand] = true
		docs["docs/local-contract.md"] = true
	}
	if classes[harnessValidationCompilerGenerator] {
		for _, command := range harnessFixtureRegenerationCommands {
			commands[command] = true
		}
		commands["go test ./..."] = true
	}
	if classes[harnessValidationUICatalog] {
		commands["apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json"] = true
		commands["go test ./internal/generate"] = true
		for _, command := range harnessFixtureRegenerationCommands {
			commands[command] = true
		}
	}
	if classes[harnessValidationDashboard] {
		commands["cd apps/console && bun run lint && bun run typecheck && bun run build"] = true
		commands[harnessValidationUICommand] = true
	}
	if classes[harnessValidationReleaseRuntime] {
		commands[harnessValidationFullCommand] = true
		delete(commands, harnessValidationQuickCommand)
	}
	if len(classes) == 0 {
		classes[harnessValidationRepositoryFallback] = true
		commands["go test ./..."] = true
	}
	return sortedStringSet(classes)
}

func harnessDocumentationOnlyChange(files []harnessChangedFile) bool {
	if len(files) == 0 {
		return false
	}
	for _, file := range files {
		if file.Category != "docs" && file.Category != "exec-plan" {
			return false
		}
	}
	return true
}

func harnessCLIJSONContractPath(path, category string) bool {
	return category == "cli" ||
		category == "schema" ||
		path == "docs/local-contract.md" ||
		path == "docs/knowledge.json" ||
		strings.HasPrefix(path, "internal/machine/")
}

func harnessReleaseSensitivePath(path, category string) bool {
	if category == "runtime" || category == "dependency" || category == "script" {
		return true
	}
	for _, prefix := range []string{
		"cmd/scenery/agent",
		"cmd/scenery/build",
		"cmd/scenery/dashboard",
		"cmd/scenery/db_",
		"cmd/scenery/deploy",
		"cmd/scenery/dev",
		"cmd/scenery/edge",
		"cmd/scenery/harness",
		"cmd/scenery/local",
		"cmd/scenery/observability",
		"cmd/scenery/postgres",
		"cmd/scenery/process",
		"cmd/scenery/snapshot",
		"cmd/scenery/storage",
		"cmd/scenery/system",
		"cmd/scenery/upgrade",
		"cmd/scenery/victoria",
		"cmd/scenery/watch",
		"cmd/scenery/worker",
		"internal/agent/",
		"internal/app/",
		"internal/authbridge/",
		"internal/build/",
		"internal/calendartrigger/",
		"internal/deploydiag/",
		"internal/deployplan/",
		"internal/desktop/",
		"internal/devdash/",
		"internal/devmeta/",
		"internal/devreport/",
		"internal/doctor/",
		"internal/durable/",
		"internal/edge/",
		"internal/librarybuild/",
		"internal/localproxy/",
		"internal/observability/",
		"internal/postgresdb/",
		"internal/runtimeapi/",
		"internal/sqlitedb/",
		"internal/storage/",
		"internal/storageconfig/",
		"internal/testsuite/",
		"internal/toolchain/",
		"internal/victoria/",
		"internal/watchignore/",
		"internal/wire/",
		"internal/workspacetx/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
