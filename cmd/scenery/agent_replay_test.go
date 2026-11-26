package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devreport"
)

func TestRuntimeFailureReportRoundTripsReplayInvocation(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)

	token := "rpt_replayroundtrip01"
	recordRuntimeFailureReport(devreport.ReportEnvelope{
		Type:        "internal-failure",
		AppID:       "polyglot",
		SessionID:   "ses_1",
		ReporterPID: 4242,
		InternalFailure: &devreport.InternalFailure{
			ReportToken: token,
			Code:        "SCN9000",
			Cause:       "division by zero in quote",
			Timestamp:   time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC),
			Invocation: &devreport.ReplayInvocation{
				Module:     "pricing",
				Runtime:    "rust",
				Service:    "pricing/service/pricing",
				Operation:  "pricing/operation/quote",
				Binding:    "pricing/binding/quote_http",
				Mode:       "direct",
				HTTPMethod: "POST",
				HTTPPath:   "/api/quote",
				HTTPBody:   []byte(`{"amount":"0"}`),
				Principal:  "usr_alice",
				TenantID:   "tnt_acme",
				Input:      json.RawMessage(`{"amount":"0"}`),
			},
		},
	})

	loaded, err := readFailureReport(token)
	if err != nil {
		t.Fatalf("readFailureReport: %v", err)
	}
	if loaded.Invocation == nil || loaded.Invocation.Module != "pricing" || loaded.Invocation.Principal != "usr_alice" {
		t.Fatalf("unexpected loaded invocation: %+v", loaded.Invocation)
	}
	var decodedInput map[string]string
	if err := json.Unmarshal(loaded.Invocation.Input, &decodedInput); err != nil || decodedInput["amount"] != "0" {
		t.Fatalf("unexpected canonical input: %s (err=%v)", loaded.Invocation.Input, err)
	}
}

