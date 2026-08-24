package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/compiler"
)

func copyAssistantFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeAssistantTestFile(t, filepath.Join(root, ".scenery.json"), `{"name":"assistant-fixture","envs":{"local":{"default":true}}}`)
	writeAssistantTestFile(t, filepath.Join(root, testAppFilename), `workspace {
  implementation_root "application" {
    path = "."
    revision_include = ["assistants/**/*.ts", "assistants/**/package.json", "assistants/**/package-lock.json"]
  }
  managed_generated_roots = ["clients/generated/public_api"]
}
application "assistant_fixture" {}
http_gateway "public_api" {
  exposure = "internet"
  base_path = "/"
  cors = std.cors.none
  trusted_proxies = std.trusted_proxies.none
  forwarded = std.forwarded_headers.reject
}
module "house" { source = "./house" }
mcp_server "support" {
  capability "invoke" {
    binding = module.house.invoke_mcp
    name = "house__invoke"
    approval = "never"
  }
  max_input_bytes = 262144
  max_result_bytes = 1048576
}
assistant "support" {
  mcp_server = mcp_server.support
  implementation {
    adapter = "eve"
    source = "./assistants/support"
    package = "./assistants/support/package.json"
    package_lock = "./assistants/support/package-lock.json"
  }
  surface {
    gateway = http_gateway.public_api
    path = "/assistants/support"
    authentication = std.authentication.none
    authorization = std.authorization.public
    pipeline = std.pipeline.empty
    session_access = "initiator"
    client = typescript_client.public_api
  }
}
typescript_client "public_api" {
  gateways = [http_gateway.public_api]
  package = "@example/assistant-client"
  module = "esm"
  runtime = "fetch"
  output_root = "clients/generated/public_api"
}
`)
	writeAssistantTestFile(t, filepath.Join(root, "house", testPackageFilename), `package "house" {}
service "house" {
  runtime = "test"
  implementation { constructor = "NewService" }
}
record "invoke_input" {
  field "value" {
    type = string
  }
}
operation "invoke" {
  service = service.house
  input = record.invoke_input
  handler { method = "Invoke" }
}
execution "invoke" {
  operation = operation.invoke
  mode = "direct"
  timeout = "1s"
}
binding "invoke_mcp" {
  operation = operation.invoke
  execution = execution.invoke
  protocol = "mcp"
  delivery = "call"
  exposure = "application"
  authentication = std.authentication.inherit
  authorization = std.authorization.public
  pipeline = std.pipeline.empty
  mcp {
    name = "invoke"
    title = "Invoke"
    description = "Invoke the test operation."
    read_only = true
    destructive = false
    idempotent = true
    open_world = false
  }
}
export "invoke_mcp" { value = binding.invoke_mcp }
`)
	assets, err := eve.ScaffoldPackageFiles()
	if err != nil {
		t.Fatal(err)
	}
	writeAssistantTestFile(t, filepath.Join(root, "assistants", "support", "package.json"), string(assets["package.json"]))
	writeAssistantTestFile(t, filepath.Join(root, "assistants", "support", "package-lock.json"), string(assets["package-lock.json"]))
	writeAssistantTestFile(t, filepath.Join(root, "assistants", "support", "agent", "agent.ts"), scaffoldAgentSource)
	writeAssistantTestFile(t, filepath.Join(root, "assistants", "support", "agent", "instructions.md"), scaffoldInstructions)
	return root
}

func writeAssistantTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func initializeAssistantForFastTest(ctx context.Context, root string, cfg appcfg.Config, compiled *compiler.Result, opts assistantScaffoldOptions) (assistantInitResponse, error) {
	return initializeAssistantWithDependencies(ctx, root, cfg, compiled, opts, assistantInitDependencies{})
}

func TestAssistantInitDryRunLeavesWorkspaceUnchanged(t *testing.T) {
	t.Parallel()

	root := copyAssistantFixture(t)
	compiledRoot, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	appBefore, err := os.ReadFile(filepath.Join(root, "app.scn"))
	if err != nil {
		t.Fatal(err)
	}
	response, err := initializeAssistantForFastTest(context.Background(), compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api", DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !response.DryRun || response.Applied || len(response.Created) != 4 {
		t.Fatalf("dry-run response = %#v", response)
	}
	if got, _ := os.ReadFile(filepath.Join(root, "app.scn")); string(got) != string(appBefore) {
		t.Fatal("dry-run changed app.scn")
	}
	if _, err := os.Stat(filepath.Join(root, "assistants", "extra")); !os.IsNotExist(err) {
		t.Fatalf("dry-run created assistant files: %v", err)
	}
}

