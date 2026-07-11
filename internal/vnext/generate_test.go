package vnext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/parse"
)

func TestGenerateContractsAndTypeScriptAreStable(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	_ = os.RemoveAll(filepath.Join(temp, "house", "scenerycontract"))
	_ = os.RemoveAll(filepath.Join(temp, "clients"))
	goResult, err := GenerateGoContracts(temp, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(goResult.Changed) != 3 {
		t.Fatalf("Go changed = %#v", goResult.Changed)
	}
	if _, err := GenerateGoContracts(temp, true); err != nil {
		t.Fatal(err)
	}
	tsResult, err := GenerateTypeScriptClients(temp, "public_api", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(tsResult.Changed) != 7 {
		t.Fatalf("TS changed = %#v", tsResult.Changed)
	}
	if _, err := GenerateTypeScriptClients(temp, "public_api", true); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"types.ts", "runtime.ts", "client.ts", "metadata.ts", "index.ts", "scenery.client-selection.v1.json", "scenery.typescript-client-generated.v1.json"} {
		if _, err := os.Stat(filepath.Join(temp, "clients", "generated", "public_api", path)); err != nil {
			t.Error(err)
		}
	}
}

func TestTypeScriptOutputRequiresDeclaredManagedGeneratedRoot(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	if err := os.RemoveAll(filepath.Join(root, "clients")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scenery.scn")
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
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	goArtifact := filepath.Join(root, "house", "scenerycontract", "scenery.package-generated.v1.json")
	before, err := os.ReadFile(goArtifact)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "scenery.scn")
	source, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	updated := bytes.Replace(source, []byte(`version = "2.0.0-dev"`), []byte(`version = "2.0.1-dev"`), 1)
	if bytes.Equal(updated, source) {
		t.Fatal("fixture does not declare the application version")
	}
	source = updated
	updated = bytes.Replace(source, []byte(`"clients/generated/public_api"`), []byte(`"managed"`), 1)
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
	result, err := Compile(filepath.Join("testdata", "native"))
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
	if !strings.Contains(client, "response.status === 200") || strings.Contains(client, "response.status === 0") || strings.Contains(client, "mergeResponseValue(payload, null") {
		t.Fatalf("generated fixture client lost exact response status/path semantics:\n%s", client)
	}
}

func TestNativeFixtureLegacyParserFindsImplementationService(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	app, err := parse.App(root, "nativeapp")
	if err != nil {
		t.Fatal(err)
	}
	for _, service := range app.Services {
		if service.Name == "house" && service.RootPackage != nil && service.Struct != nil {
			return
		}
	}
	t.Fatalf("packages=%d services=%#v", len(app.Packages), app.Services)
}

func TestNativeFixtureChecksCommittedContractAndApplicationArtifacts(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	generated, err := GenerateGoContracts(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(generated.Changed) != 0 || len(generated.Checked) != 6 {
		t.Fatalf("generated = %#v", generated)
	}
	plan, err := BuildRuntimeIntegrationPlan(result)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.NativeServices["house"] || plan.CompositionImport != "example.test/nativeapp/internal/scenerygen/composition" {
		t.Fatalf("runtime plan = %#v", plan)
	}
}

func TestNativeImplementationVerificationUsesOverlayWithoutGeneratedTree(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "../../../..", filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "house", "scenerycontract")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "internal", "scenerygen")); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractStatus != "valid" || result.ImplementationStatus != "valid" {
		t.Fatalf("overlay verification = %#v", result.Diagnostics)
	}
	if pathExists(filepath.Join(root, "house", "scenerycontract")) || pathExists(filepath.Join(root, "internal", "scenerygen")) {
		t.Fatal("compile materialized generated files")
	}
}