func TestReplayEmitRegressionTestAcrossRuntimes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)

	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(".scenery.json", `{"name":"replayapp","envs":{"local":{"default":true}}}`)
	write("go.mod", "module example.test/replayapp\n\ngo 1.27.0\n")
	write("app.scn", `
application "replayapp" {}

workspace {
  managed_generated_roots = [
    "gomod/scenerycontract",
    "rustmod/scenerycontract",
    "tsmod/scenerycontract",
    "jsmod/scenerycontract",
    "pymod/scenerycontract",
  ]
}

http_gateway "api" {
  exposure        = "internet"
  base_path       = "/api"
  cors            = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded       = std.forwarded_headers.reject
}

go_module "app" {
  root        = "."
  import_path = "example.test/replayapp"
}
go_toolchain "app" { version = "1.27.0" }
go_target "dev" {
  role = "development"
  platform = "host"
  toolchain = go_toolchain.app
  module = go_module.app
  packages = ["./..."]
  cgo = "disabled"
}

rust_workspace "app" { root = "rustmod" }
rust_toolchain "app" { version = "1.99.1" }
rust_target "dev" {
  role      = "development"
  workspace = rust_workspace.app
  toolchain = rust_toolchain.app
}

js_workspace "app" { root = "." }
js_toolchain "app" { version = "1.3.14" }
js_target "dev" {
  role      = "development"
  workspace = js_workspace.app
  toolchain = js_toolchain.app
}

python_workspace "app" { root = "." }
python_toolchain "app" { version = "3.14.6" }
python_target "dev" {
  role      = "development"
  workspace = python_workspace.app
  toolchain = python_toolchain.app
}

module "gomod" {
  source = "./gomod"
  inputs = { gateway = http_gateway.api }
}
module "rustmod" {
  source = "./rustmod"
  inputs = { gateway = http_gateway.api }
}
module "tsmod" {
  source = "./tsmod"
  inputs = { gateway = http_gateway.api }
}
module "jsmod" {
  source = "./jsmod"
  inputs = { gateway = http_gateway.api }
}
module "pymod" {
  source = "./pymod"
  inputs = { gateway = http_gateway.api }
}
`)

	modSCN := func(pkg, contractBlock, runtimeName, ctor string) string {
		return `package "` + pkg + `" {
  ` + contractBlock + `
}
input "gateway" { type = resource_ref("http_gateway") }
service "svc" {
  runtime = "` + runtimeName + `"
  implementation { constructor = "` + ctor + `" }
}
record "ping_input" {
  field "value" { type = string }
}
operation "ping" {
  service = service.svc
  input   = record.ping_input
  handler { method = "Ping" }
  result "ok" { type = record.ping_input }
}
execution "ping" {
  operation = operation.ping
  mode      = "direct"
  timeout   = "5s"
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
    method        = "POST"
    path          = "/` + pkg + `"
    codec_profile = std.codec.http_json_v1
    body {
      codec = "json"
      to    = operation.ping.input
    }
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
`
	}

	write("gomod/package.scn", modSCN("gomod", `go_contract { import_path = "example.test/replayapp/gomod" }`, "go", "NewService"))
	write("gomod/service.go", "package gomod\n")
	write("rustmod/package.scn", modSCN("rustmod", `rust_contract { crate = "rustmod" }`, "rust", "Service::new"))
	write("rustmod/Cargo.toml", "[package]\nname = \"rustmod\"\nversion = \"0.1.0\"\nedition = \"2024\"\n")
	write("rustmod/src/lib.rs", "// stub\n")
	write("tsmod/package.scn", modSCN("tsmod", `js_contract { entry = "service.ts" }`, "ts", "newService"))
	write("tsmod/service.ts", "export const newService = () => ({});\n")
	write("jsmod/package.scn", modSCN("jsmod", `js_contract { entry = "service.js" }`, "js", "newService"))
	write("jsmod/service.js", "export const newService = () => ({});\n")
	write("pymod/package.scn", modSCN("pymod", `python_contract { entry = "service.py" }`, "python", "new_service"))
	write("pymod/service.py", "def new_service(inp): return None\n")

	cases := []struct {
		mod      string
		rt       string
		wantExt  string
		contains []string
	}{
		{"gomod", "go", "_test.go", []string{"NewService"}},
		{"rustmod", "rust", ".rs", []string{"sdk::Context::with_principal"}},
		{"tsmod", "ts", ".test.ts", []string{`from "bun:test"`, "standaloneContext("}},
		{"jsmod", "js", ".test.js", []string{`from "bun:test"`, "standaloneContext("}},
		{"pymod", "python", ".py", []string{"unittest.IsolatedAsyncioTestCase", `mode="direct"`}},
	}

	g, err := loadAgentGraph(context.Background(), root)
	if err != nil {
		t.Fatalf("loadAgentGraph: %v", err)
	}
	modules, err := g.modules()
	if err != nil {
		t.Fatalf("modules: %v", err)
	}
	modByName := make(map[string]agentModule, len(modules))
	for _, m := range modules {
		modByName[m.Name] = m
	}

	for i, tc := range cases {
		token := "rpt_emitcase000" + string(rune('a'+i))
		inv := &devreport.ReplayInvocation{
			Module:    tc.mod,
			Runtime:   tc.rt,
			Service:   tc.mod + "/service/svc",
			Operation: tc.mod + "/operation/ping",
			Binding:   tc.mod + "/binding/ping",
			Mode:      "direct",
			Principal: "usr_1",
			TenantID:  "tnt_1",
			Input:     json.RawMessage(`{"value":"crash"}`),
		}
		if i == 0 {
			// Exercise full CLI envelope & file emission path once; render the remaining runtimes directly in-memory.
			recordRuntimeFailureReport(devreport.ReportEnvelope{
				Type:  "internal-failure",
				AppID: "replayapp",
				InternalFailure: &devreport.InternalFailure{
					ReportToken: token,
					Code:        "SCN9000",
					Cause:       "boom",
					Invocation:  inv,
				},
			})
			var buf bytes.Buffer
			if err := runAgentReplay(context.Background(), &buf, []string{token, "--emit-test", "--app-root", root, "-o", "json"}); err != nil {
				t.Fatalf("%s: runAgentReplay --emit-test failed: %v", tc.rt, err)
			}
			var env struct {
				OK   bool              `json:"ok"`
				Data agentReplayResult `json:"data"`
			}
			if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
				t.Fatalf("%s: decode JSON: %v", tc.rt, err)
			}
			res := env.Data
			if !env.OK || !res.OK || res.Status != "emitted" || !strings.HasSuffix(res.EmittedTestPath, tc.wantExt) {
				t.Fatalf("%s: unexpected result %+v", tc.rt, res)
			}
			generated, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(res.EmittedTestPath)))
			if err != nil {
				t.Fatalf("%s: read emitted test: %v", tc.rt, err)
			}
			for _, sub := range append(tc.contains, "crash") {
				if !strings.Contains(string(generated), sub) {
					t.Fatalf("%s: emitted test missing expected content (%q):\n%s", tc.rt, sub, generated)
				}
			}
			continue
		}

		relPath, generated, err := renderReplayTestFile(
			g,
			modByName[tc.mod],
			g.resources[inv.Service],
			g.resources[inv.Operation],
			failureReport{ReportToken: token, Code: "SCN9000", Cause: "boom", Origin: "runtime", Invocation: inv},
			strings.TrimPrefix(token, "rpt_"),
		)
		if err != nil {
			t.Fatalf("%s: renderReplayTestFile failed: %v", tc.rt, err)
		}
		if !strings.HasSuffix(relPath, tc.wantExt) {
			t.Fatalf("%s: unexpected path %q, want suffix %q", tc.rt, relPath, tc.wantExt)
		}
		for _, sub := range append(tc.contains, "crash") {
			if !strings.Contains(generated, sub) {
				t.Fatalf("%s: rendered test missing expected content (%q):\n%s", tc.rt, sub, generated)
			}
		}
	}

	// Verify SQL database URLs are rendered for both Python and JS/TS services.
	sqlDeps := []sqlDependencyEntry{{name: "database", schema: "orders_db"}}
	if got := renderPythonTestDatabases(sqlDeps); !strings.Contains(got, "ORDERS_DB_DATABASE_URL") || !strings.Contains(got, `"database"`) {
		t.Fatalf("unexpected Python database map: %s", got)
	}
	if got := renderJSTestDatabases(sqlDeps); !strings.Contains(got, "ORDERS_DB_DATABASE_URL") || !strings.Contains(got, `"database"`) {
		t.Fatalf("unexpected JS database map: %s", got)
	}
}

