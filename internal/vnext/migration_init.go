package vnext

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/mod/modfile"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/parse"
)

type MigrationInitializationPlan struct {
	APIVersion                 string       `json:"api_version"`
	PlanID                     string       `json:"plan_id"`
	Caller                     string       `json:"caller"`
	BaseWorkspaceRevision      string       `json:"base_workspace_revision"`
	PredictedWorkspaceRevision string       `json:"predicted_workspace_revision"`
	PredictedContractRevision  string       `json:"predicted_contract_revision"`
	Services                   []string     `json:"services"`
	Edits                      []SourceEdit `json:"source_edits"`
	ExpiresAt                  time.Time    `json:"expires_at"`
}

type MigrationInitializationApplyOptions struct {
	ExpectedWorkspaceRevision string
	Caller                    string
}

type MigrationInitializationReceipt struct {
	APIVersion        string   `json:"api_version"`
	PlanID            string   `json:"plan_id"`
	WorkspaceRevision string   `json:"workspace_revision"`
	ContractRevision  string   `json:"contract_revision"`
	Services          []string `json:"services"`
}

func PlanMigrationInitialization(root, caller string) (MigrationInitializationPlan, error) {
	discoveredRoot, _, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return MigrationInitializationPlan{}, fmt.Errorf("load legacy app root: %w", err)
	}
	absRoot, err := filepath.Abs(discoveredRoot)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	for _, name := range []string{"scenery.scn", "scenery.migration.scn"} {
		if pathExists(filepath.Join(absRoot, name)) {
			return MigrationInitializationPlan{}, fmt.Errorf("failed_precondition: %s already exists", name)
		}
	}
	baseRevision, err := legacyWorkspaceRevision(absRoot)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	rootSource, migrationSource, services, err := migrationInitializationSources(absRoot)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	temp, err := cloneWorkspace(absRoot)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	defer os.RemoveAll(temp)
	if err := atomicWrite(filepath.Join(temp, "scenery.scn"), rootSource); err != nil {
		return MigrationInitializationPlan{}, err
	}
	if err := atomicWrite(filepath.Join(temp, "scenery.migration.scn"), migrationSource); err != nil {
		return MigrationInitializationPlan{}, err
	}
	if _, err := Format(temp, false); err != nil {
		return MigrationInitializationPlan{}, err
	}
	predicted, err := Compile(temp)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	if !predicted.Valid() || predicted.Manifest == nil {
		return MigrationInitializationPlan{}, fmt.Errorf("failed_precondition: proposed migration initialization is invalid: %s", firstError(predicted.Diagnostics))
	}
	edits, err := changedWorkspaceFiles(absRoot, temp)
	if err != nil {
		return MigrationInitializationPlan{}, err
	}
	caller = strings.TrimSpace(caller)
	if caller == "" {
		caller = "local"
	}
	plan := MigrationInitializationPlan{
		APIVersion:                 "scenery.migrate.initialization-plan.v1",
		Caller:                     caller,
		BaseWorkspaceRevision:      baseRevision,
		PredictedWorkspaceRevision: predicted.WorkspaceRevision,
		PredictedContractRevision:  predicted.Manifest.ContractRevision,
		Services:                   services,
		Edits:                      edits,
		ExpiresAt:                  time.Now().UTC().Add(15 * time.Minute),
	}
	plan.PlanID = migrationInitializationPlanID(plan)
	if err := retainIssuedPlan(absRoot, issuedMigrationInitializationPlan, plan.PlanID, plan); err != nil {
		return MigrationInitializationPlan{}, err
	}
	return plan, nil
}

