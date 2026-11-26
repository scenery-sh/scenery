package generate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGeneratePythonContractsRendersTypedModelsClientsAndBuilder(t *testing.T) {
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
	write("app.scn", `application "pygen" {}
workspace { managed_generated_roots = ["echo/scenerycontract"] }
python_workspace "application" { root = "." }
python_toolchain "application" { version = "3.14.6" }
python_target "development" {
  role      = "development"
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
module "echo" {
  source = "./echo"
  inputs = { gateway = http_gateway.public_api }
}
`)
	write("echo/package.scn", `package "echo" {
  python_contract { entry = "service.py" }
}
input "gateway" { type = resource_ref("http_gateway") }
service "echo" {
  runtime = "python"
  implementation { constructor = "new_service" }
  lifecycle {
    start = "start"
    stop  = "stop"
  }
}
enum "status" {
  value "open" {}
  value "closed" {}
}
record "echo_input" {
  unknown_fields = "preserve"
  field "note" { type = optional(nullable(string)) }
  field "amount" { type = int64 }
  field "status" { type = enum.status }
}
operation "echo" {
  service = service.echo
  input   = record.echo_input
  handler { method = "echo" }
  result "ok" { type = record.echo_input }
  error "negative" { type = std.type.problem }
}
execution "echo" {
  operation = operation.echo
  mode      = "direct"
  timeout   = "10s"
}
binding "echo" {
  gateway        = var.gateway
  operation      = operation.echo
  execution      = execution.echo
  protocol       = "http"
  delivery       = "call"
  authentication = std.authentication.none
  authorization  = std.authorization.public
  pipeline       = std.pipeline.empty
  http {
    method        = "POST"
    path          = "/echo"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "json"
      to    = operation.echo.input
    }
    response "ok" {
      when   = result.ok
      status = 200
      body {
        codec = "json"
        from  = result.ok
      }
    }
    response "negative" {
      when   = error.negative
      status = 422
      body {
        codec = "problem_json"
        from  = error.negative
      }
    }
  }
}
`)
	write("echo/service.py", "def new_service(input):\n    return object()\n")

	compiled, err := compiler.Compile(root)
	if err != nil {
		t.Fatal(err)
	}
	if !compiled.Valid() {
		t.Fatalf("compile diagnostics: %#v", compiled.Diagnostics)
	}
	result, err := GenerateSnapshotWithContext(t.Context(), compiled, "contracts", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Changed) == 0 {
		t.Fatal("expected generated Python contract files")
	}

	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	typesSrc := read("echo/scenerycontract/_types.py")
	for _, want := range []string{
		`Status: TypeAlias = Literal["closed", "open"]`,
		"class EchoInput:",
		"    amount: int\n    status: Status\n    note: str | None | MissingType = MISSING\n    unknown_fields: Mapping[str, JsonValue] = field(default_factory=dict)",
		"class EchoOk:",
		"class EchoNegative:",
		"EchoOutcome: TypeAlias = EchoOk | EchoNegative",
		"__all__ = [",
	} {
		if !strings.Contains(typesSrc, want) {
			t.Errorf("_types.py lacks %q:\n%s", want, typesSrc)
		}
	}

	initSrc := read("echo/scenerycontract/__init__.py")
	for _, want := range []string{
		`SERVICE_ADDRESS = "echo/service/echo"`,
		"class ConstructorInput:",
		"class Service(Protocol):",
		"def start(self) -> None | Awaitable[None]: ...",
		"def stop(self) -> None | Awaitable[None]: ...",
		"def echo(self, ctx: Context, input: EchoInput) -> EchoOutcome | Awaitable[EchoOutcome]: ...",
		"async def dispatch(",
		"__all__ = [",
	} {
		if !strings.Contains(initSrc, want) {
			t.Errorf("__init__.py lacks %q:\n%s", want, initSrc)
		}
	}

	mainSrc := read("echo/scenerycontract/_main.py")
	for _, want := range []string{
		`mod = importlib.import_module("service")`,
		`construct = getattr(mod, "new_service")`,
		"await maybe_await(service.start())",
		"await maybe_await(service.stop())",
	} {
		if !strings.Contains(mainSrc, want) {
			t.Errorf("_main.py lacks %q:\n%s", want, mainSrc)
		}
	}

	buildSrc := read("echo/scenerycontract/_build.py")
	if !strings.Contains(buildSrc, `ENTRY = "service.py"`) || !strings.Contains(buildSrc, `CONSTRUCTOR = "new_service"`) {
		t.Errorf("_build.py missing entry/constructor constants:\n%s", buildSrc)
	}
	for _, sdkFile := range []string{"__init__.py", "_hpack.py", "_h2c.py", "codec.py", "host.py", "worker.py"} {
		if read("echo/scenerycontract/sdk/"+sdkFile) == "" {
			t.Errorf("missing sdk/%s", sdkFile)
		}
	}

	// Second run in --check mode must report zero drift.
	check, err := GenerateSnapshotWithContext(t.Context(), compiled, "contracts", true)
	if err != nil {
		t.Fatalf("check failed: %v", err)
	}
	if len(check.Changed) != 0 {
		t.Fatalf("unexpected drift on second generate: %v", check.Changed)
	}
}
