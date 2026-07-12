package vnext

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHTTPPathTailSyntaxAndMappingValidation(t *testing.T) {
	if _, err := parseHTTPPathTemplate("/drive/{bucket}/{path...}"); err != nil {
		t.Fatalf("valid path tail was rejected: %v", err)
	}
	for _, route := range []string{
		"/drive/{path...}/metadata", "/drive/prefix-{path...}", "/drive/{path...}-suffix", "/drive/{first...}/{second...}",
		"/drive/*path", "/drive/{*path}", "/drive/{path*}", "/drive/{Path...}", "/drive//{path...}", "/drive/./{path...}",
	} {
		if validHTTPPath(route) {
			t.Errorf("invalid path-tail template %q was accepted", route)
		}
	}
	binding := Resource{Address: "drive/binding/download", Module: "drive"}
	valid := map[string]any{"path_tail": map[string]any{"name": "path", "to": map[string]any{"$ref": "operation.download.input.path"}}}
	if diagnostics := validateHTTPPathMappings(binding, valid, "/drive/{path...}"); len(diagnostics) != 0 {
		t.Fatalf("matching path_tail mapping was rejected: %#v", diagnostics)
	}
	for _, invalid := range []map[string]any{
		{},
		{"path_tail": map[string]any{"name": "rest", "to": map[string]any{"$ref": "operation.download.input.path"}}},
		{"path_parameter": map[string]any{"name": "path", "to": map[string]any{"$ref": "operation.download.input.path"}}},
	} {
		if diagnostics := validateHTTPPathMappings(binding, invalid, "/drive/{path...}"); !hasDiagnostic(diagnostics, "SCN2110") {
			t.Errorf("invalid mapping %#v was accepted", invalid)
		}
	}
}

func TestHTTPPathTailTargetTypesAndProfiles(t *testing.T) {
	for _, test := range []struct {
		typeExpression string
		valid          bool
	}{
		{"string", true}, {"relative_path", true}, {"optional(relative_path)", true},
		{"optional(string)", false}, {"nullable(relative_path)", false}, {"list(string)", false}, {"uuid", false},
	} {
		resources, operation, binding, httpSpec := pathTailMappingFixture(test.typeExpression)
		diagnostics := validateHTTPInputMappings(resources, binding, operation, httpSpec)
		if got := !hasDiagnostic(diagnostics, "SCN2114"); got != test.valid {
			t.Errorf("target %s valid=%t diagnostics=%#v", test.typeExpression, got, diagnostics)
		}
	}
	resources, _, binding, _ := pathTailMappingFixture("string")
	resourceList := make([]Resource, 0, len(resources)+1)
	for _, resource := range resources {
		resourceList = append(resourceList, resource)
	}
	binding.Spec = map[string]any{"protocol": "http", "http": map[string]any{"path_tail": map[string]any{"name": "path"}}}
	resourceList = append(resourceList, binding)
	if diagnostics := validateProfiles([]string{"scenery.compiler-core/v1", "scenery.http-codec/v1", "scenery.runtime-http/v1"}, resourceList); !hasDiagnostic(diagnostics, "SCN7008") {
		t.Fatalf("missing extension profiles were accepted: %#v", diagnostics)
	}
	profiles := normalizeProfiles([]string{RuntimeHTTPPathTailProfile}, resourceList)
	if diagnostics := validateProfiles(profiles, resourceList); hasDiagnostic(diagnostics, "SCN7008") {
		t.Fatalf("active extension profiles were rejected: %#v", diagnostics)
	}
}

func TestHTTPPathTailChangesRequireSecurityReview(t *testing.T) {
	classification := classifySecurityChange("replace", "/spec/http/path", "/drive/{path}", "/drive/{path...}")
	if classification.Result != CompatibilityUnknown || classification.Relation != SecurityUnknown {
		t.Fatalf("path-tail security classification = %#v", classification)
	}
}

func TestHTTPPathTailRouteIdentityAllowsSpecificRoutesAndRejectsEqualTails(t *testing.T) {
	gateway := Resource{Address: "app/http_gateway/public", Kind: "scenery.http-gateway/v1", Spec: map[string]any{"base_path": "/", "exposure": "internal"}}
	binding := func(name, route string) Resource {
		return Resource{Address: "drive/binding/" + name, Module: "drive", Kind: "scenery.binding/v1", Spec: map[string]any{
			"gateway": map[string]any{"$ref": gateway.Address}, "protocol": "http", "http": map[string]any{"method": "GET", "path": route},
		}}
	}
	valid := []Resource{gateway, binding("tail", "/drive/{path...}"), binding("literal", "/drive/health"), binding("parameter", "/drive/{bucket}"), binding("exact", "/drive"), binding("nested", "/drive/public/{path...}")}
	if diagnostics := validateHTTPResources(valid); hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("specific routes conflicted with a tail: %#v", diagnostics)
	}
	invalid := append(valid, binding("rest", "/drive/{rest...}"))
	if diagnostics := validateHTTPResources(invalid); !hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("equal path-tail match sets did not conflict: %#v", diagnostics)
	}
	headConflict := binding("head", "/drive/{rest...}")
	headConflict.Spec["http"].(map[string]any)["method"] = "HEAD"
	if diagnostics := validateHTTPResources(append(valid, headConflict)); !hasDiagnostic(diagnostics, "SCN2002") {
		t.Fatalf("automatic GET/HEAD tail overlap did not conflict: %#v", diagnostics)
	}
}