func TestGenerateBootstrapsContractArtifactsWhileImplementationIsInvalid(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	repositoryRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "../../../..", filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	servicePath := filepath.Join(root, "house", "service.go")
	service, err := os.ReadFile(servicePath)
	if err != nil {
		t.Fatal(err)
	}
	service = []byte(strings.Replace(string(service), "ProcessScene(_", "ProcessSceneBroken(_", 1))
	if err := os.WriteFile(servicePath, service, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(root, "house", "scenerycontract")); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractStatus != "valid" || result.ImplementationStatus != "invalid" {
		t.Fatalf("compile state = contract %s implementation %s diagnostics %#v", result.ContractStatus, result.ImplementationStatus, result.Diagnostics)
	}
	generated, err := GenerateGoContracts(root, false)
	if err != nil {
		t.Fatalf("bootstrap generation failed: %v", err)
	}
	if len(generated.Changed) == 0 {
		t.Fatal("bootstrap generation produced no contract artifacts")
	}
}

func TestCheckRejectsStaleGeneratedArtifactsWithoutWriting(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	generated := filepath.Join(temp, "house", "scenerycontract", "contract.gen.go")
	if err := os.WriteFile(generated, []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := Check(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() || !hasDiagnostic(result.Diagnostics, "SCN6203") {
		t.Fatalf("diagnostics = %#v, want SCN6203", result.Diagnostics)
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

func TestGeneratedDescriptorAcceptsLegacyDigestOnlyForSafeRegeneration(t *testing.T) {
	root := t.TempDir()
	contents := []byte("// Code generated by Scenery vNext. DO NOT EDIT.\npackage generated\n")
	if err := os.WriteFile(filepath.Join(root, "contract.gen.go"), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("contract.gen.go"))
	_, _ = hash.Write([]byte{0})
	_, _ = hash.Write(contents)
	descriptor := map[string]any{
		"api_version":    "scenery.package-generated.v1",
		"content_digest": "sha256:" + hex.EncodeToString(hash.Sum(nil)),
		"files":          []string{"contract.gen.go"},
	}
	encoded, err := json.Marshal(descriptor)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "scenery.package-generated.v1.json")
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		t.Fatal(err)
	}
	files, verified, err := verifiedGeneratedDescriptorFiles(path)
	if err != nil || !verified || !slices.Equal(files, []string{"contract.gen.go"}) {
		t.Fatalf("legacy descriptor verification = files %v, verified %t, error %v", files, verified, err)
	}
}

func TestGeneratedDescriptorOwnershipPrunesRetiredFiles(t *testing.T) {
	root := t.TempDir()
	generatedRoot := filepath.Join(root, "internal", "scenerygen")
	oldAdapter := filepath.Join(generatedRoot, "retired", "adapter.gen.go")
	descriptorPath := filepath.Join(generatedRoot, "scenery.generated.v1.json")
	if err := os.MkdirAll(filepath.Dir(oldAdapter), 0o755); err != nil {
		t.Fatal(err)
	}
	oldBytes := []byte("// Code generated by Scenery vNext. DO NOT EDIT.\npackage retired\n")
	if err := os.WriteFile(oldAdapter, oldBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor, _ := json.Marshal(map[string]any{
		"api_version": "scenery.generated.v1", "content_digest": artifactDigest(generatedRoot, []generatedFile{{Path: oldAdapter, Bytes: oldBytes}}), "files": []string{"retired/adapter.gen.go"},
	})
	if err := os.WriteFile(descriptorPath, descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	expected := []generatedFile{{Path: descriptorPath, Bytes: []byte(`{"files":[]}`)}}
	files, err := includeStaleGeneratedFiles(root, expected, map[string]bool{"scenery.generated.v1.json": true}, nil)
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
	descriptorPath := filepath.Join(generatedRoot, "scenery.generated.v1.json")
	descriptor, _ := json.Marshal(map[string]any{"api_version": "scenery.generated.v1", "content_digest": "sha256:" + strings.Repeat("0", 64), "files": []string{"../service.go"}})
	if err := os.WriteFile(descriptorPath, descriptor, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := includeStaleGeneratedFiles(root, nil, map[string]bool{"scenery.generated.v1.json": true}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsafe owned path") {
		t.Fatalf("error = %v, want unsafe owned path", err)
	}
	if _, err := os.Stat(handwritten); err != nil {
		t.Fatalf("handwritten file changed: %v", err)
	}
}

func TestGoGenerationCoversPreservingRecordsAndOpenUnions(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/item", Module: "house", Kind: "scenery.record/v1", Name: "item", Spec: map[string]any{
			"unknown_fields": "preserve",
			"field": []any{
				map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}},
				map[string]any{"name": "tags", "type": map[string]any{"$expression": "set(string)"}},
			},
		}},
		{Address: "house/union/state", Module: "house", Kind: "scenery.union/v1", Name: "state", Spec: map[string]any{
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
	source := renderContractTypes([]Resource{{Address: "house/record/tuple_holder", Module: "house", Kind: "scenery.record/v1", Name: "tuple_holder", Spec: map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$expression": "tuple(string, int64, list(uuid))"}}}}})
	for _, fragment := range []string{"type " + want + " struct", "Item0 string", "Item1 int64", "Item2 []scenery.UUID", "Value " + want} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("named tuple declaration missing %q:\n%s", fragment, source)
		}
	}
	if got := typeExpressionNames("tuple(record.zed, record.alpha)"); !semanticEqual(got, []string{"record.zed", "record.alpha"}) {
		t.Fatalf("tuple references reordered: %#v", got)
	}
}

func TestGoContractUsesQualifiedCrossModuleTypes(t *testing.T) {
	all := []Resource{
		{Address: "app/module/house", Module: "app", Kind: "scenery.module/v1", Name: "house", Spec: map[string]any{"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/house"}}}},
		{Address: "app/module/geometry", Module: "app", Kind: "scenery.module/v1", Name: "geometry", Spec: map[string]any{"package": map[string]any{"go_contract": map[string]any{"import_path": "example.test/geometry"}}}},
		{Address: "geometry/record/point", Module: "geometry", Kind: "scenery.record/v1", Name: "point", Spec: map[string]any{"field": map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}}}},
		{Address: "geometry/union/location", Module: "geometry", Kind: "scenery.union/v1", Name: "location", Spec: map[string]any{"variant": map[string]any{"name": "point", "type": map[string]any{"$ref": "record.point"}}}},
		{Address: "house/record/shape", Module: "house", Kind: "scenery.record/v1", Name: "shape", Spec: map[string]any{"field": []any{
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
		{Address: "house/record/item", Module: "house", Kind: "scenery.record/v1", Name: "item", Spec: map[string]any{"unknown_fields": "preserve", "field": map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}}}},
		{Address: "house/enum/mode", Module: "house", Kind: "scenery.enum/v1", Name: "mode", Spec: map[string]any{"open": true, "value": map[string]any{"name": "all", "wire_value": "all"}}},
		{Address: "house/union/state", Module: "house", Kind: "scenery.union/v1", Name: "state", Spec: map[string]any{"open": true, "variant": map[string]any{"name": "ready", "type": map[string]any{"$ref": "record.item"}}}},
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
	operation := Resource{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{
		"input":  map[string]any{"$ref": "record.input"},
		"result": map[string]any{"name": "processed", "type": map[string]any{"$ref": "record.output"}},
		"error":  map[string]any{"name": "invalid", "type": map[string]any{"$ref": "std.type.problem"}},
	}}
	binding := Resource{Address: "house/binding/process", Module: "house", Kind: "scenery.binding/v1", Name: "process", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.process"},
		"http": map[string]any{"method": "POST", "path": "/process", "body": map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation.process.input"}}, "response": []any{
			map[string]any{"name": "processed", "when": map[string]any{"$ref": "result.processed"}, "status": "200", "body": map[string]any{"codec": "json"}},
			map[string]any{"name": "invalid", "when": map[string]any{"$ref": "error.invalid"}, "status": "422", "body": map[string]any{"codec": "problem_json"}},
		}},
	}}
	runtimeSource := renderTSRuntime()
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
	for _, want := range []string{"encodeRequestBody(input", "decodeResponseBody", "response.status === 200", "response.status === 422"} {
		if !strings.Contains(clientSource, want) {
			t.Errorf("client missing %q in:\n%s", want, clientSource)
		}
	}
	if strings.Contains(clientSource, "response.json()") {
		t.Fatalf("client must not round exact JSON numbers through response.json():\n%s", clientSource)
	}
}

