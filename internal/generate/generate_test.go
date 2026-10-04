package generate

import (
	"bytes"
	"encoding/json"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

func TestGeneratedDescriptorStalenessIgnoresProducerProvenance(t *testing.T) {
	for _, test := range []struct {
		name, kind string
		schema     any
	}{
		{"scenery.generated.json", goApplicationDescriptorKind, goApplicationSchemaDescriptor},
		{"scenery.package-generated.json", goPackageDescriptorKind, goPackageSchemaDescriptor},
		{"scenery.typescript-client-generated.json", typeScriptDescriptorKind, typeScriptSchemaDescriptor},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, test.name)
			descriptor := addGeneratedArtifactIdentity(map[string]any{"content_digest": "sha256:" + strings.Repeat("a", 64), "files": []string{}}, test.kind, test.schema, "")
			expected, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			descriptor["producer"] = machine.Producer{Version: "v0.3.2-test", Toolchain: machine.Toolchain{GoVersion: "go-test"}}
			current, err := json.Marshal(descriptor)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, current, 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := inspectGeneratedFiles(root, []generatedFile{{Path: path, Bytes: expected}})
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Changed) != 0 {
				t.Fatalf("producer-only drift marked descriptor stale: %#v", result.Changed)
			}
			current[len(current)-1] ^= 1
			if generatedFileBytesEqual(path, current, expected) {
				t.Fatal("non-producer descriptor corruption was ignored")
			}
		})
	}
}

func TestHermeticModuleCacheDiagnosticIsActionableAndBounded(t *testing.T) {
	missing := []string{"example.test/a", "example.test/b", "example.test/c", "example.test/d", "example.test/e", "example.test/f"}
	diagnostic := hermeticModuleCacheDiagnostic("app/go_target/development", missing)
	if diagnostic.Code != "SCN6202" || diagnostic.Address != "app/go_target/development" {
		t.Fatalf("diagnostic identity = %#v", diagnostic)
	}
	if !strings.Contains(diagnostic.Message, "hermetic module cache is missing 6 imported packages") ||
		!strings.Contains(diagnostic.Message, "example.test/a") ||
		!strings.Contains(diagnostic.Message, "(+1 more)") ||
		strings.Contains(diagnostic.Message, "example.test/f") {
		t.Fatalf("diagnostic message = %q", diagnostic.Message)
	}
	if len(diagnostic.Suggestions) != 1 || !strings.Contains(diagnostic.Suggestions[0], "go mod download") {
		t.Fatalf("diagnostic suggestions = %#v", diagnostic.Suggestions)
	}
	if got, ok := diagnostic.Details["missing_packages"].([]string); !ok || !slices.Equal(got, missing) {
		t.Fatalf("missing package details = %#v", diagnostic.Details)
	}
}

func TestGenerateGoContractsAreStable(t *testing.T) {
	temp := t.TempDir()
	writeMinimalGenerationFixture(t, temp)
	result, err := compiler.Compile(temp)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	files, err := renderGoContractFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 {
		t.Fatalf("Go files = %#v", files)
	}
	writeRenderedFixture(t, files)
	if _, err := GenerateGoContractsFromResult(result, true); err != nil {
		t.Fatal(err)
	}
}

func TestGenerateTypeScriptClientsAreStable(t *testing.T) {
	temp := t.TempDir()
	writeMinimalGenerationFixture(t, temp)
	result, err := compiler.Compile(temp)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	files, err := renderTypeScriptClientFiles(result, "public_api")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 6 {
		t.Fatalf("TS files = %#v", files)
	}
	writeRenderedFixture(t, files)
	if _, err := GenerateTypeScriptClientsFromResult(result, "public_api", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"types.ts", "runtime.ts", "client.ts", "metadata.ts", "index.ts", "scenery.typescript-client-generated.json"} {
		if _, err := os.Stat(filepath.Join(temp, "clients", "generated", "public_api", path)); err != nil {
			t.Error(err)
		}
	}
}