func TestAssistantInitAppliesScaffold(t *testing.T) {
	t.Parallel()

	root := copyAssistantFixture(t)
	compiledRoot, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	response, err := initializeAssistant(context.Background(), compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Applied || response.Idempotent || len(response.Created) != 4 {
		t.Fatalf("apply response = %#v", response)
	}
	if _, err := os.Stat(filepath.Join(root, "assistants", "extra", "eval")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, "assistants", "extra", "eval"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("eval directory not empty: %#v", entries)
	}
	lock, err := os.ReadFile(filepath.Join(root, "assistants", "extra", "package-lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(lock)
	if got, want := "sha256:"+hex.EncodeToString(sum[:]), "sha256:50688be5a4ea2b73acffd21b724caa699ea81e8343befd22b1212e89e845938a"; got != want {
		t.Fatalf("lock digest=%s want=%s", got, want)
	}
	if !strings.Contains(string(mustReadAssistant(t, filepath.Join(root, "app.scn"))), `assistant "extra"`) {
		t.Fatal("canonical assistant block missing")
	}
}

func TestAssistantInitIsIdempotentAndPreservesAuthoredFiles(t *testing.T) {
	t.Parallel()

	root := copyAssistantFixture(t)
	instructionsPath := filepath.Join(root, "assistants", "support", "agent", "instructions.md")
	if err := os.WriteFile(instructionsPath, []byte("edited by developer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compiledRoot, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	response, err := initializeAssistantForFastTest(context.Background(), compiledRoot, cfg, compiled, assistantScaffoldOptions{Name: "support", MCPServer: "support", Client: "public_api"})
	if err != nil {
		t.Fatal(err)
	}
	if !response.Idempotent || response.Applied || len(response.Created) != 0 {
		t.Fatalf("second init response = %#v", response)
	}
	if got, _ := os.ReadFile(instructionsPath); string(got) != "edited by developer\n" {
		t.Fatal("second init overwrote authored instructions")
	}
}

func TestAssistantInitRejectsExistingNonRegularScaffoldFile(t *testing.T) {
	root := copyAssistantFixture(t)
	path := filepath.Join(root, "assistants", "extra", "agent")
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(path, "instructions.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, cfg, compiled, err := loadAssistantApp(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := initializeAssistant(context.Background(), root, cfg, compiled, assistantScaffoldOptions{Name: "extra", MCPServer: "support", Client: "public_api", DryRun: true}); err == nil {
		t.Fatal("init accepted directory at authored file path")
	}
}

func TestAssistantScaffoldResponsesMatchSchemas(t *testing.T) {
	root := repoRootForTest(t)
	initResponse := assistantInitResponse{
		cliPayloadIdentity:         newCLIPayloadIdentity(assistantInitKind),
		Assistant:                  "support",
		Address:                    "app/assistant/support",
		MCPServer:                  "support",
		Client:                     "public_api",
		Source:                     "./assistants/support",
		Package:                    "./assistants/support/package.json",
		PackageLock:                "./assistants/support/package-lock.json",
		EvalDirectory:              "./assistants/support/eval",
		DryRun:                     true,
		Applied:                    false,
		Idempotent:                 false,
		Created:                    []string{"./assistants/support/agent/agent.ts"},
		Preserved:                  []string{},
		PlanID:                     digestBytes([]byte("plan")),
		BaseWorkspaceRevision:      digestBytes([]byte("base")),
		PredictedWorkspaceRevision: digestBytes([]byte("predicted")),
		ContractRevision:           digestBytes([]byte("contract")),
		Files:                      []assistantInitFile{{Path: "./assistants/support/agent/agent.ts", Action: "create"}},
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.init.schema.json"), initResponse); len(diagnostics) != 0 {
		t.Fatalf("init response schema diagnostics = %v", diagnostics)
	}
	syncResponse := assistantSyncResponse{
		cliPayloadIdentity: newCLIPayloadIdentity(assistantSyncKind), Assistant: "support", Address: "app/assistant/support",
		Source: "./assistants/support", Package: "./assistants/support/package.json", PackageLock: "./assistants/support/package-lock.json",
		LockDigest: digestBytes([]byte("lock")), PackageDigest: digestBytes([]byte("package")), CachePath: "/tmp/cache", Status: "reused", Reused: true,
		NodePath: "/tmp/node", NPMPath: "/tmp/npm",
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", "scenery.assistant.sync.schema.json"), syncResponse); len(diagnostics) != 0 {
		t.Fatalf("sync response schema diagnostics = %v", diagnostics)
	}
}

func mustReadAssistant(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