func TestTypeScriptClientUsesDeclaredPathQueryAndBodyMappings(t *testing.T) {
	operation := Resource{Address: "house/operation/update", Module: "house", Kind: "scenery.operation/v1", Name: "update", Spec: map[string]any{
		"input":  map[string]any{"$ref": "record.update_input"},
		"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "json"}},
		"error":  map[string]any{"name": "invalid_input", "type": map[string]any{"$ref": "std.type.problem"}},
	}}
	binding := Resource{Address: "house/binding/update", Module: "house", Kind: "scenery.binding/v1", Name: "update", Spec: map[string]any{
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
		`encodeHTTPValue(input.sceneId,`,
		`appendQuery(query, "tag", input.tags, "repeated",`,
		`appendHeader(headers, "if-match", input.etag, "repeated",`,
		`appendCookie(cookies, "tenant", input.tenant,`,
		`body: encodeRequestBody(input.body`,
		`decodeResponseBody(completionResponse0, "problem_json", ["application/problem+json"]`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("client missing %q:\n%s", fragment, source)
		}
	}
}

func TestTypeScriptReachabilityExcludesUnrelatedTypes(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/input", Module: "house", Kind: "scenery.record/v1", Name: "input", Spec: map[string]any{"field": map[string]any{"name": "item", "type": map[string]any{"$ref": "record.item"}}}},
		{Address: "house/record/item", Module: "house", Kind: "scenery.record/v1", Name: "item", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}},
		{Address: "house/record/unrelated", Module: "house", Kind: "scenery.record/v1", Name: "unrelated", Spec: map[string]any{}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.input"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.item"}}}},
	}
	bindings := []Resource{{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.get"}}}}
	reachable := reachableResources(resources, bindings)
	addresses := resourceAddresses(reachable)
	want := []string{"house/operation/get", "house/record/input", "house/record/item"}
	if !semanticEqual(addresses, want) {
		t.Fatalf("reachable = %#v, want %#v", addresses, want)
	}
}

func TestTypeScriptReachabilityIncludesCanonicalCrossModuleTypes(t *testing.T) {
	resources := []Resource{
		{Address: "geometry/record/point", Module: "geometry", Kind: "scenery.record/v1", Name: "point", Spec: map[string]any{"field": map[string]any{"name": "x", "type": map[string]any{"$ref": "float64"}}}},
		{Address: "house/record/shape", Module: "house", Kind: "scenery.record/v1", Name: "shape", Spec: map[string]any{"field": map[string]any{"name": "point", "type": map[string]any{"$ref": "geometry/record/point"}}}},
		{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "house/record/shape"}}},
	}
	bindings := []Resource{{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "house/operation/get"}}}}
	reachable := reachableResources(resources, bindings)
	want := []string{"geometry/record/point", "house/operation/get", "house/record/shape"}
	if got := resourceAddresses(reachable); !semanticEqual(got, want) {
		t.Fatalf("reachable = %#v, want %#v", got, want)
	}
	if generated := renderTSTypes(reachable); !strings.Contains(generated, "readonly point: Point") {
		t.Fatalf("cross-module field lost its type:\n%s", generated)
	}
	if registry := renderTSRegistry(reachable); !strings.Contains(registry, `"name":"geometry/record/point"`) {
		t.Fatalf("cross-module field lost its runtime descriptor:\n%s", registry)
	}
}