func TestTypeScriptOutputRequiresDeclaredManagedGeneratedRoot(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	if err := os.RemoveAll(filepath.Join(root, "clients")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, testAppFilename)
	source, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(source, []byte(`"clients/generated/public_api"`), []byte(`"managed"`), 1)
	if bytes.Equal(updated, source) {
		t.Fatal("fixture does not declare the TypeScript managed root")
	}
	source = updated
	if err := os.WriteFile(path, source, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateTypeScriptClients(root, "public_api", false); err == nil || !strings.Contains(err.Error(), "managed generated root") {
		t.Fatalf("generation error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "clients")); !os.IsNotExist(err) {
		t.Fatal("generation wrote outside the declared managed root")
	}
}

func TestGenerateAllDoesNotCommitGoArtifactsWhenTypeScriptValidationFails(t *testing.T) {
	root := generatedOrdinaryGoFixture(t)
	goArtifact := filepath.Join(root, "house", "scenerycontract", "scenery.package-generated.json")
	before, err := os.ReadFile(goArtifact)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, testAppFilename)
	source, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(source, []byte(`"clients/generated/public_api"`), []byte(`"managed"`), 1)
	if bytes.Equal(updated, source) {
		t.Fatal("fixture does not declare the TypeScript managed root")
	}
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := GenerateAll(root, false); err == nil || !strings.Contains(err.Error(), "managed generated root") {
		t.Fatalf("generation error = %v", err)
	}
	got, err := os.ReadFile(goArtifact)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, before) {
		t.Fatal("combined generation committed Go artifacts before TypeScript validation completed")
	}
}

func TestExportedFixtureProducesNativeTypeScriptClientBinding(t *testing.T) {
	parallelIntegrationTest(t)

	result, err := compiler.Compile(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil || result.Manifest == nil {
		t.Fatalf("compile: %v %#v", err, result)
	}
	targets := typescriptTargets(result.Manifest.Resources, "public_api")
	if len(targets) != 1 {
		t.Fatalf("targets = %#v", resourceAddresses(targets))
	}
	bindings := publicHTTPBindings(result.Manifest.Resources, targets[0])
	if len(bindings) != 1 || bindings[0].Address != "house/binding/process_scene_http" {
		exported, declared := exportedOperations(result.Manifest.Resources)
		t.Fatalf("bindings = %#v, exported = %#v, declared = %#v", resourceAddresses(bindings), exported, declared)
	}
	client := renderTSClient(targets[0], bindings, result.Manifest.Resources)
	if !strings.Contains(client, "this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);") {
		t.Fatalf("generated fixture client does not bind the default fetch:\n%s", client)
	}
	if !strings.Contains(client, `"status":200`) || strings.Contains(client, `"status":0`) || strings.Contains(client, `"path":null`) {
		t.Fatalf("generated fixture client lost exact response status/path semantics:\n%s", client)
	}
	if strings.Contains(client, `"query":[]`) || strings.Contains(client, `"cookies":[]`) {
		t.Fatalf("generated fixture client emits empty mapping collections:\n%s", client)
	}
}

func TestNativeFixtureRendersContractAndApplicationArtifacts(t *testing.T) {
	parallelIntegrationTest(t)

	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	files, err := RenderGoWorkspaceFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 7 {
		t.Fatalf("generated paths = %#v", files)
	}
	if _, ok := files["internal/scenerygen/assets/assets.gen.go"]; !ok {
		t.Fatalf("generated paths do not include the empty assistant asset registry: %#v", files)
	}
	plan, err := BuildRuntimeIntegrationPlan(result)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CompositionImport != "example.test/nativeapp/internal/scenerygen/composition" {
		t.Fatalf("runtime plan = %#v", plan)
	}
}

func TestTypeScriptCacheMaterializationDoesNotAffectCheck(t *testing.T) {
	root := t.TempDir()
	writeMinimalGenerationFixture(t, root)
	path := filepath.Join(root, testAppFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte("typescript_client \"public_api\" {"), []byte("typescript_client \"public_api\" {\n  materialization = \"cache\""), 1)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	files, err := renderCachedTypeScriptClients(result)
	if err != nil {
		t.Fatal(err)
	}
	writeRenderedFixture(t, files)
	cacheFile := filepath.Join(root, ".scenery", "gen", "typescript", "public_api", "client.ts")
	if _, err := os.Stat(cacheFile); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, ".scenery", "gen")); err != nil {
		t.Fatal(err)
	}
	result, err = compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check requires disposable TypeScript cache: %v %#v", err, result.Diagnostics)
	}
}

func TestCacheTypeScriptTargetsExcludesSourceTargets(t *testing.T) {
	targets := []Resource{
		{Name: "source", Spec: map[string]any{"materialization": "source", "react": map[string]any{"tsconfig": "tsconfig.json"}}},
		{Name: "cache", Spec: map[string]any{"materialization": "cache", "react": map[string]any{"tsconfig": "tsconfig.json"}}},
	}
	got := cacheTypeScriptTargets(targets)
	if len(got) != 1 || got[0].Name != "cache" {
		t.Fatalf("cache targets = %#v", got)
	}
}