func ApplyMigrationInitialization(root string, plan MigrationInitializationPlan, options MigrationInitializationApplyOptions) (MigrationInitializationReceipt, error) {
	discoveredRoot, _, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return MigrationInitializationReceipt{}, fmt.Errorf("load legacy app root: %w", err)
	}
	root = discoveredRoot
	if err := requireIssuedPlan(root, issuedMigrationInitializationPlan, plan.PlanID, plan); err != nil {
		return MigrationInitializationReceipt{}, err
	}
	if time.Now().UTC().After(plan.ExpiresAt) {
		return MigrationInitializationReceipt{}, fmt.Errorf("failed_precondition: migration initialization plan expired")
	}
	if plan.PlanID == "" || migrationInitializationPlanID(plan) != plan.PlanID {
		return MigrationInitializationReceipt{}, fmt.Errorf("failed_precondition: migration initialization plan identity mismatch")
	}
	if options.Caller != plan.Caller || options.ExpectedWorkspaceRevision != plan.BaseWorkspaceRevision {
		return MigrationInitializationReceipt{}, fmt.Errorf("revision_conflict: migration initialization binding changed")
	}
	if pathExists(migrationInitializationReceiptPath(root, plan.PlanID)) {
		return MigrationInitializationReceipt{}, fmt.Errorf("failed_precondition: migration initialization plan was already applied")
	}
	currentRevision, err := legacyWorkspaceRevision(root)
	if err != nil {
		return MigrationInitializationReceipt{}, err
	}
	if currentRevision != plan.BaseWorkspaceRevision {
		return MigrationInitializationReceipt{}, fmt.Errorf("revision_conflict: legacy workspace changed")
	}
	staged, err := cloneWorkspace(root)
	if err != nil {
		return MigrationInitializationReceipt{}, err
	}
	defer os.RemoveAll(staged)
	if err := applyPlannedEdits(staged, plan.Edits, true); err != nil {
		return MigrationInitializationReceipt{}, err
	}
	checked, checkedFiles, err := validateStagedWorkspace(staged, false)
	if err != nil || !checked.Valid() || checked.Manifest == nil || checked.WorkspaceRevision != plan.PredictedWorkspaceRevision || checked.Manifest.ContractRevision != plan.PredictedContractRevision {
		return MigrationInitializationReceipt{}, fmt.Errorf("failed_precondition: staged migration initialization no longer validates")
	}
	rollback, finalize, err := commitPlannedEdits(root, plan.Edits, migrationInitializationReceiptPath(root, plan.PlanID))
	if err != nil {
		return MigrationInitializationReceipt{}, err
	}
	actual, err := revalidateCommittedResult(root, checked, checkedFiles)
	if err != nil || !actual.Valid() || actual.Manifest == nil || actual.WorkspaceRevision != plan.PredictedWorkspaceRevision || actual.Manifest.ContractRevision != plan.PredictedContractRevision {
		rollback()
		return MigrationInitializationReceipt{}, fmt.Errorf("internal: applied migration initialization revisions differ from plan")
	}
	receipt := MigrationInitializationReceipt{
		APIVersion:        "scenery.migrate.initialization-receipt.v1",
		PlanID:            plan.PlanID,
		WorkspaceRevision: actual.WorkspaceRevision,
		ContractRevision:  actual.Manifest.ContractRevision,
		Services:          append([]string(nil), plan.Services...),
	}
	encoded, _ := json.MarshalIndent(receipt, "", "  ")
	if err := atomicWriteSynced(migrationInitializationReceiptPath(root, plan.PlanID), append(encoded, '\n'), 0o644); err != nil {
		rollback()
		return MigrationInitializationReceipt{}, err
	}
	finalize()
	return receipt, nil
}