func TestResolveDurableReplayTargetPrefersScheduleOverDirectExecution(t *testing.T) {
	root := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(".scenery.json", `{"name":"schedapp","envs":{"local":{"default":true}}}`)
	write("go.mod", "module example.test/schedapp\n\ngo 1.27.0\n")
	write("app.scn", `
application "schedapp" {}
go_module "app" {
  root        = "."
  import_path = "example.test/schedapp"
}
go_toolchain "app" { version = "1.27.0" }
go_target "dev" {
  role      = "development"
  platform  = "host"
  toolchain = go_toolchain.app
  module    = go_module.app
  packages  = ["./..."]
  cgo       = "disabled"
}
module "house" {
  source = "./house"
}
`)
	write("house/package.scn", `
package "house" {
  go_contract { import_path = "example.test/schedapp/house" }
}
service "svc" {
  runtime = "go"
  implementation { constructor = "NewService" }
}
record "empty" {}
operation "cleanup" {
  service = service.svc
  input   = record.empty
  handler { method = "Cleanup" }
  result "ok" { type = record.empty }
}
execution "cleanup_direct" {
  operation = operation.cleanup
  mode      = "direct"
  timeout   = "10s"
}
schedule "nightly" {
  trigger { every = "1h" }
  overlap = "skip"
  catchup { maximum_age = "10m" }
  invoke {
    operation     = operation.cleanup
    execution     = execution.cleanup_direct
    identity      = std.workload_identity.scheduler
    authorization = std.authorization.scheduled
    pipeline      = std.pipeline.empty
    input         = {}
  }
}
`)
	write("house/service.go", "package house\n")

	if _, err := loadAgentGraph(context.Background(), root); err != nil {
		t.Fatalf("loadAgentGraph: %v", err)
	}

	got := resolveDurableReplayTarget(context.Background(), root, &devreport.ReplayInvocation{
		Module:    "house",
		Runtime:   "go",
		Service:   "house/service/svc",
		Operation: "house/operation/cleanup",
		Mode:      "direct",
	})
	if got != "house/schedule/nightly" {
		t.Fatalf("resolveDurableReplayTarget(direct schedule) = %q, want house/schedule/nightly", got)
	}

	explicit := resolveDurableReplayTarget(context.Background(), root, &devreport.ReplayInvocation{
		Module:    "house",
		Runtime:   "go",
		Service:   "house/service/svc",
		Operation: "house/operation/cleanup",
		Mode:      "direct",
		Target:    "house/schedule/nightly",
	})
	if explicit != "house/schedule/nightly" {
		t.Fatalf("resolveDurableReplayTarget(explicit target) = %q, want house/schedule/nightly", explicit)
	}
}