func TestDrivePathTailCompilesAndProjectsAllArtifacts(t *testing.T) {
	parallelVNextIntegrationTest(t)
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "native"), root)
	rootPath := filepath.Join(root, "scenery.scn")
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http/v1",`, `"scenery.runtime-http/v1",
    "scenery.http-path-tail/v1",
    "scenery.runtime-http-path-tail/v1",`, 1))
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", "scenery.package.scn")
	packageSource, err := os.ReadFile(packagePath)
	if err != nil {
		t.Fatal(err)
	}
	packageSource = append(packageSource, []byte(`

binding "download_http" {
  gateway   = var.gateway
  operation = operation.process_scene
  execution = execution.process_scene_direct
  protocol  = "http"
  delivery  = "call"

  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty

  http {
    method        = "GET"
    path          = "/drive/{path...}"
    codec_profile = std.codec.http_json_v1

    path_tail "path" {
      to = operation.process_scene.input.scene_id
    }

    response "processed" {
      when   = result.processed
      status = 200
      body {
        codec = "json"
        from  = result.processed
      }
    }
  }
}
`)...)
	if err := os.WriteFile(packagePath, packageSource, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Format(root, false); err != nil {
		t.Fatal(err)
	}
	result, err := compileContractGraph(root, false)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	binding := resourcesByAddress(result.Manifest)["house/binding/download_http"]
	httpSpec := binding.Spec["http"].(map[string]any)
	tail := namedChildren(httpSpec, "path_tail")[0]
	if stringValue(tail["target_type"]) != "string" || stringValue(tail["empty_capture"]) != "empty_string" || stringValue(tail["decoding"]) != "segment_rfc3986_once" || !containsString(literalStringListFromValue(httpSpec["required_profiles"]), HTTPPathTailProfile) {
		t.Fatalf("effective path-tail metadata = %#v", tail)
	}

	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		t.Fatal(err)
	}
	applicationSource := generatedContent(applicationFiles, "adapter.gen.go")
	for _, fragment := range []string{"Path: \"/api/drive/*path\"", "ContractSourcePathTail", "ContractPathTail", "CanonicalTemplate: \"/api/drive/{path...}\"", `Precedence: []string{"literal", "parameter", "exact_end", "path_tail"}`, RuntimeHTTPPathTailProfile} {
		if !strings.Contains(applicationSource, fragment) {
			t.Errorf("generated Go adapter omitted %q", fragment)
		}
	}
	if descriptor := generatedContent(applicationFiles, "scenery.generated.v1.json"); !strings.Contains(descriptor, HTTPPathTailProfile) || !strings.Contains(descriptor, RuntimeHTTPPathTailProfile) {
		t.Fatalf("generated application descriptor omitted path-tail profiles: %s", descriptor)
	}

	typescriptFiles, err := renderTypeScriptClientFiles(result, "public_api")
	if err != nil {
		t.Fatal(err)
	}
	clientSource := generatedContent(typescriptFiles, "client.ts")
	if !strings.Contains(clientSource, `let path = "/api/drive"`) || !strings.Contains(clientSource, "Runtime.appendPathTail(path, input.sceneId") {
		t.Fatalf("generated TypeScript path-tail URL construction is missing:\n%s", clientSource)
	}
	for _, name := range []string{"metadata.ts", "scenery.typescript-client-generated.v1.json"} {
		if content := generatedContent(typescriptFiles, name); !strings.Contains(content, HTTPPathTailProfile) {
			t.Errorf("%s omitted path-tail profile", name)
		}
	}

	openAPI, err := GenerateOpenAPIArtifact(result, "public_api")
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal(openAPI.Document, &document); err != nil {
		t.Fatal(err)
	}
	paths := document["paths"].(map[string]any)
	operation := paths["/api/drive/{path...}"].(map[string]any)["get"].(map[string]any)
	extension, _ := operation["x-scenery-path-tail"].(map[string]any)
	if extension["target_type"] != "string" || extension["cardinality"] != "zero_or_more" || extension["trailing_slash"] != "no_match" || extension["segment_encoding"] != "rfc3986_independent" {
		t.Fatalf("OpenAPI path-tail extension = %#v", extension)
	}
	if !strings.Contains(string(openAPI.Descriptor), HTTPPathTailProfile) {
		t.Fatalf("OpenAPI descriptor omitted path-tail profile: %s", openAPI.Descriptor)
	}
}

func pathTailMappingFixture(typeExpressionValue string) (map[string]Resource, Resource, Resource, map[string]any) {
	typeValue := any(map[string]any{"$ref": typeExpressionValue})
	if strings.Contains(typeExpressionValue, "(") {
		typeValue = map[string]any{"$expression": typeExpressionValue}
	}
	record := Resource{Address: "drive/record/download_input", Module: "drive", Name: "download_input", Kind: "scenery.record/v1", Spec: map[string]any{"field": map[string]any{"name": "path", "type": typeValue}}}
	operation := Resource{Address: "drive/operation/download", Module: "drive", Name: "download", Kind: "scenery.operation/v1", Spec: map[string]any{"input": map[string]any{"$ref": "record.download_input"}}}
	binding := Resource{Address: "drive/binding/download", Module: "drive", Name: "download", Kind: "scenery.binding/v1"}
	httpSpec := map[string]any{"path_tail": map[string]any{"name": "path", "to": map[string]any{"$ref": "operation.download.input.path"}}}
	return map[string]Resource{record.Address: record, operation.Address: operation}, operation, binding, httpSpec
}

func generatedContent(files []generatedFile, suffix string) string {
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), suffix) {
			return string(file.Bytes)
		}
	}
	return ""
}