func TestNativeImplementationVerificationOverlayStaysInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	result := nativeApplicationGenerationFixture(root)
	index := newResourceIndex(result.Manifest.Resources)
	files, err := generateModuleContract(result, index, resourceByKind(result.Manifest.Resources, "scenery.module"))
	if err != nil {
		t.Fatal(err)
	}
	applicationFiles, err := generateApplicationArtifacts(result, index, newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	files = append(files, applicationFiles...)
	overlay, err := generatedGoVerificationOverlay(files)
	if err != nil {
		t.Fatal(err)
	}
	if len(overlay) == 0 {
		t.Fatal("verification overlay has no generated Go files")
	}
	for _, path := range []string{filepath.Join(root, "house", "scenerycontract"), filepath.Join(root, "internal", "scenerygen")} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("verification overlay materialized %s", path)
		}
	}
}

func TestGenerateBootstrapsContractArtifactsForInvalidImplementationInProcess(t *testing.T) {
	t.Parallel()

	result := nativeApplicationGenerationFixture(t.TempDir())
	result.ImplementationStatus = "invalid"
	result.Diagnostics = []Diagnostic{{Code: "SCN6202", Severity: "error", Message: "implementation method mismatch"}}
	index := newResourceIndex(result.Manifest.Resources)
	generated, err := generateModuleContract(result, index, resourceByKind(result.Manifest.Resources, "scenery.module"))
	if err != nil {
		t.Fatalf("bootstrap generation failed: %v", err)
	}
	applicationFiles, err := generateApplicationArtifacts(result, index, newProjectionInput(result))
	if err != nil {
		t.Fatalf("bootstrap application generation failed: %v", err)
	}
	generated = append(generated, applicationFiles...)
	if len(generated) == 0 {
		t.Fatal("bootstrap generation produced no contract artifacts")
	}
}

func TestGenerationCheckRejectsStaleArtifactsWithoutWriting(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), temp)
	generated := filepath.Join(temp, "house", "scenerycontract", "contract.gen.go")
	if err := os.WriteFile(generated, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := GenerateGoContracts(temp, true); err == nil {
		t.Fatalf("generation check error = %v", err)
	}
	data, err := os.ReadFile(generated)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "stale\n" {
		t.Fatalf("check rewrote generated artifact: %q", data)
	}
}

func TestAtomicWriteSetLeavesEveryOriginalOnPreflightFailure(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "first.go")
	second := filepath.Join(root, "second.go")
	if err := os.WriteFile(first, []byte("old first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "target.go"), []byte("target\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.go", second); err != nil {
		t.Fatal(err)
	}
	err := atomicWriteSet(root, []generatedFile{{Path: first, Bytes: []byte("new first\n")}, {Path: second, Bytes: []byte("new second\n")}})
	if err == nil {
		t.Fatal("generated symlink was accepted")
	}
	data, readErr := os.ReadFile(first)
	if readErr != nil || string(data) != "old first\n" {
		t.Fatalf("first artifact changed: %q, %v", data, readErr)
	}
	entries, readErr := os.ReadDir(root)
	if readErr != nil {
		t.Fatal(readErr)
	}
	for _, entry := range entries {
		if strings.Contains(entry.Name(), ".scenery-stage-") || strings.Contains(entry.Name(), ".scenery-backup-") {
			t.Fatalf("transaction file remains: %s", entry.Name())
		}
	}
}

func TestAtomicWriteSetRejectsSymlinkedOutputParent(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "generated")); err != nil {
		t.Fatal(err)
	}
	err := atomicWriteSet(root, []generatedFile{{Path: filepath.Join(root, "generated", "client.ts"), Bytes: []byte("outside\n")}})
	if err == nil || !strings.Contains(err.Error(), "contains symlink") {
		t.Fatalf("atomicWriteSet() error = %v, want symlink rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "client.ts")); !os.IsNotExist(err) {
		t.Fatalf("outside artifact exists: %v", err)
	}
}

func TestArtifactDigestLengthFramesPathsAndContents(t *testing.T) {
	root := t.TempDir()
	joined := artifactDigest(root, []generatedFile{{Path: filepath.Join(root, "a"), Bytes: []byte("xb\x00y")}})
	split := artifactDigest(root, []generatedFile{
		{Path: filepath.Join(root, "a"), Bytes: []byte("x")},
		{Path: filepath.Join(root, "b"), Bytes: []byte("y")},
	})
	if joined == split {
		t.Fatal("artifact digest did not frame file contents")
	}
}

