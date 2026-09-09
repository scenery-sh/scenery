package generate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestHTTPDirectoryGroupsReachEveryGeneratedRoute(t *testing.T) {
	root := t.TempDir()
	writeMinimalNativeGenerationFixture(t, root)
	appendFixtureFile(t, filepath.Join(root, "house", testPackageFilename), `
input "gateway" { type = resource_ref("http_gateway") }

execution "inspect" {
  operation = operation.inspect
  mode = "direct"
}

binding "inspect" {
  gateway = var.gateway
  operation = operation.inspect
  execution = execution.inspect
  protocol = "http"
  delivery = "call"
  authentication = std.authentication.none
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  http {
    method = "POST"
    path = "/house/inspect"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "json"
      to = operation.inspect.input
    }
    response "ok" {
      when = result.ok
      status = 200
      body {
        codec = "json"
        from = result.ok
      }
    }
  }
}

export "inspect" { value = operation.inspect }
`)
	group := filepath.Join(root, "group1", "nested")
	if err := os.MkdirAll(group, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "house"), filepath.Join(group, "house")); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{testAppFilename, "group1/nested/house/" + testPackageFilename} {
		filename := filepath.Join(root, relative)
		source, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		updated := strings.NewReplacer(
			"example.test/nativeapp/house", "example.test/nativeapp/group1/nested/house",
			`"house/scenerycontract"`, `"group1/nested/house/scenerycontract"`,
			`source = "./house"`, "source = \"./group1/nested/house\"\n  inputs = { gateway = http_gateway.public_api }",
			`base_path       = "/"`, `base_path       = "/v1"`,
		).Replace(string(source))
		if err := os.WriteFile(filename, []byte(updated), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	result, err := compiler.Compile(root)
	if err != nil || result == nil || !result.Valid() {
		t.Fatalf("compile: %v diagnostics=%#v", err, diagnosticsOf(result))
	}
	const route = "/v1/group1/nested/house/inspect"
	goFiles, err := RenderGoWorkspaceFiles(result)
	if err != nil {
		t.Fatal(err)
	}
	serverFound := false
	for _, source := range goFiles {
		serverFound = serverFound || strings.Contains(string(source), `Path: "`+route+`"`)
	}
	if !serverFound {
		t.Fatalf("generated Go server has no route %q", route)
	}
	clientFiles, err := renderTypeScriptClientFiles(result, "public_api")
	if err != nil {
		t.Fatal(err)
	}
	if source := generatedSourceWithSuffix(clientFiles, "client.ts"); !strings.Contains(source, `"path":"`+route+`"`) {
		t.Fatalf("generated TypeScript descriptor has no route %q:\n%s", route, source)
	}
	artifact, err := GenerateOpenAPIArtifact(result, "public_api")
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(artifact.Document, &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Paths) != 1 || len(document.Paths[route]["post"]) == 0 {
		t.Fatalf("OpenAPI paths = %#v", document.Paths)
	}
}