func TestTypeScriptNameValidationReservesRuntimeAndConstructorNames(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client/v1", Name: "public"}
	resources := []Resource{
		{Address: "house/record/json_value", Module: "house", Kind: "scenery.record/v1", Name: "json_value", Spec: map[string]any{}},
		{Address: "house/operation/constructor", Module: "house", Kind: "scenery.operation/v1", Name: "constructor", Spec: map[string]any{"input": map[string]any{"$ref": "house/record/json_value"}}},
	}
	bindings := []Resource{{Address: "house/binding/constructor", Module: "house", Kind: "scenery.binding/v1", Name: "constructor", Spec: map[string]any{"operation": map[string]any{"$ref": "house/operation/constructor"}}}}
	diagnostics := validateTypeScriptNames(target, resources, bindings)
	for _, code := range []string{"SCN6310", "SCN6312"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Fatalf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestTypeScriptNameValidationReservesPreservedUnknownField(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client/v1", Name: "public"}
	record := Resource{Address: "house/record/item", Module: "house", Kind: "scenery.record/v1", Name: "item", Spec: map[string]any{
		"unknown_fields": "preserve", "field": map[string]any{"name": "unknown_fields", "type": map[string]any{"$ref": "json"}},
	}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Spec: map[string]any{"input": map[string]any{"$ref": "record.item"}}}
	binding := Resource{Address: "house/binding/get", Module: "house", Kind: "scenery.binding/v1", Name: "get", Spec: map[string]any{"operation": map[string]any{"$ref": "operation.get"}}}
	if diagnostics := validateTypeScriptNames(target, []Resource{record, operation}, []Resource{binding}); !hasDiagnostic(diagnostics, "SCN6311") {
		t.Fatalf("missing preserved unknown-field collision: %#v", diagnostics)
	}
}

func TestTypeScriptExportSelectionAcceptsCanonicalModuleAddresses(t *testing.T) {
	module := Resource{Address: "app/module/house", Module: "app", Kind: "scenery.module/v1", Name: "house", Spec: map[string]any{
		"exports": map[string]any{"operations": map[string]any{"get": map[string]any{"$ref": "house/operation/get"}}},
	}}
	operation := Resource{Address: "house/operation/get", Module: "house", Kind: "scenery.operation/v1", Name: "get", Origin: Origin{Kind: "authored"}, Spec: map[string]any{"input": map[string]any{"$ref": "string"}}}
	binding := Resource{Address: "house/binding/get_http", Module: "house", Kind: "scenery.binding/v1", Name: "get_http", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "app/http_gateway/public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "GET", "path": "/get", "guarantee": "framework_enforced"},
	}}
	target := Resource{Address: "app/typescript_client/public", Module: "app", Kind: "scenery.typescript-client/v1", Name: "public", Spec: map[string]any{"gateways": []any{map[string]any{"$ref": "app/http_gateway/public"}}}}
	selected := publicHTTPBindings([]Resource{module, operation, binding}, target)
	if len(selected) != 1 || selected[0].Address != binding.Address {
		t.Fatalf("selected bindings = %#v, want %s", resourceAddresses(selected), binding.Address)
	}
}

func TestTypeScriptRetryRequiresIdempotentReplayableOperation(t *testing.T) {
	target := Resource{Address: "app/typescript_client/public", Kind: "scenery.typescript-client/v1", Name: "public", Module: "app", Spec: map[string]any{
		"gateways": []any{map[string]any{"$ref": "http_gateway.public"}}, "package": "@test/client", "module": "esm", "runtime": "fetch", "output_root": "generated/client",
		"retry": map[string]any{"policy": "scenery.retry.idempotent/v1", "maximum_attempts": "3"},
	}}
	input := Resource{Address: "house/record/get_input", Kind: "scenery.record/v1", Name: "get_input", Module: "house", Spec: map[string]any{"field": map[string]any{"name": "id", "type": map[string]any{"$ref": "string"}}}}
	operation := Resource{Address: "house/operation/get", Kind: "scenery.operation/v1", Name: "get", Module: "house", Spec: map[string]any{"input": map[string]any{"$ref": "record.get_input"}}}
	binding := Resource{Address: "house/binding/get", Kind: "scenery.binding/v1", Name: "get", Module: "house", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "operation": map[string]any{"$ref": "operation.get"}, "protocol": "http", "http": map[string]any{"method": "POST", "path": "/get", "body": map[string]any{"codec": "json"}},
	}}
	resources := []Resource{target, input, operation, binding}
	diagnostics := validateTypeScriptTarget(target, resources)
	if !diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	operation.Spec["idempotency"] = map[string]any{"mode": "keyed"}
	resources[2] = operation
	if diagnostics := validateTypeScriptTarget(target, resources); !diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("keyed operation without a key was accepted: %#v", diagnostics)
	}
	operation.Spec["idempotency"] = map[string]any{"mode": "keyed", "key": []any{map[string]any{"$expression": "input.id"}}}
	resources[2] = operation
	if diagnostics := validateTypeScriptTarget(target, resources); diagnosticsContain(diagnostics, "SCN6309") {
		t.Fatalf("idempotent operation was rejected: %#v", diagnostics)
	}
	client := renderTSClient(target, []Resource{binding}, resources)
	if !strings.Contains(client, "fetchWithRetry") || !strings.Contains(client, "maximumAttempts: 3") {
		t.Fatalf("retry client missing policy:\n%s", client)
	}
}