func TestGeneratedDescriptorOwnershipPrunesRetiredFiles(t *testing.T) {
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "internal", "scenerygen")
	oldAdapter := filepath.Join(generatedRoot, "retired", "adapter.gen.go")
	descriptorPath := filepath.Join(generatedRoot, "scenery.generated.json")
	if err := os.MkdirAll(filepath.Dir(oldAdapter), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBytes := []byte("// Code generated by Scenery. DO NOT EDIT.\npackage retired\n")
	if err := os.WriteFile(oldAdapter, oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, _ := json.Marshal(addGeneratedArtifactIdentity(map[string]any{"content_digest": artifactDigest(generatedRoot, []generatedFile{{Path: oldAdapter, Bytes: oldBytes}}), "files": []string{"retired/adapter.gen.go"}}, goApplicationDescriptorKind, goApplicationSchemaDescriptor, ""))
	if err := os.WriteFile(descriptorPath, descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	expected := []generatedFile{{Path: descriptorPath, Bytes: []byte(`{"files":[]}`)}}
	files, err := includeStaleGeneratedFiles(root, expected, map[string]bool{"scenery.generated.json": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	result, err := inspectGeneratedFiles(root, files)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(result.Changed, "internal/scenerygen/retired/adapter.gen.go") {
		t.Fatalf("changed = %#v, want retired adapter", result.Changed)
	}
	if err := atomicWriteSet(root, files); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(oldAdapter); !os.IsNotExist(err) {
		t.Fatalf("retired adapter remains: %v", err)
	}
}

func TestGeneratedDescriptorRetirementIgnoresAgentWorktrees(t *testing.T) {
	root := t.TempDir()
	for _, toolDirectory := range []string{".agents", ".claude", ".codex"} {
		descriptorPath := filepath.Join(root, toolDirectory, "worktrees", "other", "scenery.generated.json")
		if err := os.MkdirAll(filepath.Dir(descriptorPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(descriptorPath, []byte("not a Scenery descriptor"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := includeStaleGeneratedFiles(root, nil, map[string]bool{"scenery.generated.json": true}, nil); err != nil {
		t.Fatalf("agent worktree descriptor affected the app root: %v", err)
	}
}

func TestGeneratedDescriptorCannotClaimHandwrittenFileOutsideOutputRoot(t *testing.T) {
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "generated")
	if err := os.MkdirAll(generatedRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	handwritten := filepath.Join(root, "service.go")
	if err := os.WriteFile(handwritten, []byte("package app\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptorPath := filepath.Join(generatedRoot, "scenery.generated.json")
	descriptor, _ := json.Marshal(addGeneratedArtifactIdentity(map[string]any{"content_digest": "sha256:" + strings.Repeat("0", 64), "files": []string{"../service.go"}}, goApplicationDescriptorKind, goApplicationSchemaDescriptor, ""))
	if err := os.WriteFile(descriptorPath, descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := includeStaleGeneratedFiles(root, nil, map[string]bool{"scenery.generated.json": true}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe owned path") {
		t.Fatalf("error = %v, want unsafe owned path", err)
	}
	if _, err := os.Stat(handwritten); err != nil {
		t.Fatalf("handwritten file changed: %v", err)
	}
}

func TestGoGenerationCoversPreservingRecordsAndOpenUnions(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/item", Module: "house", Kind: "scenery.record", Name: "item", Spec: map[string]any{
			"unknown_fields": "preserve",
			"field": []any{
				map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}},
				map[string]any{"name": "tags", "type": map[string]any{"$expression": "set(string)"}},
			},
		}},
		{Address: "house/union/state", Module: "house", Kind: "scenery.union", Name: "state", Spec: map[string]any{
			"open": true, "discriminator": "kind", "unknown_variant": map[string]any{"preserve": true},
			"variant": []any{
				map[string]any{"name": "ready", "type": map[string]any{"$ref": "record.item"}},
			},
		}},
	}
	source := renderContractTypes(resources)
	for _, want := range []string{
		"UnknownFields map[string]scenery.JSON",
		"Tags scenery.Set[string]",
		"scenery.MarshalContractValue",
		"scenery.ValidateContractValue",
		"type State interface",
		"type StateReady struct",
		"type StateUnknown struct",
		"func MarshalStateJSON",
		"func UnmarshalStateJSON",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

func TestGoTupleMappingPreservesDeclaredPositionOrder(t *testing.T) {
	got := goTypeExpression("tuple(string, int64, list(uuid))")
	want := tupleGoTypeName("tuple(string,int64,list(uuid))")
	if got != want {
		t.Fatalf("tuple Go type = %q, want %q", got, want)
	}
	source := renderContractTypes([]Resource{{Address: "house/record/tuple_holder", Module: "house", Kind: "scenery.record", Name: "tuple_holder", Spec: map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$expression": "tuple(string, int64, list(uuid))"}}}}})
	for _, fragment := range []string{"type " + want + " struct", "Item0 string", "Item1 int64", "Item2 []scenery.UUID", "Value " + want} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("named tuple declaration missing %q:\n%s", fragment, source)
		}
	}
	if got := typeExpressionNames("tuple(record.zed, record.alpha)"); !slices.Equal(got, []string{"record.zed", "record.alpha"}) {
		t.Fatalf("tuple references reordered: %#v", got)
	}
}

func TestGoContractUsesQualifiedCrossModuleTypes(t *testing.T) {
	all := []Resource{
		{Address: "app/module/house", Module: "app", Kind: "scenery.module", Name: "house", Spec: map[string]any{"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/house"}}}},
		{Address: "app/module/geometry", Module: "app", Kind: "scenery.module", Name: "geometry", Spec: map[string]any{"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/geometry"}}}},
		{Address: "geometry/record/point", Module: "geometry", Kind: "scenery.record", Name: "point", Spec: map[string]any{"field": map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}}}},
		{Address: "geometry/union/location", Module: "geometry", Kind: "scenery.union", Name: "location", Spec: map[string]any{"variant": map[string]any{"name": "point", "type": map[string]any{"$ref": "record.point"}}}},
		{Address: "house/record/shape", Module: "house", Kind: "scenery.record", Name: "shape", Spec: map[string]any{"field": []any{
			map[string]any{"name": "point", "type": map[string]any{"$ref": "geometry/record/point"}},
			map[string]any{"name": "location", "type": map[string]any{"$ref": "geometry/union/location"}},
		}}},
	}
	resolver := newGoContractTypeResolver("house", all)
	source := renderContractTypesResolved([]Resource{all[4]}, resolver)
	if resolver.Err() != nil {
		t.Fatal(resolver.Err())
	}
	for _, fragment := range []string{
		`geometrycontract "example.test/geometry/scenerycontract"`,
		`Point geometrycontract.Point`,
		`Location geometrycontract.Location`,
		`case "geometry/union/location": return geometrycontract.UnmarshalLocationJSON(data)`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("qualified cross-module contract missing %q:\n%s", fragment, source)
		}
	}
	if _, err := format.Source([]byte(source)); err != nil {
		t.Fatalf("generated cross-module contract is invalid Go: %v\n%s", err, source)
	}
}

func TestTypeScriptGenerationCoversEnumsUnionsAndUnknownFields(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/item", Module: "house", Kind: "scenery.record", Name: "item", Spec: map[string]any{"unknown_fields": "preserve", "field": map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}}}},
		{Address: "house/enum/mode", Module: "house", Kind: "scenery.enum", Name: "mode", Spec: map[string]any{"open": true, "value": map[string]any{"name": "all", "wire_value": "all"}}},
		{Address: "house/union/state", Module: "house", Kind: "scenery.union", Name: "state", Spec: map[string]any{"open": true, "variant": map[string]any{"name": "ready", "type": map[string]any{"$ref": "record.item"}}}},
	}
	source := renderTSTypes(resources)
	for _, want := range []string{
		"readonly unknownFields: Readonly<Record<string, JsonValue>>",
		"export const Mode =",
		"__modeUnknown",
		`readonly kind: "ready"`,
		"readonly unknown: true",
	} {
		if !strings.Contains(source, want) {
			t.Errorf("missing %q in:\n%s", want, source)
		}
	}
}

func TestTypeScriptClientUsesExactResponseMapAndBigIntSafeEncoding(t *testing.T) {
	operation := Resource{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{
		"input":  map[string]any{"$ref": "record.input"},
		"result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.output"}},
		"error":  map[string]any{"name": "invalid", "type": map[string]any{"$ref": "std.type.problem"}},
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding", Name: "process", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process"},
		"http": map[string]any{"method": "POST", "path": "/process", "body": map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.process.input"}}, "response": []any{
			map[string]any{"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200", "body": map[string]any{"codec": "json"}},
			map[string]any{"name": "invalid", "when": map[string]any{"$ref": "error.invalid"}, "status": "422", "body": map[string]any{"codec": "problem_json"}},
		}},
	}}
	runtimeSource := renderTSRuntime(allTSRuntimeCapabilities())
	clientSource := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	for _, want := range []string{
		"export function jsonNumber",
		"export function parseExactJSON",
		"export function encodeTypedJSON",
		"duplicate object member",
		"Object.create(null)",
		"Object.is(value, -0)",
	} {
		if !strings.Contains(runtimeSource, want) {
			t.Errorf("runtime missing %q", want)
		}
	}
	for _, want := range []string{`"codec":"json"`, `"status":200`, `"status":422`, `"codec":"problem_json"`} {
		if !strings.Contains(clientSource, want) {
			t.Errorf("client missing %q in:\n%s", want, clientSource)
		}
	}
	for _, want := range []string{"export function encodeRequestBody", "export async function decodeResponseBody"} {
		if !strings.Contains(runtimeSource, want) {
			t.Errorf("runtime missing %q", want)
		}
	}
	if strings.Contains(clientSource, "response.json()") {
		t.Fatalf("client must not round exact JSON numbers through response.json():\n%s", clientSource)
	}
}