func migrationInitializationSources(root string) ([]byte, []byte, []string, error) {
	_, config, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("load legacy config: %w", err)
	}
	appModel, err := parse.App(root, config.Name)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("discover legacy application: %w", err)
	}
	if len(appModel.Services) == 0 {
		return nil, nil, nil, fmt.Errorf("failed_precondition: no legacy services were discovered")
	}
	goModBytes, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("read go.mod: %w", err)
	}
	goMod, err := modfile.Parse("go.mod", goModBytes, nil)
	if err != nil || goMod.Module == nil || strings.TrimSpace(goMod.Module.Mod.Path) == "" {
		return nil, nil, nil, fmt.Errorf("failed_precondition: go.mod requires a module path")
	}
	toolchain := strings.TrimPrefix(runtime.Version(), "go")
	if _, err := parseSemanticVersion(toolchain); err != nil {
		return nil, nil, nil, fmt.Errorf("capability_unavailable: active Go toolchain %q is not an exact release", runtime.Version())
	}
	packageSet := map[string]bool{}
	servicePackages := map[string]string{}
	services := make([]string, 0, len(appModel.Services))
	for _, service := range appModel.Services {
		name := strings.TrimSpace(service.Name)
		packagePath := filepath.ToSlash(filepath.Clean(service.RootRelDir))
		if name == "" || packagePath == "." || packagePath == "" || strings.HasPrefix(packagePath, "../") {
			return nil, nil, nil, fmt.Errorf("failed_precondition: legacy service %q has an invalid package root %q", name, service.RootRelDir)
		}
		if _, exists := servicePackages[name]; exists {
			return nil, nil, nil, fmt.Errorf("failed_precondition: duplicate legacy service %s", name)
		}
		services = append(services, name)
		servicePackages[name] = packagePath
		packageSet["./"+packagePath] = true
	}
	sort.Strings(services)
	packages := sortedBoolKeys(packageSet)
	quotedPackages := make([]string, 0, len(packages))
	for _, packagePath := range packages {
		quotedPackages = append(quotedPackages, fmt.Sprintf("%q", packagePath))
	}
	rootSource := fmt.Sprintf(`language {
  edition = "2027"

  require_profiles = [
    "scenery.compiler-core/v1",
    "scenery.go-implementation/v1",
    "scenery.runtime-http/v1",
    "scenery.inspection-core/v1",
    "scenery.legacy-bridge/v1",
  ]
}

workspace {
  implementation_root "application" {
    path             = "."
    revision_include = ["**/*.go", "go.mod"]
  }
}

go_module "application" {
  root        = "."
  import_path = %q
}

go_toolchain "application" {
  version     = %q
  experiments = []
}

go_target "legacy" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.application
  module    = go_module.application
  packages  = [%s]
  cgo       = "disabled"
}

application %q {
  version = "0.1.0"
}

http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
`, goMod.Module.Mod.Path, toolchain, strings.Join(quotedPackages, ", "), config.Name)

	var migration strings.Builder
	fmt.Fprintf(&migration, "migration {\n  frontend      = \"scenery.legacy.v0\"\n  legacy_config = %q\n\n  legacy_gateway \"default\" {\n    target = http_gateway.public_api\n  }\n", config.SourceRelPath(root))
	for _, service := range services {
		fmt.Fprintf(&migration, "\n  legacy_service %q {\n    package   = %q\n    namespace = %q\n    target    = go_target.legacy\n  }\n", service, "./"+servicePackages[service], service)
	}
	migration.WriteString("}\n")
	return []byte(rootSource), []byte(migration.String()), services, nil
}

func legacyWorkspaceRevision(root string) (string, error) {
	files, err := snapshotWorkspaceFiles(root)
	if err != nil {
		return "", err
	}
	projection := map[string]any{}
	for path, file := range files {
		projection[path] = map[string]any{"digest": byteDigest(file.bytes), "mode": uint32(file.mode.Perm())}
	}
	return revisionHash("scenery.legacy-workspace.v1\x00", projection), nil
}

func migrationInitializationPlanID(plan MigrationInitializationPlan) string {
	copy := plan
	copy.PlanID = ""
	return revisionHash("scenery.migration-initialization-plan.v1\x00", copy)
}

func migrationInitializationReceiptPath(root, planID string) string {
	name := strings.NewReplacer(":", "_", "/", "_").Replace(planID) + ".json"
	return filepath.Join(root, ".scenery", "migrations", "receipts", "initialization-"+name)
}