func diagnosticsContain(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func TestGenerateApplicationArtifactsUsesExplicitRegistryAndComposition(t *testing.T) {
	root := t.TempDir()
	result := nativeApplicationGenerationFixture(root)
	files, err := generateApplicationArtifacts(result)
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
	if err := json.Unmarshal(byPath["internal/scenerygen/scenery.generated.v1.json"], &descriptor); err != nil {
		t.Fatal(err)
	}
	if descriptor["api_version"] != "scenery.generated.v1" || descriptor["content_digest"] == "" {
		t.Fatalf("descriptor = %#v", descriptor)
	}
}

func TestGeneratedGoOperationOutcomeHasDeterministicDurableCodec(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	module := resourceByKind(result.Manifest.Resources, "scenery.module/v1")
	files, err := generateModuleContract(result, module)
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

func TestGenerateApplicationArtifactsRegistersProviderCRUDRuntime(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	dataResources := dataProfileFixtureResources()
	for index := range dataResources {
		switch dataResources[index].Kind {
		case "scenery.provider/v1":
			dataResources[index].Spec["runtime_abi"] = "scenery.data-runtime/v1"
			dataResources[index].Spec["locked_version"] = "2.1.0"
		case "scenery.data-source/v1":
			dataResources[index].Spec["config"] = map[string]any{"database": "house"}
		}
	}
	expanded, diagnostics := expandDataResources(dataResources)
	if hasErrors(diagnostics) {
		t.Fatalf("expand CRUD: %#v", diagnostics)
	}
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Address: "app/http_gateway/public", Module: "app", Name: "public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"base_path": "/"}, Origin: Origin{Kind: "authored"}},
	)
	result.Manifest.Resources = append(result.Manifest.Resources, expanded...)
	files, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(files, "/house_scene_api_data_adapter/adapter.gen.go")
	for _, fragment := range []string{
		"type providerCRUDService struct",
		`scenerydb.Get(ctx, "house")`,
		"datasource.InvokeCRUD",
		`TenantKey: true`,
		`RegisterEndpointChecked`,
		`principal.tenant_id`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("provider CRUD adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGeneratedProviderCRUDAdapterCompilesInCleanClone(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)

	rootSourcePath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http/v1",`, `"scenery.runtime-http/v1",
    "scenery.data/v1",`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `gateway = http_gateway.public_api`, `gateway  = http_gateway.public_api
    database = data_source.house_database`, 1))
	rootSource = append(rootSource, []byte(`

provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
  version = ">= 2.1.0, < 3.0.0"
}

data_source "house_database" {
  provider  = provider.postgres
  lifecycle = "external"
  require_capabilities = ["sql.query/v1", "sql.transaction/v1"]
  config = { database = "house" }
}
`)...)
	if err := os.WriteFile(rootSourcePath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}

	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`

input "database" { type = resource_ref("data_source") }

record "scene_row" {
  field "id" { type = uuid }
  field "tenant_id" { type = string }
  field "name" { type = string }
}

entity "scene" {
  type        = record.scene_row
  data_source = var.database
  mapping {
    relation = "scenes"
  }
  field "id" {
    column      = "id"
    primary_key = true
    default {
      strategy = "uuid_v7"
    }
  }
  field "tenant_id" {
    column    = "tenant_id"
    tenant_key = true
    immutable = true
  }
  field "name" { column = "name" }
}

crud "scene_api" {
  entity         = entity.scene
  implementation = std.crud.entity
  actions        = ["list", "get", "create", "update", "delete"]
  execution {
    mode    = "direct"
    timeout = "15s"
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
		t.Fatal(err)
	}
	version, integrity, ok := BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres provider unavailable")
	}
	lock := fmt.Sprintf("lock { schema = \"scenery.lock/v1\" }\nprovider \"postgres\" {\n  source = \"registry.scenery.dev/core/postgres\"\n  version = %q\n  integrity = %q\n}\n", version, integrity)
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "./...")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clean-clone provider CRUD compile: %v\n%s", err, output)
	}
}