func TestTypeScriptClientUsesDeclaredPathQueryAndBodyMappings(t *testing.T) {
	operation := Resource{Address: "house/operation/update", Module: "house", Kind: "scenery.operation", Name: "update", Spec: map[string]any{
		"input":  map[string]any{"$ref": "record.update_input"},
		"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "json"}},
		"error":  map[string]any{"name": "invalid_input", "type": map[string]any{"$ref": "std.type.problem"}},
	}}
	binding := Resource{Address: "house/binding/update", Module: "house", Kind: "scenery.binding", Name: "update", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.update"},
		"http": map[string]any{
			"method": "PATCH", "path": "/scenes/{scene_id}",
			"path_parameter":  map[string]any{"name": "scene_id", "to": map[string]any{"$ref": "operation.update.input.scene_id"}},
			"query_parameter": map[string]any{"name": "tag", "to": map[string]any{"$ref": "operation.update.input.tags"}, "encoding": "repeated"},
			"header":          map[string]any{"name": "if-match", "to": map[string]any{"$ref": "operation.update.input.etag"}},
			"cookie":          map[string]any{"name": "tenant", "to": map[string]any{"$ref": "operation.update.input.tenant"}},
			"body":            map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.update.input.body"}},
			"response": []any{
				map[string]any{"name": "ok", "when": map[string]any{"$ref": "result.ok"}, "status": "200", "body": map[string]any{"codec": "json"}},
				map[string]any{"name": "invalid_input", "when": map[string]any{"$ref": "error.invalid_input"}, "status": "400", "body": map[string]any{"codec": "problem_json"}},
			},
		},
	}}
	source := renderTSClient(Resource{Name: "public"}, []Resource{binding}, []Resource{operation})
	for _, fragment := range []string{
		`"name":"scene_id"`,
		`"property":"sceneId"`,
		`"name":"tag"`,
		`"property":"tags"`,
		`"encoding":"repeated"`,
		`"name":"if-match"`,
		`"property":"etag"`,
		`"name":"tenant"`,
		`"property":"tenant"`,
		`"property":"body"`,
		`"codec":"problem_json"`,
		`"producedMediaTypes":["application/problem+json"]`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, source)
		}
	}
}

