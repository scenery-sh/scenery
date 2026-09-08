package repoinfo

import (
	"scenery.sh/internal/harnessreport"
	"strings"
)

const (
	ValidationDocumentation      = "documentation-only"
	ValidationGoPackage          = "go-package"
	ValidationCLIJSONContract    = "cli-json-contract"
	ValidationCompilerGenerator  = "compiler-or-generator"
	ValidationUICatalog          = "ui-catalog"
	ValidationDashboard          = "dashboard"
	ValidationReleaseRuntime     = "release-sensitive-or-runtime"
	ValidationRepositoryFallback = "repository-fallback"

	ValidationQuickCommand = "go run ./scripts/verify --quick --summary --write"
	ValidationFullCommand  = "go run ./scripts/verify --summary --write"
	ValidationUICommand    = ".scenery/harness/bin/scenery harness ui -o json --write"
)

var FixtureRegenerationCommands = []string{
	"go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json",
	"go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json",
}

// addHarnessChangedAreaValidation turns path and contract matches into the
// minimum command union for the current change. The classes are deliberately
// mechanical: agents should not have to decide whether a change feels large.
func addHarnessChangedAreaValidation(report *harnessreport.ChangedAreaReport, commands, docs map[string]bool) []string {
	if len(report.ChangedFiles) == 0 {
		return []string{}
	}

	classes := map[string]bool{}
	if harnessDocumentationOnlyChange(report.ChangedFiles) {
		classes[ValidationDocumentation] = true
		commands[ValidationQuickCommand] = true
		return SortedStringSet(classes)
	}

	if len(report.AffectedPackages) > 0 {
		classes[ValidationGoPackage] = true
		commands["go test ./..."] = true
	}

	for _, file := range report.ChangedFiles {
		if harnessCLIJSONContractPath(file.Path, file.Category) {
			classes[ValidationCLIJSONContract] = true
		}
		if file.Category == "docs" || file.Category == "exec-plan" {
			continue
		}
		if strings.HasPrefix(file.Path, "internal/compiler/") ||
			strings.HasPrefix(file.Path, "internal/generate/") ||
			strings.HasPrefix(file.Path, "cmd/scenery/generate") {
			classes[ValidationCompilerGenerator] = true
		}
		if strings.HasPrefix(file.Path, "ui/") {
			classes[ValidationUICatalog] = true
		}
		if strings.HasPrefix(file.Path, "apps/console/") {
			classes[ValidationDashboard] = true
		}
		if harnessReleaseSensitivePath(file.Path, file.Category) {
			classes[ValidationReleaseRuntime] = true
		}
	}

	if classes[ValidationCLIJSONContract] {
		commands["go test ./cmd/scenery"] = true
		commands[ValidationQuickCommand] = true
		docs["docs/local-contract.md"] = true
	}
	if classes[ValidationCompilerGenerator] {
		for _, command := range FixtureRegenerationCommands {
			commands[command] = true
		}
		commands["go test ./..."] = true
	}
	if classes[ValidationUICatalog] {
		commands["apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json"] = true
		commands["go test ./internal/generate"] = true
		for _, command := range FixtureRegenerationCommands {
			commands[command] = true
		}
	}
	if classes[ValidationDashboard] {
		commands["cd apps/console && bun run lint && bun run typecheck && bun run build"] = true
		commands[ValidationUICommand] = true
	}
	if classes[ValidationReleaseRuntime] {
		commands[ValidationFullCommand] = true
		delete(commands, ValidationQuickCommand)
	}
	if len(classes) == 0 {
		classes[ValidationRepositoryFallback] = true
		commands["go test ./..."] = true
	}
	return SortedStringSet(classes)
}

func harnessDocumentationOnlyChange(files []harnessreport.ChangedFile) bool {
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