func TestGenerateApplicationAdapterEmitsTypedPathMappingAndGatewayBasePath(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway/v1", Name: "public", Spec: map[string]any{"base_path": "/api"}, Origin: Origin{Kind: "authored"}})
	for index := range result.Manifest.Resources {
		resource := &result.Manifest.Resources[index]
		switch resource.Kind {
		case "scenery.binding/v1":
			resource.Spec["gateway"] = map[string]any{"$ref": "app/http_gateway/public"}
			httpSpec := resource.Spec["http"].(map[string]any)
			httpSpec["method"] = "GET"
			httpSpec["path"] = "/house/process/{scene_id}"
			delete(httpSpec, "body")
			httpSpec["path_parameter"] = map[string]any{"name": "scene_id", "to": map[string]any{"$ref": "operation.process_scene.input.scene_id"}}
		}
	}
	files, err := generateApplicationArtifacts(result)
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

func TestRenderContractPolicyIncludesAuthorizationAndOrderedPipeline(t *testing.T) {
	resources := map[string]Resource{
		"app/http_gateway/public": {Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"cors": map[string]any{"$ref": "std.cors.none"}, "forwarded": map[string]any{"$ref": "std.forwarded_headers.reject"}, "trusted_proxies": map[string]any{"$ref": "std.trusted_proxies.none"}}},
		"app/authorization/member": {Address: "app/authorization/member", Kind: "scenery.authorization/v1", Spec: map[string]any{"strategy": "deny_unless_allowed", "rule": []any{
			map[string]any{"name": "signed_in", "allow": map[string]any{"$expression": `principal.uid != ""`}},
			map[string]any{"name": "enabled", "allow": true},
		}}},
		"app/pipeline/default": {Address: "app/pipeline/default", Kind: "scenery.pipeline/v1", Spec: map[string]any{"step": []any{
			map[string]any{"name": "trace", "use": map[string]any{"$ref": "std.middleware.trace"}},
			map[string]any{"name": "request", "use": map[string]any{"$ref": "std.middleware.request_id"}},
		}}},
	}
	binding := Resource{Address: "house/binding/process", Module: "house", Spec: map[string]any{
		"gateway": map[string]any{"$ref": "http_gateway.public"}, "authorization": map[string]any{"$ref": "authorization.member"}, "pipeline": map[string]any{"$ref": "pipeline.default"},
	}}
	policy := renderContractHTTPPolicy(resources, binding, map[string]any{})
	for _, fragment := range []string{`BindingAddress: "house/binding/process"`, `Expression: "principal.uid != \"\""`, `Expression: "true"`, `PipelineSteps: []string{"std.middleware.trace", "std.middleware.request_id"}`} {
		if !strings.Contains(policy, fragment) {
			t.Fatalf("policy missing %q:\n%s", fragment, policy)
		}
	}
}