func TestTypeScriptReachabilityExcludesUnrelatedTypes(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/input", Module: "house", Kind: "scenery.record", Name: "input", Spec: map[string]any{"field": map[string]any{"name": "item", "type": map[string]any{"$ref": "record.item"}}}},
		{Address: "house/record/item", Module: "house", Kind: "scenery.record", Name: "item", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}},
		{Address: "house/record/unrelated", Module: "house", Kind: "scenery.record", Name: "unrelated", Spec: map[string]any{}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.item"}}}},
	}
	bindings := []Resource{{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.get"}}}}
	reachable := reachableResources(resources, bindings)
	addresses := resourceAddresses(reachable)
	want := []string{"house/operation/get", "house/record/input", "house/record/item"}
	if !slices.Equal(addresses, want) {
		t.Fatalf("reachable = %#v, want %#v", addresses, want)
	}
}

func TestTypeScriptReachabilityIncludesCanonicalCrossModuleTypes(t *testing.T) {
	resources := []Resource{
		{Address: "geometry/record/point", Module: "geometry", Kind: "scenery.record", Name: "point", Spec: map[string]any{"field": map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}}}},
		{Address: "house/record/shape", Module: "house", Kind: "scenery.record", Name: "shape", Spec: map[string]any{"field": map[string]any{"name": "point", "type": map[string]any{"$ref": "geometry/record/point"}}}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "house/record/shape"}}},
	}
	bindings := []Resource{{Address: "house/binding/get", Module: "house", Kind: "scenery.binding", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "house/operation/get"}}}}
	reachable := reachableResources(resources, bindings)
	want := []string{"geometry/record/point", "house/operation/get", "house/record/shape"}
	if got := resourceAddresses(reachable); !slices.Equal(got, want) {
		t.Fatalf("reachable = %#v, want %#v", got, want)
	}
	if generated := renderTSTypes(reachable); !strings.Contains(generated, "readonly point: Point") {
		t.Fatalf("cross-module field lost its type:\n%s", generated)
	}
	if registry := renderTSRegistry(reachable); !strings.Contains(registry, `"name":"geometry/record/point"`) {
		t.Fatalf("cross-module field lost its runtime descriptor:\n%s", registry)
	}
}

