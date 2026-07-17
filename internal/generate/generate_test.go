package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/tscheck"
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

func TestGenerateContractsAndTypeScriptAreStable(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), temp)
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
	if len(tsResult.Changed) != 6 {
		t.Fatalf("TS changed = %#v", tsResult.Changed)
	}
	if _, err := GenerateTypeScriptClients(temp, "public_api", true); err != nil {
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
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	goArtifact := filepath.Join(root, "house", "scenerycontract", "scenery.package-generated.json")
	before, err := os.ReadFile(goArtifact)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "scenery.scn")
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
	if !strings.Contains(client, "response.status === 200") || strings.Contains(client, "response.status === 0") || strings.Contains(client, "mergeResponseValue(payload, null") {
		t.Fatalf("generated fixture client lost exact response status/path semantics:\n%s", client)
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
	if len(files) != 6 {
		t.Fatalf("generated paths = %#v", files)
	}
	plan, err := BuildRuntimeIntegrationPlan(result)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CompositionImport != "example.test/nativeapp/internal/scenerygen/composition" {
		t.Fatalf("runtime plan = %#v", plan)
	}
}

func TestPruneMaterializedGoVerifiesLegacyOwnership(t *testing.T) {
	root := t.TempDir()
	generated := filepath.Join(root, "service", "scenerycontract")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	contents := []byte("// Code generated by Scenery " + "vN" + "ext. DO NOT EDIT.\npackage scenerycontract\n")
	file := generatedFile{Path: filepath.Join(generated, "types.gen.go"), Bytes: contents}
	if err := os.WriteFile(file.Path, contents, 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor := map[string]any{
		"api_version": "legacy", "files": []string{"types.gen.go"},
		"content_digest": artifactDigest(generated, []generatedFile{file}),
	}
	data, _ := json.Marshal(descriptor)
	if err := os.WriteFile(filepath.Join(generated, legacyGoPackageDescriptorName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	checked, err := PruneMaterializedGo(root, true)
	if err == nil || len(checked.Changed) != 2 {
		t.Fatalf("check = %#v, %v", checked, err)
	}
	if _, err := PruneMaterializedGo(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("generated directory remains: %v", err)
	}
}

func TestPruneMaterializedGoRejectsModifiedArtifact(t *testing.T) {
	root := t.TempDir()
	generated := filepath.Join(root, "internal", "scenerygen")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("// Code generated by Scenery. DO NOT EDIT.\npackage scenerygen\n")
	file := generatedFile{Path: filepath.Join(generated, "adapter.gen.go"), Bytes: original}
	if err := os.WriteFile(file.Path, original, 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor := map[string]any{
		"api_version": "legacy", "files": []string{"adapter.gen.go"},
		"content_digest": artifactDigest(generated, []generatedFile{file}),
	}
	data, _ := json.Marshal(descriptor)
	if err := os.WriteFile(filepath.Join(generated, legacyGoApplicationDescriptorName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file.Path, append(original, []byte("// user change\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := PruneMaterializedGo(root, false); err == nil || !strings.Contains(err.Error(), "unverified") {
		t.Fatalf("prune error = %v", err)
	}
	if _, err := os.Stat(file.Path); err != nil {
		t.Fatalf("modified artifact was removed: %v", err)
	}
}

func TestPruneMaterializedGoAcceptsPriorCurrentDescriptorIdentity(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	if _, err := PruneMaterializedGo(root, false); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		filepath.Join(root, "house", "scenerycontract", "contract.gen.go"),
		filepath.Join(root, "house", "scenerycontract", "types.gen.go"),
		filepath.Join(root, "house", "scenerycontract", "scenery.package-generated.json"),
		filepath.Join(root, "internal", "scenerygen"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("generated artifact remains at %s: %v", path, err)
		}
	}
}

func TestTypeScriptCacheMaterializationDoesNotAffectCheck(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "house"), root)
	path := filepath.Join(root, "scenery.scn")
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
	if _, err := SyncCachedTypeScriptClients(result); err != nil {
		t.Fatal(err)
	}
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

func TestNativeImplementationVerificationUsesOverlayWithoutGeneratedTree(t *testing.T) {
	parallelIntegrationTest(t)

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
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
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if diagnostics := VerifyImplementation(result); len(diagnostics) != 0 {
		t.Fatalf("overlay verification = %#v", diagnostics)
	}
	if _, err := os.Stat(filepath.Join(root, "house", "scenerycontract")); !os.IsNotExist(err) {
		t.Fatal("compile materialized generated files")
	}
	if _, err := os.Stat(filepath.Join(root, "internal", "scenerygen")); !os.IsNotExist(err) {
		t.Fatal("compile materialized generated files")
	}
}

func TestGenerateBootstrapsContractArtifactsWhileImplementationIsInvalid(t *testing.T) {
	parallelIntegrationTest(t)

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
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
	result, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.ContractStatus != "valid" || len(VerifyImplementation(result)) == 0 {
		t.Fatalf("compile state = contract %s implementation diagnostics %#v", result.ContractStatus, VerifyImplementation(result))
	}
	generated, err := GenerateGoContracts(root, false)
	if err != nil {
		t.Fatalf("bootstrap generation failed: %v", err)
	}
	if len(generated.Changed) == 0 {
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
		`encodeHTTPValue(input.sceneId,`,
		`appendQuery(query, "tag", input.tags, "repeated",`,
		`appendHeader(headers, "if-match", input.etag, "repeated",`,
		`appendCookie(cookies, "tenant", input.tenant,`,
		`body: Runtime.encodeRequestBody(input.body`,
		`decodeResponseBody(completionResponse0, "problem_json", ["application/problem+json"]`,
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
	if !strings.Contains(client, "fetchWithRetry") || !strings.Contains(client, "maximumAttempts: 3") {
		t.Fatalf("retry client missing policy:\n%s", client)
	}
}

func TestGenerateApplicationArtifactsUsesExplicitRegistryAndComposition(t *testing.T) {
	root := t.TempDir()
	result := nativeApplicationGenerationFixture(root)
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
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

func TestGeneratedProviderCRUDAdapterCompilesInCleanClone(t *testing.T) {
	parallelIntegrationTest(t)

	root := t.TempDir()
	copyTree(t, filepath.Join("..", "compiler", "testdata", "native"), root)
	rewriteFixtureSceneryReplace(t, root)

	rootSourcePath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootSourcePath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http",`, `"scenery.runtime-http",
    "scenery.data",`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `gateway = http_gateway.public_api`, `gateway  = http_gateway.public_api
    database = data_source.house_database`, 1))
	rootSource = []byte(strings.Replace(string(rootSource), `  output_root = "clients/generated/public_api"
}`, `  output_root = "clients/generated/public_api"
  react {
    tsconfig = "tsconfig.json"
  }
}`, 1))
	rootSource = append(rootSource, []byte(`

provider "postgres" {
  source  = "registry.scenery.dev/core/postgres"
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
  field "name" { type = enum.scene_name }
}

enum "scene_name" {
  value "roof" { wire_value = "roof \"quoted\" \\ path" }
  value "wall" {}
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
  list {
    filters       = ["name"]
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

react_component "scene_name_cell" {
  module = "scene-name-cell.tsx"
  export = "SceneNameCell"
}

react_component "scene_name_filter" {
  module = "scene-name-filter.tsx"
  export = "SceneNameFilter"
}

table_page "scenes" {
  path        = "/scenes"
  source      = crud.scene_api
  title       = "Scenes \"quoted\" \\ path"
  description = "Description \"quoted\" \\ path"
  column "id" { label = "ID \"quoted\" \\ path" }
  column "name" {
    label     = "Name \"quoted\" \\ path"
    component = react_component.scene_name_cell
  }
  filter "name" {
    label     = "Filter \"quoted\" \\ path"
    component = react_component.scene_name_filter
  }
  sort "name" {
    label   = "Sort \"quoted\" \\ path"
    default = "asc"
  }
  row_link  = "/scenes/{id}"
  page_size = 20
}

table_page "plain_scenes" {
  path      = "/plain-scenes"
  source    = crud.scene_api
  title     = "Plain scenes"
  page_size = 20
  column "name" {}
  column "id" {}
  sort "name" { default = "asc" }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	for name, source := range map[string]string{
		"scene-name-cell.tsx":   `export function SceneNameCell(props: { readonly row: object; readonly value: string }) { return props.value; }`,
		"scene-name-filter.tsx": `export function SceneNameFilter(props: { readonly value?: string; readonly label: string; readonly onChange: (value: string | undefined) => void }) { return props.label; }`,
	} {
		if err := os.WriteFile(filepath.Join(root, "house", name), []byte(source), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"strict":true,"noUnusedLocals":true,"jsx":"react-jsx","module":"esnext","moduleResolution":"bundler","target":"es2022","lib":["es2022","dom"]},"include":["clients/generated/public_api/react/**/*.ts","clients/generated/public_api/react/**/*.tsx","house/**/*.tsx"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	integrity, ok := compiler.BuiltinProviderLock("registry.scenery.dev/core/postgres")
	if !ok {
		t.Fatal("builtin postgres provider unavailable")
	}
	lock := fmt.Sprintf("lock {}\nprovider \"postgres\" {\n  source = \"registry.scenery.dev/core/postgres\"\n  integrity = %q\n}\n", integrity)
	if err := os.WriteFile(filepath.Join(root, "scenery.lock.scn"), []byte(lock), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := scn.Format(root, false); err != nil {
		t.Fatal(err)
	}
	if _, err := GenerateGoContracts(root, false); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(root)
	if err != nil || !compiled.Valid() {
		t.Fatalf("compile generated CRUD list: %v diagnostics=%#v", err, compiled.Diagnostics)
	}
	var target Resource
	for _, resource := range compiled.Manifest.Resources {
		if resource.Address == "app/typescript_client/public_api" {
			target = resource
			break
		}
	}
	typeScriptFiles, err := renderTypeScriptTarget(compiled, target)
	if err != nil {
		t.Fatal(err)
	}
	if binary := os.Getenv("SCENERY_TSGO_BINARY"); binary != "" {
		repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(repoRoot, "apps", "console", "node_modules"), filepath.Join(root, "node_modules")); err != nil {
			t.Fatal(err)
		}
		staged := make([]tscheck.File, 0, len(typeScriptFiles))
		for _, file := range typeScriptFiles {
			if !file.Remove {
				staged = append(staged, tscheck.File{Path: file.Path, Bytes: file.Bytes})
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if err := tscheck.Check(ctx, binary, root, filepath.Join(root, "clients", "generated", "public_api"), "tsconfig.json", staged); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := finishGeneratedFiles(root, typeScriptFiles, false, ""); err != nil {
		t.Fatal(err)
	}
	var listBinding Resource
	for _, resource := range compiled.Manifest.Resources {
		if resource.Address == "house/binding/scene_api_list_http" {
			listBinding = resource
			break
		}
	}
	types := []byte(renderTSTypes(reachableResources(compiled.Manifest.Resources, []Resource{listBinding}), []Resource{listBinding}))
	for _, fragment := range []string{
		`export type SceneApiListSort = "name";`,
		`readonly name?: readonly SceneName[];`,
		`readonly direction?: SceneApiListDirection;`,
		`readonly nextCursor?: string;`,
	} {
		if !strings.Contains(string(types), fragment) {
			t.Errorf("generated CRUD list TypeScript missing %q:\n%s", fragment, types)
		}
	}
	clientSource, err := os.ReadFile(filepath.Join(root, "clients", "generated", "public_api", "client.ts"))
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"sceneApiCreate", "sceneApiGet", "sceneApiUpdate", "sceneApiDelete"} {
		if strings.Contains(string(clientSource), method) {
			t.Errorf("React table-page projection exported unrelated CRUD method %q", method)
		}
	}
	pageSource, err := os.ReadFile(filepath.Join(root, "clients", "generated", "public_api", "react", "scenes.generated.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"defineTablePageSlots<SceneRow",
		"client?: PublicApiClient",
		"providedClient ?? defaultClient",
		"client.sceneApiList",
		"value is SceneName",
		"Code generated by Scenery",
		`title={"Scenes \"quoted\" \\ path"}`,
		`description={"Description \"quoted\" \\ path"}`,
		`label: "ID \"quoted\" \\ path"`,
		`label: "Name \"quoted\" \\ path"`,
		`label: "Filter \"quoted\" \\ path"`,
		`label: "Sort \"quoted\" \\ path"`,
		`options: ["roof \"quoted\" \\ path", "wall"]`,
		`return { kind: "error", name: "unexpected", problem: { code: "unexpected", message: cause instanceof Error ? cause.message : "Unexpected error" } };`,
	} {
		if !strings.Contains(string(pageSource), fragment) {
			t.Errorf("generated table page missing %q:\n%s", fragment, pageSource)
		}
	}
	for _, forbidden := range []string{"as any", "as unknown as", "import(", "throw cause"} {
		if strings.Contains(string(pageSource), forbidden) {
			t.Errorf("generated table page contains forbidden %q:\n%s", forbidden, pageSource)
		}
	}
	plainPageSource, err := os.ReadFile(filepath.Join(root, "clients", "generated", "public_api", "react", "plain_scenes.generated.tsx"))
	if err != nil {
		t.Fatal(err)
	}
	for _, unusedImport := range []string{"dateTime", "TablePageCellProps", "TablePageFilterProps"} {
		if strings.Contains(string(plainPageSource), unusedImport) {
			t.Errorf("plain generated table page imports unused %q:\n%s", unusedImport, plainPageSource)
		}
	}
	plainSource := string(plainPageSource)
	if nameIndex, idIndex := strings.Index(plainSource, `{ field: "name"`), strings.Index(plainSource, `{ field: "id"`); nameIndex < 0 || idIndex < 0 || nameIndex > idIndex {
		t.Errorf("plain generated table page did not preserve authored column order:\n%s", plainSource)
	}
	if !strings.Contains(plainSource, `baseUrl: url(new URL("/api/", globalThis.location.origin).toString())`) {
		t.Errorf("plain generated table page does not target the browser API route:\n%s", plainSource)
	}
	descriptorBytes, err := os.ReadFile(filepath.Join(root, "clients", "generated", "public_api", "scenery.typescript-client-generated.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(descriptorBytes), `"react/scenery-ui"`) {
		t.Errorf("generated descriptor has no UI catalog root:\n%s", descriptorBytes)
	}
	command := boundedGoCommand("test", "./...")
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("clean-clone provider CRUD compile: %v\n%s", err, output)
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
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
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