func TestGenerateGoConstructorInjectsStableSQLCapability(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	dataSource := Resource{
		Address: "app/data_source/house_database", Module: "app", Kind: "scenery.data-source/v1", Name: "house_database", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"require_capabilities": []any{"sql.query/v1", "sql.transaction/v1"}, "config": map[string]any{"database": "house"}},
	}
	result.Manifest.Resources = append(result.Manifest.Resources, dataSource)
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind == "scenery.service/v1" {
			result.Manifest.Resources[index].Spec["dependency"] = map[string]any{"name": "database", "instance": map[string]any{"$ref": "data_source.house_database"}}
		}
	}
	module := result.Manifest.Resources[1]
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.module/v1" {
			module = resource
		}
	}
	contractFiles, err := generateModuleContract(result, module)
	if err != nil {
		t.Fatal(err)
	}
	var contractSource string
	for _, file := range contractFiles {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/contract.gen.go") {
			contractSource = string(file.Bytes)
		}
	}
	for _, fragment := range []string{`datasource "scenery.sh/datasource"`, `Database datasource.SQL`} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	var adapter string
	for _, file := range applicationFiles {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/house_house_adapter/adapter.gen.go") {
			adapter = string(file.Bytes)
		}
	}
	for _, fragment := range []string{`scenerydb "scenery.sh/db"`, `scenerydb.Get(ctx, "house")`, `input.Dependencies.Database = dependency0`, `implementation.NewService(ctx, input)`} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}