func TestTypeScriptExportSelectionAcceptsCanonicalModuleAddresses(t *testing.T) {
	module := Resource{Address: "app/module/house", Module: "app", Kind: "scenery.module", Name: "house", Spec: map[string]any{
		"exports": map[string]any{"operations": map[string]any{"get": map[string]any{"$ref": "house/operation/get"}}},
	}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation", Name: "get", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"input": map[string]any{"$ref": "string"}}}
	binding := Resource{Address: "house/binding/get_http", Module: "house", Kind: "scenery.binding", Name: "get_http", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "GET", "path": "/get", "guarantee": "framework_enforced"},
	}}
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client", Name: "public", Spec: map[string]any{"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}}}
	selected := publicHTTPBindings([]Resource{module, operation, binding}, target)
	if len(selected) != 1 || selected[0].Address != binding.Address {
		t.Fatalf("selected bindings = %#v, want %s", resourceAddresses(selected), binding.Address)
	}
}

func TestTypeScriptRetryRequiresIdempotentReplayableOperation(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Kind: "scenery.typescript-client", Name: "public", Module: "app", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "http_gateway.public"}}, "package": "@test/client", "module": "esm", "runtime": "fetch", "output_root": "generated/client",
		"retry": map[string]any{"policy": "scenery.retry.idempotent", "maximum_attempts": "3"},
	}}
	input := Resource{Address: "house/record/get_input", Kind: "scenery.record", Name: "get_input", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}}
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "record.get_input"}}}
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding", Name: "get", Module: "house", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "POST", "path": "/get", "body": map[string]any{"codec": "json"}},
	}}
	operation.Spec["idempotency"] = map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.id"}}}
	resources := []Resource{target, input, operation, binding}
	client := renderTSClient(target, []Resource{binding}, resources)
	if !strings.Contains(client, "maximumAttempts: 3") || !strings.Contains(client, "retryRuntime: this.#retryRuntime") {
		t.Fatalf("retry client missing policy:\n%s", client)
	}
	capabilities := tsRuntimeCapabilitiesFor(target, []Resource{binding}, resources)
	if !strings.Contains(renderTSRuntime(capabilities), "export async function fetchWithRetry") {
		t.Fatal("runtime missing fetchWithRetry")
	}
}

