package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/scn"
)

const harnessGenerationCompileProbeName = "generation compile probes"

type harnessGenerationCompileCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessGenerationCompileProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessGenerationCompileProbeStepWithCheck(ctx, repoRoot, runHarnessGenerationCompileProbeCheck)
}

func runHarnessGenerationCompileProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessGenerationCompileCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessGenerationCompileProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the generation compiler boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessGenerationCompileProbeCheck(parent context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	started := time.Now()
	segments := []map[string]any{}
	runSegment := func(name string, run func() error) error {
		segmentStarted := time.Now()
		err := run()
		segments = append(segments, map[string]any{
			"name":        name,
			"duration_ms": time.Since(segmentStarted).Milliseconds(),
			"ok":          err == nil,
		})
		return err
	}
	summary = map[string]any{"proof": "pending", "segments": segments}
	defer func() {
		summary["segments"] = segments
		summary["duration_ms"] = time.Since(started).Milliseconds()
	}()

	probeRoot, err := os.MkdirTemp("", "scenery-generation-compile-probe-*")
	if err != nil {
		return summary, nil, err
	}
	defer func() { _ = os.RemoveAll(probeRoot) }()
	restoreDevCache := devcache.SetRoot(filepath.Join(probeRoot, "editor-cache"))
	defer restoreDevCache()
	appRoot := filepath.Join(probeRoot, "app")
	if err := runSegment("prepare provider CRUD fixture", func() error {
		if err := copyHarnessNativeContractFixture(repoRoot, appRoot); err != nil {
			return err
		}
		return configureHarnessProviderCRUDFixture(appRoot)
	}); err != nil {
		return summary, nil, err
	}

	var changed []string
	if err := runSegment("generate provider CRUD contracts", func() error {
		result, generateErr := generate.GenerateGoContracts(appRoot, false)
		changed = result.Changed
		return generateErr
	}); err != nil {
		return summary, nil, err
	}
	var providerCompiled *compiler.Result
	var contractRevision string
	var adapterPath string
	if err := runSegment("verify provider CRUD adapter", func() error {
		compiled, compileErr := compiler.Compile(appRoot)
		if compileErr != nil {
			return compileErr
		}
		if !compiled.Valid() {
			return fmt.Errorf("generated provider CRUD graph is invalid: %#v", compiled.Diagnostics)
		}
		providerCompiled = compiled
		contractRevision = compiled.Manifest.ContractRevision
		adapterPath, compileErr = findHarnessProviderCRUDAdapter(appRoot)
		return compileErr
	}); err != nil {
		return summary, nil, err
	}
	var goTestOutput string
	if err := runSegment("compile generated provider CRUD application", func() error {
		command := exec.CommandContext(ctx, "go", "test", "./...")
		command.Dir = appRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off", "GOMAXPROCS=2")
		output, runErr := command.CombinedOutput()
		goTestOutput = strings.TrimSpace(string(output))
		if runErr != nil {
			return fmt.Errorf("go test generated provider CRUD application: %w\n%s", runErr, output)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	var assistantAssetGoTestOutput string
	if err := runSegment("render and compile assistant asset registry", func() error {
		var compileErr error
		assistantAssetGoTestOutput, compileErr = runHarnessAssistantAssetRegistryCompile(ctx, probeRoot, appRoot, providerCompiled)
		return compileErr
	}); err != nil {
		return summary, nil, err
	}

	invalidImplementationRoot := filepath.Join(probeRoot, "invalid-implementation")
	if err := runSegment("prepare invalid implementation fixture", func() error {
		if err := copyHarnessNativeContractFixture(repoRoot, invalidImplementationRoot); err != nil {
			return err
		}
		return configureHarnessInvalidImplementationFixture(invalidImplementationRoot)
	}); err != nil {
		return summary, nil, err
	}
	var invalidImplementationDiagnostics int
	if err := runSegment("verify invalid native implementation", func() error {
		compiled, compileErr := compiler.Compile(invalidImplementationRoot)
		if compileErr != nil {
			return compileErr
		}
		if compiled.ContractStatus != "valid" {
			return fmt.Errorf("invalid-implementation fixture contract status = %q", compiled.ContractStatus)
		}
		implementationDiagnostics := generate.VerifyImplementation(compiled)
		invalidImplementationDiagnostics = len(implementationDiagnostics)
		if invalidImplementationDiagnostics == 0 {
			return fmt.Errorf("broken native implementation unexpectedly verified")
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	var bootstrapChanged int
	if err := runSegment("bootstrap contracts for invalid implementation", func() error {
		result, generateErr := generate.GenerateGoContracts(invalidImplementationRoot, false)
		bootstrapChanged = len(result.Changed)
		if generateErr != nil {
			return generateErr
		}
		if bootstrapChanged == 0 {
			return fmt.Errorf("invalid implementation bootstrap produced no contract artifacts")
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	overlayRoot := filepath.Join(probeRoot, "native-overlay")
	if err := runSegment("prepare source-only native fixture", func() error {
		if err := copyHarnessNativeContractFixture(repoRoot, overlayRoot); err != nil {
			return err
		}
		for _, path := range []string{filepath.Join(overlayRoot, "house", "scenerycontract"), filepath.Join(overlayRoot, "internal", "scenerygen")} {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	var overlayCompiled *compiler.Result
	if err := runSegment("apply native implementation check through generated overlay", func() error {
		compiled, compileErr := compiler.Compile(overlayRoot)
		if compileErr != nil {
			return compileErr
		}
		overlayCompiled = compiled
		generate.ApplyImplementationCheck(compiled)
		if compiled.ImplementationStatus != "valid" {
			return fmt.Errorf("native overlay implementation status = %q, diagnostics: %#v", compiled.ImplementationStatus, compiled.Diagnostics)
		}
		for _, path := range []string{filepath.Join(overlayRoot, "house", "scenerycontract"), filepath.Join(overlayRoot, "internal", "scenerygen")} {
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				return fmt.Errorf("native overlay verification materialized %s", path)
			}
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	var editorGoTestOutput string
	if err := runSegment("sync editor workspace and compile raw Go application", func() error {
		if err := generate.SyncEditorWorkspace(overlayCompiled); err != nil {
			return err
		}
		command := exec.CommandContext(ctx, "go", "test", "./...")
		command.Dir = overlayRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=auto", "GOMAXPROCS=2")
		output, runErr := command.CombinedOutput()
		editorGoTestOutput = strings.TrimSpace(string(output))
		if runErr != nil {
			return fmt.Errorf("go test raw editor workspace: %w\n%s", runErr, output)
		}
		for _, path := range []string{filepath.Join(overlayRoot, "house", "scenerycontract"), filepath.Join(overlayRoot, "internal", "scenerygen")} {
			if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
				return fmt.Errorf("editor workspace materialized %s", path)
			}
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	mergeRoot := filepath.Join(probeRoot, "merged-editor-workspace")
	var mergedEditorGoTestOutput string
	if err := runSegment("merge user editor workspace and resolve generated contracts", func() error {
		if err := copyHarnessNativeContractFixture(repoRoot, mergeRoot); err != nil {
			return err
		}
		for _, path := range []string{filepath.Join(mergeRoot, "house", "scenerycontract"), filepath.Join(mergeRoot, "internal", "scenerygen")} {
			if err := os.RemoveAll(path); err != nil {
				return err
			}
		}
		compiled, compileErr := compiler.Compile(mergeRoot)
		if compileErr != nil {
			return compileErr
		}
		workPath := filepath.Join(mergeRoot, "go.work")
		authored := []byte("go 1.27.0\n\nuse .\n\n// user-owned sentinel\n")
		if err := os.WriteFile(workPath, authored, 0o644); err != nil {
			return err
		}
		if err := generate.SyncEditorWorkspaceMerge(compiled); err != nil {
			return err
		}
		merged, err := os.ReadFile(workPath)
		if err != nil {
			return err
		}
		if !bytes.Contains(merged, authored) || !bytes.Contains(merged, []byte("// scenery:begin managed editor contracts")) {
			return fmt.Errorf("merged editor workspace lost user or managed bytes:\n%s", merged)
		}
		command := exec.CommandContext(ctx, "go", "test", "./...")
		command.Dir = mergeRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=auto", "GOMAXPROCS=2")
		output, runErr := command.CombinedOutput()
		mergedEditorGoTestOutput = strings.TrimSpace(string(output))
		if runErr != nil {
			return fmt.Errorf("go test merged editor workspace: %w\n%s", runErr, output)
		}
		changed := bytes.Replace(merged, []byte("scenery:begin"), []byte("scenery:changed"), 1)
		if bytes.Equal(changed, merged) {
			return fmt.Errorf("merged editor workspace has no managed marker")
		}
		if err := os.WriteFile(workPath, changed, 0o644); err != nil {
			return err
		}
		if syncErr := generate.SyncEditorWorkspace(compiled); syncErr == nil || !strings.Contains(syncErr.Error(), "managed block") {
			return fmt.Errorf("tampered merged editor workspace error = %v", syncErr)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	nestedRoot := filepath.Join(probeRoot, "nested-contract")
	if err := runSegment("prepare nested exported contract fixture", func() error {
		return configureHarnessNestedContractFixture(repoRoot, nestedRoot)
	}); err != nil {
		return summary, nil, err
	}
	if err := runSegment("generate nested exported contract closure", func() error {
		_, generateErr := generate.GenerateGoContracts(nestedRoot, false)
		return generateErr
	}); err != nil {
		return summary, nil, err
	}
	var nestedGoTestOutput string
	if err := runSegment("compile nested exported contract closure", func() error {
		command := exec.CommandContext(ctx, "go", "test", "-mod=mod", "./parent/scenerycontract")
		command.Dir = nestedRoot
		command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off", "GOMAXPROCS=2")
		output, runErr := command.CombinedOutput()
		nestedGoTestOutput = strings.TrimSpace(string(output))
		if runErr != nil {
			return fmt.Errorf("go test nested exported contract closure: %w\n%s", runErr, output)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	libraryRoot := filepath.Join(probeRoot, "generated-library-facade")
	if err := runSegment("prepare generated library facade fixture", func() error {
		if err := copyHarnessNativeContractFixture(repoRoot, libraryRoot); err != nil {
			return err
		}
		return configureHarnessGeneratedLibraryFacadeFixture(libraryRoot)
	}); err != nil {
		return summary, nil, err
	}
	var libraryVerificationPatterns []string
	if err := runSegment("resolve generated library facade through Go toolchain", func() error {
		compiled, compileErr := compiler.Compile(libraryRoot)
		if compileErr != nil {
			return compileErr
		}
		if !compiled.Valid() {
			return fmt.Errorf("generated library facade graph is invalid: %#v", compiled.Diagnostics)
		}
		libraryVerificationPatterns, compileErr = generate.GoVerificationPatterns(compiled)
		if compileErr != nil {
			return compileErr
		}
		if !slices.Contains(libraryVerificationPatterns, "./pkg/geometry/scenerylib_geometry") {
			return fmt.Errorf("generated library facade verification pattern is missing: %#v", libraryVerificationPatterns)
		}
		if implementationDiagnostics := generate.VerifyImplementation(compiled); len(implementationDiagnostics) != 0 {
			return fmt.Errorf("generated library facade implementation diagnostics: %#v", implementationDiagnostics)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	summary["proof"] = "generation_external_boundaries_passed"
	summary["provider_crud_proof"] = "generated_provider_crud_adapter_compiles_in_disposable_clone"
	summary["contract_revision"] = contractRevision
	summary["adapter_path"] = adapterPath
	summary["changed_files"] = len(changed)
	summary["go_test_output"] = goTestOutput
	summary["assistant_asset_registry_proof"] = "generated_embedded_asset_package_compiled_by_real_go_toolchain"
	summary["assistant_asset_go_test_output"] = assistantAssetGoTestOutput
	summary["invalid_implementation_proof"] = "contract_bootstrap_succeeds_after_real_implementation_verification_fails"
	summary["invalid_implementation_diagnostics"] = invalidImplementationDiagnostics
	summary["bootstrap_changed_files"] = bootstrapChanged
	summary["native_overlay_proof"] = "real_implementation_check_applied_without_materialized_generated_tree"
	summary["editor_workspace_proof"] = "raw_go_test_passed_against_external_generated_contract_modules"
	summary["editor_go_test_output"] = editorGoTestOutput
	summary["merged_editor_workspace_proof"] = "user_go_work_preserved_and_real_go_test_resolved_generated_contracts"
	summary["merged_editor_go_test_output"] = mergedEditorGoTestOutput
	summary["nested_contract_proof"] = "nested_exported_type_contract_closure_compiled"
	summary["nested_go_test_output"] = nestedGoTestOutput
	summary["generated_library_facade_proof"] = "generated_facade_resolved_by_real_go_toolchain"
	summary["generated_library_verification_patterns"] = libraryVerificationPatterns
	return summary, nil, nil
}

func runHarnessAssistantAssetRegistryCompile(ctx context.Context, probeRoot, appRoot string, result *compiler.Result) (string, error) {
	if result == nil {
		return "", fmt.Errorf("compiled provider fixture is unavailable")
	}
	archiveRoot := filepath.Join(probeRoot, "assistant-assets")
	nodeRoot := filepath.Join(archiveRoot, "node")
	capsuleRoot := filepath.Join(archiveRoot, "capsule")
	for root, payload := range map[string]string{nodeRoot: "node-runtime", capsuleRoot: "assistant-capsule"} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(root, "payload.txt"), []byte(payload), 0o644); err != nil {
			return "", err
		}
	}
	node, err := runtimeassets.BuildArchive(nodeRoot)
	if err != nil {
		return "", err
	}
	capsule, err := runtimeassets.BuildArchive(capsuleRoot)
	if err != nil {
		return "", err
	}
	nodeDescriptor, err := json.Marshal(node.Descriptor)
	if err != nil {
		return "", err
	}
	capsuleDescriptor, err := json.Marshal(capsule.Descriptor)
	if err != nil {
		return "", err
	}
	descriptor := generate.AssistantAssetDescriptor{
		Kind:                 generate.AssistantAssetDescriptorKind,
		SchemaRevision:       generate.AssistantAssetSchemaRevision,
		AssistantAddress:     "assistant/release-probe",
		Target:               runtime.GOOS + "/" + runtime.GOARCH,
		RuntimeRevision:      "release-probe-runtime",
		CapabilityRevision:   "sha256:" + strings.Repeat("1", 64),
		NodeArchiveDigest:    node.ArchiveDigest,
		NodeTreeDigest:       node.Descriptor.Digest,
		CapsuleArchiveDigest: capsule.ArchiveDigest,
		CapsuleTreeDigest:    capsule.Descriptor.Digest,
		CapsuleEntry:         generate.AssistantAssetCapsuleEntry,
		PackageLockDigest:    "sha256:" + strings.Repeat("2", 64),
	}
	files, err := generate.RenderAssistantAssetRegistry(result, []generate.AssistantAssetInput{{
		Descriptor: descriptor, NodeArchive: node.Data, NodeDescriptorJSON: nodeDescriptor,
		CapsuleArchive: capsule.Data, CapsuleDescriptorJSON: capsuleDescriptor,
	}})
	if err != nil {
		return "", err
	}
	for relative, data := range files {
		path := filepath.Join(appRoot, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return "", err
		}
	}
	command := exec.CommandContext(ctx, "go", "test", "./internal/scenerygen/assets")
	command.Dir = appRoot
	command.Env = envWithOverrides(envpolicy.Environ(), "GOWORK=off", "GOMAXPROCS=2")
	output, err := command.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("go test generated assistant asset registry: %w\n%s", err, output)
	}
	return strings.TrimSpace(string(output)), nil
}

func configureHarnessProviderCRUDFixture(appRoot string) error {
	appPath := filepath.Join(appRoot, "app.scn")
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	updatedApp := strings.Replace(string(appSource), "    gateway = http_gateway.public_api", "    gateway  = http_gateway.public_api\n    database = data_source.house_database", 1)
	if updatedApp == string(appSource) {
		return fmt.Errorf("native fixture module input shape changed")
	}
	updatedApp += `

provider "postgres" {
  source = "registry.scenery.dev/core/postgres"
}

data_source "house_database" {
  provider             = provider.postgres
  lifecycle            = "external"
  require_capabilities = ["sql.query/v1", "sql.transaction/v1"]
  config                = { database = "house" }
}
`
	if err := os.WriteFile(appPath, []byte(updatedApp), 0o644); err != nil {
		return err
	}

	packagePath := filepath.Join(appRoot, "house", "package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		return err
	}
	packageSource = append(packageSource, []byte(`

input "database" { type = resource_ref("data_source") }

record "scene_row" {
  field "id" { type = uuid }
  field "tenant_id" { type = string }
  field "name" { type = string }
  field "created_at" { type = datetime }
}

entity "scene" {
  type        = record.scene_row
  data_source = var.database
  mapping { relation = "scenes" }
  field "id" {
    column      = "id"
    primary_key = true
    default { strategy = "uuid_v7" }
  }
  field "tenant_id" {
    column     = "tenant_id"
    tenant_key = true
    immutable  = true
  }
  field "name" { column = "name" }
  field "created_at" { column = "created_at" }
}

crud "scene_api" {
  entity         = entity.scene
  implementation = std.crud.entity
  actions        = ["list", "get", "create", "update", "delete"]
  execution {
    mode    = "direct"
    timeout = "15s"
  }
  list {
    filters       = ["name", "created_at"]
    search        = ["tenant_id"]
    sorts         = ["name"]
    default_sort  = { field = "name", direction = "asc" }
    max_page_size = 25
  }
  http {
    path           = "/scenes"
    codec_profile  = std.codec.http_json_v1
    gateway        = var.gateway
    authentication = std.authentication.none
    authorization  = std.authorization.public
    pipeline       = std.pipeline.empty
  }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		return err
	}

	integrity, ok := compiler.BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		return fmt.Errorf("built-in PostgreSQL provider lock is unavailable")
	}
	lock := fmt.Sprintf("lock {}\nprovider \"postgres\" {\n  source = \"registry.scenery.dev/core/postgres\"\n  integrity = %q\n}\n", integrity)
	return os.WriteFile(filepath.Join(appRoot, scn.AppLockFilename), []byte(lock), 0o644)
}

func configureHarnessInvalidImplementationFixture(appRoot string) error {
	servicePath := filepath.Join(appRoot, "house", "service.go")
	service, err := os.ReadFile(servicePath)
	if err != nil {
		return err
	}
	broken := bytes.Replace(service, []byte("ProcessScene(_"), []byte("ProcessSceneBroken(_"), 1)
	if bytes.Equal(broken, service) {
		return fmt.Errorf("native fixture implementation method shape changed")
	}
	if err := os.WriteFile(servicePath, broken, 0o644); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Join(appRoot, "house", "scenerycontract"))
}

func configureHarnessGeneratedLibraryFacadeFixture(appRoot string) error {
	appPath := filepath.Join(appRoot, scn.AppFilename)
	appSource, err := os.ReadFile(appPath)
	if err != nil {
		return err
	}
	appSource = append(appSource, []byte(`

module "geometry" {
  source = "./pkg/geometry"
}
`)...)
	if err := os.WriteFile(appPath, appSource, 0o644); err != nil {
		return err
	}

	packageRoot := filepath.Join(appRoot, "pkg", "geometry")
	if err := os.MkdirAll(packageRoot, 0o755); err != nil {
		return err
	}
	packageSource := `package "geometry" {
  go_contract { import_path = "example.test/nativeapp/pkg/geometry" }
}

library "geometry" {
  runtime = "go"
  package = "example.test/nativeapp/pkg/geometry"
  version = "v1.0.0"
  artifact { name = "geometry" }
}

record "process_input" {
  field "value" { type = string }
}

record "process_result" {
  field "value" { type = string }
}

operation "process" {
  library = library.geometry
  input = record.process_input
  handler { method = "Process" }
  result "processed" { type = record.process_result }
}
`
	if err := os.WriteFile(filepath.Join(packageRoot, scn.PackageFilename), []byte(packageSource), 0o644); err != nil {
		return err
	}
	implementation := `package geometry

import (
	"context"
	contract "example.test/nativeapp/pkg/geometry/scenerycontract"
)

func Process(_ context.Context, input contract.ProcessInput) (contract.ProcessOutcome, error) {
	return contract.ProcessProcessed{Value: contract.ProcessResult{Value: input.Value}}, nil
}
`
	if err := os.WriteFile(filepath.Join(packageRoot, "library.go"), []byte(implementation), 0o644); err != nil {
		return err
	}
	consumer := `package nativeapp

import (
	"context"
	contract "example.test/nativeapp/pkg/geometry/scenerycontract"
	library "example.test/nativeapp/pkg/geometry/scenerylib_geometry"
)

func consumeGeneratedLibrary() {
	_, _ = library.Process(context.Background(), contract.ProcessInput{})
}
`
	return os.WriteFile(filepath.Join(appRoot, "library_consumer.go"), []byte(consumer), 0o644)
}

func findHarnessProviderCRUDAdapter(appRoot string) (string, error) {
	generatedRoot := filepath.Join(appRoot, "internal", "scenerygen")
	var found string
	err := filepath.WalkDir(generatedRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("type providerCRUDService struct")) && bytes.Contains(data, []byte("datasource.InvokeCRUD")) {
			found, err = filepath.Rel(appRoot, path)
			if err != nil {
				return err
			}
			return fs.SkipAll
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	if found == "" {
		return "", fmt.Errorf("generated provider CRUD adapter is missing")
	}
	return filepath.ToSlash(found), nil
}

func configureHarnessNestedContractFixture(repoRoot, appRoot string) error {
	files := map[string]string{
		"go.mod": "module example.test/cross\n\ngo 1.27.0\n\nrequire scenery.sh v0.0.0\nreplace scenery.sh => " + filepath.ToSlash(repoRoot) + "\n",
		"app.scn": `workspace {
  managed_generated_roots = ["parent/scenerycontract", "internal/scenerygen"]
}
go_module "application" {
  root = "."
  import_path = "example.test/cross"
}
go_toolchain "application" {
  version = "1.27.0"
  experiments = []
}
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
  cgo = "disabled"
}
application "cross_module" {}
module "parent" { source = "./parent" }
`,
		"parent/package.scn": `package "parent" {
  go_contract { import_path = "example.test/cross/parent" }
}
module "geometry" { source = "../geometry" }
service "parent" {
  runtime = "go"
  implementation { constructor = "NewService" }
}
record "shape" {
  field "point" { type = module.geometry.point }
}
operation "inspect" {
  service = service.parent
  input = record.shape
  handler { method = "Inspect" }
  result "ok" { type = module.geometry.point }
}
export "shape" { value = record.shape }
`,
		"geometry/package.scn": `package "geometry" {}
record "point" {
  field "x" { type = float64 }
  field "y" { type = float64 }
}
export "point" { value = record.point }
`,
	}
	for rel, body := range files {
		path := filepath.Join(appRoot, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			return err
		}
	}
	return nil
}