func TestGenerateGoConstructorInjectsTypedConfigAndSecretReferences(t *testing.T) {
	result := nativeApplicationGenerationFixture(t.TempDir())
	result.Manifest.Resources = append(result.Manifest.Resources, Resource{
		Address: "app/secret/provider_token", Module: "app", Kind: "scenery.secret/v1", Name: "provider_token", Origin: Origin{Kind: "authored"},
		Spec: map[string]any{"store": map[string]any{"$ref": "secret_store.production"}, "key": "provider/token"},
	})
	for index := range result.Manifest.Resources {
		if result.Manifest.Resources[index].Kind != "scenery.service/v1" {
			continue
		}
		result.Manifest.Resources[index].Spec["config"] = map[string]any{
			"model_path": map[string]any{"$scalar": "relative_path", "value": "models/roof"},
			"workers":    "4",
			"token":      map[string]any{"$ref": "secret.provider_token"},
		}
		result.Manifest.Resources[index].Spec["config_schema"] = []any{
			map[string]any{"name": "model_path", "type": "relative_path"},
			map[string]any{"name": "token", "type": `resource_ref("secret")`, "sensitive": true},
			map[string]any{"name": "workers", "type": "uint32"},
		}
	}
	module := resourceByKind(result.Manifest.Resources, "scenery.module/v1")
	contractFiles, err := generateModuleContract(result, module)
	if err != nil {
		t.Fatal(err)
	}
	contractSource := generatedSourceWithSuffix(contractFiles, "/contract.gen.go")
	for _, fragment := range []string{"ModelPath scenery.RelativePath", "Token     scenery.SecretRef", "Workers   uint32"} {
		if !strings.Contains(contractSource, fragment) {
			t.Fatalf("contract missing %q:\n%s", fragment, contractSource)
		}
	}
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	adapter := generatedSourceWithSuffix(applicationFiles, "/house_house_adapter/adapter.gen.go")
	for _, fragment := range []string{
		`UnmarshalContractValue([]byte("\"models/roof\"")`,
		`&input.Config.ModelPath, "relative_path"`,
		`input.Config.Token = scenery.SecretRef{Address: "app/secret/provider_token"}`,
		`&input.Config.Workers, "uint32"`,
	} {
		if !strings.Contains(adapter, fragment) {
			t.Fatalf("adapter missing %q:\n%s", fragment, adapter)
		}
	}
}