func TestGenerateApplicationArtifactsUsesExplicitRegistryAndComposition(t *testing.T) {
	root := t.TempDir()
	result := nativeApplicationGenerationFixture(root)
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string][]byte{}
	for _, file := range files {
		relative, _ := filepath.Rel(root, file.Path)
		byPath[filepath.ToSlash(relative)] = file.Bytes
	}
	adapter := string(byPath["internal/scenerygen/house_house_adapter/adapter.gen.go"])
	for _, fragment := range []string{"func Register(registry scenery.Registry) error", "RegisterNativeService", "RegisterEndpointChecked", "DecodeContractInput", "ProcessScene(ctx"} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
	if strings.Contains(adapter, "func init()") {
		t.Fatalf("adapter uses init registration:\n%s", adapter)
	}
	composition := string(byPath["internal/scenerygen/composition/composition.gen.go"])
	if !strings.Contains(composition, "adapter0.Register(registry)") || !strings.Contains(composition, result.Manifest.ContractRevision) {
		t.Fatalf("composition:\n%s", composition)
	}
	var descriptor map[string]any
	if err := json.Unmarshal(byPath["internal/scenerygen/scenery.generated.json"], &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor["kind"] != goApplicationDescriptorKind || descriptor["schema_revision"] == "" || descriptor["content_digest"] == "" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestGeneratedGoOperationOutcomeHasDeterministicDurableCodec(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	module := resourceByKind(result.Manifest.Resources, "scenery.module")
	files, err := generateModuleContract(result, newResourceIndex(result.Manifest.Resources), module)
	if err != nil {
		t.Fatal(err)
	}
	contract := generatedSourceWithSuffix(files, "/contract.gen.go")
	for _, fragment := range []string{
		"func MarshalProcessSceneOutcome(value ProcessSceneOutcome) ([]byte, error)",
		`MarshalContractOutcomeVariant("result", "processed", typed.Value, "record.process_scene_result")`,
		"func UnmarshalProcessSceneOutcome(data []byte) (ProcessSceneOutcome, error)",
		`case kind == "result" && name == "processed":`,
		`unmarshalGeneratedContractValue(payload, &value, "record.process_scene_result")`,
	} {
		if !strings.Contains(contract, fragment) {
			t.Fatalf("contract outcome codec missing %q:\n%s", fragment, contract)
		}
	}
}

func TestGenerateApplicationAdapterEmitsTypedPathMappingAndGatewayBasePath(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway", Name: "public", Spec: map[string]any{"base_path": "/api"}, Origin: Origin{Kind: "authored"}})
	for index := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[index]
		switch resource.Kind {
		case "scenery.binding":
			resource.Spec["gateway"] = map[string]any{"$ref": "app/http_gateway/public"}
			httpSpec := resource.Spec["http"].(map[string]any)
			httpSpec["method"] = "GET"
			httpSpec["path"] = "/house/process/{scene_id}"
			delete(httpSpec, "body")
			httpSpec["path_parameter"] = map[string]any{"name": "scene_id", "to": map[string]any{"$ref": "operation.process_scene.input.scene_id"}}
		}
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
	if err != nil {
		t.Fatal(err)
	}
	var adapter string
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/house_house_adapter/adapter.gen.go") {
			adapter = string(file.Bytes)
		}
	}
	for _, fragment := range []string{
		`Path: "/api/house/process/:scene_id"`,
		`Source: sceneryruntime.ContractSourcePath`,
		`Name: "scene_id"`,
		`Target: "scene_id"`,
		`Type: "string"`,
		`Body: nil`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestTypeScriptClientIncludesFrameworkOwnedEndpointProjection(t *testing.T) {
	resources := []Resource{
		{
			Address: "app/http_gateway/public",
			Module:  "app",
			Kind:    "scenery.http-gateway",
			Name:    "public",
			Spec:    map[string]any{},
			Origin:  Origin{Kind: "authored"},
		},
		{
			Address: "scenery_auth/record/google_connect_start_input",
			Module:  "scenery_auth",
			Kind:    "scenery.record",
			Name:    "google_connect_start_input",
			Spec: map[string]any{"field": []any{
				map[string]any{"name": "scopes", "type": map[string]any{"$expression": "list(string)"}},
			}},
			Origin: Origin{Kind: "framework"},
		},
		{
			Address: "scenery_auth/record/google_connect_start_response",
			Module:  "scenery_auth",
			Kind:    "scenery.record",
			Name:    "google_connect_start_response",
			Spec: map[string]any{"field": map[string]any{
				"name": "authorize_url",
				"type": map[string]any{"$ref": "string"},
			}},
			Origin: Origin{Kind: "framework"},
		},
		{
			Address: "scenery_auth/operation/google_connect_start",
			Module:  "scenery_auth",
			Kind:    "scenery.operation",
			Name:    "google_connect_start",
			Spec: map[string]any{
				"input":  map[string]any{"$ref": "scenery_auth/record/google_connect_start_input"},
				"result": map[string]any{"name": "success", "type": map[string]any{"$ref": "scenery_auth/record/google_connect_start_response"}},
			},
			Origin: Origin{Kind: "framework"},
		},
		{
			Address: "scenery_auth/binding/google_connect_start_public_http",
			Module:  "scenery_auth",
			Kind:    "scenery.binding",
			Name:    "google_connect_start_public_http",
			Spec: map[string]any{
				"gateway":   map[string]any{"$ref": "app/http_gateway/public"},
				"operation": map[string]any{"$ref": "scenery_auth/operation/google_connect_start"},
				"protocol":  "http",
				"http": map[string]any{
					"method": "POST",
					"path":   "/auth/google/connect/start",
					"body": map[string]any{
						"codec": "json",
						"to":    map[string]any{"$ref": "operation.google_connect_start.input"},
					},
					"response": map[string]any{
						"name":   "success",
						"when":   map[string]any{"$ref": "result.success"},
						"status": "200",
						"body":   map[string]any{"codec": "json"},
					},
				},
			},
			Origin: Origin{Kind: "framework"},
		},
	}
	target := Resource{
		Name: "public",
		Spec: map[string]any{
			"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}},
		},
	}
	bindings := publicHTTPBindings(resources, target)
	reachable := reachableResources(resources, bindings)
	clientSource := renderTSClient(target, bindings, reachable)
	typesSource := renderTSTypes(reachable, bindings)

	for _, fragment := range []string{
		"async googleConnectStart(input: Types.GoogleConnectStartInput",
		`"/auth/google/connect/start"`,
	} {
		if !strings.Contains(clientSource, fragment) {
			t.Fatalf("generated client missing %q:\n%s", fragment, clientSource)
		}
	}
	if !strings.Contains(typesSource, "export interface GoogleConnectStartResponse") {
		t.Fatalf("generated types omit the framework-owned response:\n%s", typesSource)
	}
}
