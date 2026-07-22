package compiler

import (
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

func TestHTTPPathTailTargetTypes(t *testing.T) {
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
}

func TestHTTPPathTailRouteIdentityAllowsSpecificRoutesAndRejectsEqualTails(t *testing.T) {
	gateway := Resource{Address: "app/http_gateway/public", Kind: "scenery.http-gateway", Spec: map[string]any{"base_path": "/", "exposure": "internal"}}
	binding := func(name, route string) Resource {
		return Resource{Address: "drive/binding/" + name, Module: "drive", Kind: "scenery.binding", Spec: map[string]any{
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
	rootPath := filepath.Join(root, appFilename)
	rootSource, err := os.ReadFile(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	rootSource = []byte(strings.Replace(string(rootSource), `"scenery.runtime-http",`, `"scenery.runtime-http",
    "scenery.http-path-tail",
    "scenery.runtime-http-path-tail",`, 1))
	if err := os.WriteFile(rootPath, rootSource, 0o644); err != nil {
		t.Fatal(err)
	}
	packagePath := filepath.Join(root, "house", packageFilename)
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
	result, err := CompileContractGraph(root)
	if err != nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, result.Diagnostics)
	}
	binding := resourcesByAddress(result.Manifest)["house/binding/download_http"]
	httpSpec := binding.Spec["http"].(map[string]any)
	tail := namedChildren(httpSpec, "path_tail")[0]
	if stringValue(tail["target_type"]) != "string" || stringValue(tail["empty_capture"]) != "empty_string" || stringValue(tail["decoding"]) != "segment_rfc3986_once" || httpSpec["required_profiles"] != nil {
		t.Fatalf("effective path-tail metadata = %#v", tail)
	}

}

func pathTailMappingFixture(typeExpressionValue string) (map[string]Resource, Resource, Resource, map[string]any) {
	typeValue := any(map[string]any{"$ref": typeExpressionValue})
	if strings.Contains(typeExpressionValue, "(") {
		typeValue = map[string]any{"$expression": typeExpressionValue}
	}
	record := Resource{Address: "drive/record/download_input", Module: "drive", Name: "download_input", Kind: "scenery.record", Spec: map[string]any{"field": map[string]any{"name": "path", "type": typeValue}}}
	operation := Resource{Address: "drive/operation/download", Module: "drive", Name: "download", Kind: "scenery.operation", Spec: map[string]any{"input": map[string]any{"$ref": "record.download_input"}}}
	binding := Resource{Address: "drive/binding/download", Module: "drive", Name: "download", Kind: "scenery.binding"}
	httpSpec := map[string]any{"path_tail": map[string]any{"name": "path", "to": map[string]any{"$ref": "operation.download.input.path"}}}
	return map[string]Resource{record.Address: record, operation.Address: operation}, operation, binding, httpSpec
}
