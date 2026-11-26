package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPythonTargetsValidationAndSelection(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("app.scn", `application "pyapp" {}
workspace { managed_generated_roots = ["scoring/scenerycontract"] }
python_workspace "application" { root = "." }
python_toolchain "application" { version = "3.14.6" }
python_target "development" {
  role      = "development"
  workspace = python_workspace.application
  toolchain = python_toolchain.application
}
python_target "production" {
  role      = "artifact"
  workspace = python_workspace.application
  toolchain = python_toolchain.application
}
http_gateway "public_api" {
  exposure        = "internet"
  base_path       = "/api"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}
module "scoring" {
  source = "./scoring"
  inputs = { gateway = http_gateway.public_api }
}
`)
	write("scoring/package.scn", `package "scoring" {
  python_contract { entry = "service.py" }
}
input "gateway" { type = resource_ref("http_gateway") }
service "scoring" {
  runtime = "python"
  implementation { constructor = "new_service" }
}
operation "ping" {
  service = service.scoring
  input   = std.type.unit
  handler { method = "ping" }
  result "ok" { type = string }
}
execution "ping" {
  operation = operation.ping
  mode      = "direct"
  timeout   = "10s"
}
binding "ping" {
  gateway        = var.gateway
  operation      = operation.ping
  execution      = execution.ping
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "GET"
    path          = "/ping"
    codec_profile = std.codec.http_json_v1
    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec = "json"
        from  = result.ok
      }
    }
  }
}
`)
	write("scoring/service.py", "def new_service(input):\n    return None\n")

	compiled, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Valid() {
		t.Fatalf("expected valid Python app, got diagnostics: %#v", compiled.Diagnostics)
	}
	var service Resource
	for _, resource := range compiled.Manifest.Resources {
		if resource.Kind == "scenery.service" && resource.Name == "scoring" {
			service = resource
		}
	}
	target, err := SelectPythonWorkerTarget(compiled, service, "development")
	if err != nil {
		t.Fatal(err)
	}
	if target.Directory != "scoring" || target.Workspace != "." || target.Entry != "service.py" || target.Version != "3.14.6" {
		t.Fatalf("unexpected target: %#v", target)
	}

	// Reject a keyword constructor name.
	write("scoring/package.scn", `package "scoring" {
  python_contract { entry = "service.py" }
}
input "gateway" { type = resource_ref("http_gateway") }
service "scoring" {
  runtime = "python"
  implementation { constructor = "class" }
}
`)
	invalid, err := Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(invalid.Diagnostics, "SCN6171") {
		t.Fatalf("expected SCN6171 for keyword constructor, got %#v", invalid.Diagnostics)
	}
}
