package compiler

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileHouseCore(t *testing.T) {
	result, err := Compile(filepath.Join("testdata", "house"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("invalid: %#v", result.Diagnostics)
	}
	if result.Manifest.Application.Name != "clean_tech" {
		t.Fatalf("application = %q", result.Manifest.Application.Name)
	}
	if !strings.HasPrefix(result.Manifest.ContractRevision, "sha256:") {
		t.Fatalf("revision = %q", result.Manifest.ContractRevision)
	}
	for name, manifest := range map[string]*Manifest{"expanded": result.Manifest, "source": result.ViewManifests["source"], "effective": result.ViewManifests["effective"]} {
		if manifest.Diagnostics == nil {
			t.Fatalf("%s manifest diagnostics is nil", name)
		}
		encoded, marshalErr := json.Marshal(manifest)
		if marshalErr != nil || strings.Contains(string(encoded), `"diagnostics":null`) {
			t.Fatalf("%s manifest diagnostics encoding = %s, error = %v", name, encoded, marshalErr)
		}
	}
	addresses := map[string]bool{}
	for _, resource := range result.Manifest.Resources {
		addresses[resource.Address] = true
	}
	for _, want := range []string{"app/http_gateway/public_api", "app/module/house", "house/service/house", "house/operation/process_scene", "house/binding/process_scene_http"} {
		if !addresses[want] {
			t.Errorf("missing %s", want)
		}
	}
	byAddress := resourcesByAddress(result.Manifest)
	module := byAddress["app/module/house"]
	inputs, _ := module.Spec["inputs"].(map[string]any)
	if refString(inputs["gateway"]) != "http_gateway.public_api" {
		t.Fatalf("module inputs were not normalized: %#v", module.Spec["inputs"])
	}
	binding := byAddress["house/binding/process_scene_http"]
	if refString(binding.Spec["gateway"]) != "http_gateway.public_api" {
		t.Fatalf("package var.gateway was not resolved through the module instance: %#v", binding.Spec["gateway"])
	}
	client := byAddress["app/typescript_client/public_api"]
	gateways, _ := client.Spec["gateways"].([]any)
	if len(gateways) != 1 || refString(gateways[0]) != "http_gateway.public_api" {
		t.Fatalf("client gateways were not normalized: %#v", client.Spec["gateways"])
	}
}

func TestCompileRejectsSymlinkedSourceFiles(t *testing.T) {
	root := t.TempDir()
	external := filepath.Join(t.TempDir(), "scenery.scn")
	if err := os.WriteFile(external, []byte("application \"external\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(external, filepath.Join(root, "scenery.scn")); err != nil {
		t.Fatal(err)
	}
	if _, err := Compile(root); err == nil || !strings.Contains(err.Error(), "regular non-symlink") {
		t.Fatalf("Compile() error = %v, want symlink rejection", err)
	}
}

func TestManifestSourceMapCarriesPortableDeclarationAttributeAndModuleRanges(t *testing.T) {
	result, err := Compile(filepath.Join("testdata", "house"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	operation := resourcesByAddress(result.Manifest)["house/operation/process_scene"]
	if operation.Origin.DeclarationRange == nil || operation.Origin.DeclarationRange.SourceID == "" || operation.Origin.AttributeRanges["/spec/input"].SourceID == "" {
		t.Fatalf("operation origin = %#v", operation.Origin)
	}
	if len(operation.Origin.ModuleChain) != 1 || operation.Origin.ModuleChain[0] != "app/module/house" {
		t.Fatalf("module chain = %#v", operation.Origin.ModuleChain)
	}
	for _, source := range result.Manifest.SourceMap {
		if filepath.IsAbs(source.URI) || strings.Contains(source.URI, "testdata/house") {
			t.Fatalf("source map leaked non-portable path: %#v", source)
		}
	}
}

func TestManifestSourceMapKeepsPunctuationDistinctSourceURIs(t *testing.T) {
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), root)
	for _, name := range []string{"a-b.scn", "a_b.scn"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("# source identity fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := Compile(root)
	if err != nil || !result.Valid() {
		t.Fatalf("Compile() = %#v, %v", result, err)
	}
	ids := map[string]string{}
	for id, record := range result.Manifest.SourceMap {
		if previous := ids[id]; previous != "" {
			t.Fatalf("source ID %q maps to both %q and %q", id, previous, record.URI)
		}
		ids[id] = record.URI
	}
	for _, uri := range []string{"a-b.scn", "a_b.scn"} {
		if ids[sourceID(uri)] != uri {
			t.Fatalf("source map[%q] = %q, want %q", sourceID(uri), ids[sourceID(uri)], uri)
		}
	}
}

func TestContractRevisionIgnoresFormatting(t *testing.T) {
	source := filepath.Join("testdata", "house")
	result, err := Compile(source)
	if err != nil {
		t.Fatal(err)
	}
	temp := t.TempDir()
	copyTree(t, source, temp)
	path := filepath.Join(temp, "scenery.scn")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append([]byte("# formatting changes workspace bytes\n\n"), b...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	changed, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Manifest.ContractRevision != changed.Manifest.ContractRevision {
		t.Fatalf("contract revision changed: %s != %s", result.Manifest.ContractRevision, changed.Manifest.ContractRevision)
	}
	if result.WorkspaceRevision == changed.WorkspaceRevision {
		t.Fatal("workspace revision did not change")
	}
}

func TestContractRevisionUsesOnlyContractDomains(t *testing.T) {
	resources := []Resource{
		{Address: "house/service/house", Module: "house", Name: "house", Kind: "scenery.service", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewOne"}}},
		{Address: "house/operation/get", Module: "house", Name: "get", Kind: "scenery.operation", Spec: map[string]any{"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "string"}, "handler": map[string]any{"method": "GetOne"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}}}},
		{Address: "house/binding/get", Module: "house", Name: "get", Kind: "scenery.binding", Spec: map[string]any{"protocol": "http", "http": map[string]any{"method": "GET", "path": "/one"}}},
	}
	base, err := contractRevision(resources, "app")
	if err != nil {
		t.Fatal(err)
	}
	changedImplementation := append([]Resource(nil), resources...)
	changedImplementation[0].Spec = map[string]any{"runtime": "go", "implementation": map[string]any{"constructor": "NewTwo"}}
	changedImplementation[1].Spec = map[string]any{"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "string"}, "handler": map[string]any{"method": "GetTwo"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}}}
	implementationRevision, _ := contractRevision(changedImplementation, "app")
	if implementationRevision != base {
		t.Fatal("implementation-only symbols changed contract revision")
	}
	withDeployment := append(append([]Resource(nil), resources...), Resource{Address: "app/deployment/prod", Module: "app", Name: "prod", Kind: "scenery.deployment", Spec: map[string]any{"replicas": "4"}})
	deploymentRevision, _ := contractRevision(withDeployment, "app")
	if deploymentRevision != base {
		t.Fatal("deployment-only resource changed contract revision")
	}
	changedContract := append([]Resource(nil), resources...)
	changedContract[2].Spec = map[string]any{"protocol": "http", "http": map[string]any{"method": "GET", "path": "/two"}}
	contractChange, _ := contractRevision(changedContract, "app")
	if contractChange == base {
		t.Fatal("HTTP path did not change contract revision")
	}
}

func TestImplementationRevisionRequiresBuildSuppliedInputManifest(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "scenery.scn")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	b = append(b, []byte(`
workspace {
  implementation_root "application" {
    path = "."
    revision_include = ["**/*.go"]
    revision_exclude = ["**/scenerycontract/**"]
  }
}

go_module "application" {
  root = "."
  import_path = "example.test/clean-tech"
}
go_toolchain "application" { version = "1.26.3" }
go_target "development" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.application
  module = go_module.application
  packages = ["./..."]
}
`)...)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	goPath := filepath.Join(temp, "house", "service.go")
	if err := os.WriteFile(goPath, []byte("package house\nfunc body() int { return 1 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, []byte("package house\nfunc body() int { return 2 }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if before.WorkspaceRevision == after.WorkspaceRevision {
		t.Fatal("Go body did not change workspace revision")
	}
	if before.Manifest.ContractRevision != after.Manifest.ContractRevision {
		t.Fatal("Go body changed contract revision")
	}
	if len(before.ImplementationRevisions) != 0 || len(after.ImplementationRevisions) != 0 {
		t.Fatal("compiler invented implementation revision without a build input manifest")
	}
	first, diagnostics := ComputeImplementationRevisions(before, map[string]string{"development": "sha256:" + strings.Repeat("1", 64)})
	if hasErrors(diagnostics) || first["development"] == "" {
		t.Fatalf("first build revision = %#v diagnostics %#v", first, diagnostics)
	}
	second, diagnostics := ComputeImplementationRevisions(after, map[string]string{"development": "sha256:" + strings.Repeat("2", 64)})
	if hasErrors(diagnostics) || second["development"] == "" || second["development"] == first["development"] {
		t.Fatalf("second build revision = %#v diagnostics %#v", second, diagnostics)
	}
	sourceBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append([]byte("# presentation only\n"), sourceBytes...), 0o644); err != nil {
		t.Fatal(err)
	}
	presentation, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if presentation.WorkspaceRevision == after.WorkspaceRevision {
		t.Fatal("source comment did not change workspace revision")
	}
	if presentation.Manifest.ContractRevision != after.Manifest.ContractRevision || len(presentation.ImplementationRevisions) != 0 {
		t.Fatal("source comment changed contract revision or invented implementation revision")
	}
}

func TestWorkspaceRevisionIncludesOnlyDescriptorOwnedGeneratedFiles(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	rootSource := filepath.Join(temp, "scenery.scn")
	data, err := os.ReadFile(rootSource)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\nworkspace { managed_generated_roots = [\"generated\"] }\n")...)
	if err := os.WriteFile(rootSource, data, 0o644); err != nil {
		t.Fatal(err)
	}
	generated := filepath.Join(temp, "generated")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatal(err)
	}
	owned := filepath.Join(generated, "types.ts")
	unknown := filepath.Join(generated, "notes.txt")
	if err := os.WriteFile(owned, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	descriptor := filepath.Join(generated, "scenery.typescript-client-generated.json")
	if err := os.WriteFile(descriptor, []byte(`{"files":["types.ts"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	base, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unknown, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unknownChanged, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if unknownChanged.WorkspaceRevision != base.WorkspaceRevision {
		t.Fatal("unknown generated file changed workspace revision")
	}
	if err := os.WriteFile(owned, []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ownedChanged, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if ownedChanged.WorkspaceRevision == base.WorkspaceRevision {
		t.Fatal("descriptor-owned generated file did not change workspace revision")
	}
}

func TestDuplicateParameterizedRoutesConflict(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "house", "duplicate.scn")
	duplicate := `binding "duplicate" {
  gateway = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol = "http"
  delivery = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method = "POST"
    path = "/house/process"
    codec_profile = std.codec.http_json_v1
  }
}`
	if err := os.WriteFile(path, []byte(duplicate), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if result.Valid() || !hasDiagnostic(result.Diagnostics, "SCN2002") {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
	if result.Manifest != nil || result.PartialGraph == nil || result.PartialGraph.Deployable || result.ContractStatus != "invalid" || result.WorkspaceRevision == "" {
		t.Fatalf("invalid contract leaked a manifest or lost recovery state: %+v", result)
	}
}

func TestFormatPreservesCommentsAndIsIdempotent(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "scenery.scn")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	before = append([]byte("# retained comment\n"), before...)
	if err := os.WriteFile(path, before, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(temp, false); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "# retained comment") {
		t.Fatal("comment was lost")
	}
	result, err := Format(temp, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) != 0 {
		t.Fatalf("second format changed %#v", result.Changed)
	}
}

func TestFormatCanonicalizesDurationBeyondMachineIntegerRange(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	source := `execution "huge" {
  timeout = "9223372036854775808ns"
}
`
	if err := os.WriteFile(path, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(root, false); err != nil {
		t.Fatal(err)
	}
	formatted, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(formatted), `timeout = "15250w1d23h47m16s854ms775us808ns"`) {
		t.Fatalf("formatted source = %s", formatted)
	}
	if result, err := Format(root, true); err != nil || len(result.Changed) != 0 {
		t.Fatalf("idempotent format = %#v, %v", result, err)
	}
}

func TestFormatRejectsEscapingModuleSources(t *testing.T) {
	for _, test := range []struct {
		name   string
		source string
		link   bool
	}{
		{name: "traversal", source: "../outside"},
		{name: "symlink", source: "./linked", link: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			root, outside := filepath.Join(parent, "app"), filepath.Join(parent, "outside")
			if err := os.MkdirAll(root, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.MkdirAll(outside, 0o755); err != nil {
				t.Fatal(err)
			}
			outsidePath := filepath.Join(outside, "scenery.package.scn")
			before := []byte("package\"outside\"{}\n")
			if err := os.WriteFile(outsidePath, before, 0o644); err != nil {
				t.Fatal(err)
			}
			if test.link {
				if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
					t.Skip(err)
				}
			}
			source := fmt.Sprintf("module \"x\" { source = %q }\n", test.source)
			if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte(source), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Format(root, false); err == nil {
				t.Fatal("formatter accepted an escaping module source")
			}
			after, err := os.ReadFile(outsidePath)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after, before) {
				t.Fatal("formatter changed source outside the workspace")
			}
		})
	}
}

func TestCompilerRetainsDistinctSourceEffectiveAndExpandedViews(t *testing.T) {
	result, err := Compile(filepath.Join("testdata", "house"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	source, err := result.ManifestForView("source")
	if err != nil {
		t.Fatal(err)
	}
	effective, err := result.ManifestForView("effective")
	if err != nil {
		t.Fatal(err)
	}
	expanded, err := result.ManifestForView("expanded")
	if err != nil {
		t.Fatal(err)
	}
	address := "app/http_gateway/public_api"
	sourceGateway := resourcesByAddress(source)[address]
	effectiveGateway := resourcesByAddress(effective)[address]
	if sourceGateway.Spec["request_limit"] != nil {
		t.Fatalf("source view contains effective default: %#v", sourceGateway.Spec["request_limit"])
	}
	if effectiveGateway.Spec["request_limit"] == nil {
		t.Fatal("effective view omitted HTTP defaults")
	}
	if expanded != result.Manifest || source.ContractRevision != expanded.ContractRevision || effective.ContractRevision != expanded.ContractRevision {
		t.Fatal("graph views do not share the deployable contract identity")
	}
	if _, err := result.ManifestForView("invented"); err == nil || !strings.Contains(err.Error(), "unsupported graph view") {
		t.Fatalf("invalid view error = %v", err)
	}
}

func TestTypeValidationRejectsStringReferencesAndDuplicateWireNames(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "house", "invalid.scn")
	invalid := `record "invalid" {
  field "first" {
    type = string
    wire_name = "same"
  }
  field "second" {
    type = string
    wire_name = "same"
  }
}
operation "invalid" {
  service = service.house
  input = "record.invalid"
  handler { method = "Invalid" }
  result "ok" { type = record.invalid }
}`
	if err := os.WriteFile(path, []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"SCN1202", "SCN1204"} {
		if !hasDiagnostic(result.Diagnostics, code) {
			t.Errorf("missing %s in %#v", code, result.Diagnostics)
		}
	}
}

func TestCompilerValidatesNestedBlockSchemasBeforeFlattening(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	invalid := `record "invalid_nested" {
  field "name" "extra" {
    type = string
    typo = true
  }
}

operation "invalid_nested" {
  service = service.house
  input   = record.invalid_nested

  handler { method = "First" }
  handler { method = "Second" }

  result "ok" {
    type = string
    unexpected {}
  }
}
`
	if err := os.WriteFile(filepath.Join(temp, "house", "invalid_nested.scn"), []byte(invalid), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	for _, code := range []string{"SCN1014", "SCN1016", "SCN1017", "SCN1018"} {
		if !hasDiagnostic(result.Diagnostics, code) {
			t.Errorf("missing %s in %#v", code, result.Diagnostics)
		}
	}
}

func TestTypeValidationRejectsUnknownReference(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "house", "invalid.scn")
	if err := os.WriteFile(path, []byte(`record "invalid" {
  field "value" { type = record.missing }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, "SCN1203") {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
}

func TestReferenceValidationRejectsUnknownResourcesAndModuleInputs(t *testing.T) {
	resources := []Resource{
		{Address: "app/module/house", Module: "app", Kind: "scenery.module", Name: "house", Spec: map[string]any{"source": "./house", "inputs": map[string]any{"gateway": map[string]any{"$ref": "http_gateway.public"}}}},
		{Address: "app/http_gateway/public", Module: "app", Kind: "scenery.http-gateway", Name: "public", Spec: map[string]any{}},
		{Address: "house/binding/bad", Module: "house", Kind: "scenery.binding", Name: "bad", Spec: map[string]any{
			"gateway": map[string]any{"$ref": "http_gateway.missing"}, "operation": map[string]any{"$ref": "operation.missing"}, "execution": map[string]any{"$ref": "var.missing"},
		}},
	}
	diagnostics := validateReferences(resources)
	if got := diagnosticCount(diagnostics, "SCN1207"); got != 3 {
		t.Fatalf("SCN1207 count = %d, diagnostics = %#v", got, diagnostics)
	}
}

func TestResourceSchemaRejectsUnknownFields(t *testing.T) {
	resources := []Resource{{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Spec: map[string]any{
		"service": map[string]any{"$ref": "service.house"}, "input": map[string]any{"$ref": "string"}, "handler": map[string]any{"method": "Process"}, "result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "string"}}, "typo": true,
	}}}
	diagnostics := validateResourceSchemas(resources)
	if !hasDiagnostic(diagnostics, "SCN1007") {
		t.Fatalf("diagnostics: %#v", diagnostics)
	}
}

func TestCompilerRejectsDynamicHCLExpressions(t *testing.T) {
	temp := t.TempDir()
	copyTree(t, filepath.Join("testdata", "house"), temp)
	path := filepath.Join(temp, "house", "invalid.scn")
	if err := os.WriteFile(path, []byte(`record "invalid" {
  field "value" { type = var.dynamic ? string : int64 }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := Compile(temp)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(result.Diagnostics, "SCN1010") {
		t.Fatalf("diagnostics: %#v", result.Diagnostics)
	}
}

func TestCompilerAllowsRuntimeAuthorizationExpressionsOnlyInRules(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "scenery.scn")
	if err := os.WriteFile(path, []byte(`authorization "member" {
  principal = std.type.authenticated_principal
  strategy  = "deny_unless_allowed"

  rule "signed_in" {
    allow = principal.uid != ""
  }
}`), 0o644); err != nil {
		t.Fatal(err)
	}
	source, diagnostics := parseSource(root, path)
	if hasErrors(diagnostics) {
		t.Fatalf("parse diagnostics = %#v", diagnostics)
	}
	if diagnostics := validateStaticExpressions([]*Source{source}); hasErrors(diagnostics) {
		t.Fatalf("runtime policy expression rejected: %#v", diagnostics)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func TestCompileValidatesIdempotencyKeysAgainstInputRecord(t *testing.T) {
	tests := []struct {
		name, key string
		valid     bool
	}{
		{name: "direct field", key: "[input.scene_id]", valid: true},
		{name: "scalar", key: "input.scene_id"},
		{name: "missing field", key: "[input.missing]"},
		{name: "nested field", key: "[input.scene_id.value]"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			copyTree(t, filepath.Join("testdata", "house"), root)
			path := filepath.Join(root, "house", "scenery.package.scn")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			const needle = "  input   = record.process_scene_input\n\n  handler {"
			replacement := "  input   = record.process_scene_input\n\n  idempotency {\n    mode = \"keyed\"\n    key  = " + test.key + "\n  }\n\n  handler {"
			if !strings.Contains(string(data), needle) {
				t.Fatal("operation fixture insertion point is missing")
			}
			if err := os.WriteFile(path, []byte(strings.Replace(string(data), needle, replacement, 1)), 0o644); err != nil {
				t.Fatal(err)
			}
			result, err := Compile(root)
			if err != nil {
				t.Fatal(err)
			}
			if result.Valid() != test.valid || (!test.valid && !hasDiagnostic(result.Diagnostics, "SCN2003")) {
				t.Fatalf("valid = %t, want %t; diagnostics = %#v", result.Valid(), test.valid, result.Diagnostics)
			}
		})
	}
}

func hasDiagnostic(diags []Diagnostic, code string) bool {
	for _, diag := range diags {
		if diag.Code == code {
			return true
		}
	}
	return false
}

func diagnosticCount(diags []Diagnostic, code string) int {
	count := 0
	for _, diagnostic := range diags {
		if diagnostic.Code == code {
			count++
		}
	}
	return count
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, path)
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0o755)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}
