package generate

import (
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestGeneratedApplicationAdapterRegistersMCPToolThroughRuntime(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(filepath.ToSlash(file.Path), "/house_house_adapter/adapter.gen.go") {
			continue
		}
		source := string(file.Bytes)
		for _, fragment := range []string{
			"RegisterMCPTool",
			"AssistantAddress: \"app/assistant/support\"",
			"Name: \"house__process_scene\"",
			"InvokeContractPolicy",
			"service.ProcessScene",
		} {
			if !strings.Contains(source, fragment) {
				t.Fatalf("generated adapter missing %q:\n%s", fragment, source)
			}
		}
		return
	}
	t.Fatal("generated application adapter was not rendered")
}

func TestGeneratedDurableMCPTypeErrorUsesValidFormattingDirective(t *testing.T) {
	binding := Resource{Address: "house/binding/process_scene_mcp", Module: "house", Kind: "scenery.binding", Spec: map[string]any{
		"authorization": map[string]any{"$ref": "std.authorization.public"},
		"pipeline":      map[string]any{"$ref": "std.pipeline.empty"},
	}}
	operation := Resource{Address: "house/operation/process_scene", Module: "house", Kind: "scenery.operation", Name: "process_scene", Spec: map[string]any{
		"handler": map[string]any{"method": "ProcessScene"},
	}}
	execution := Resource{Address: "house/execution/process_scene_durable", Module: "house", Kind: "scenery.execution", Name: "process_scene_durable"}
	service := Resource{Address: "house/service/house", Module: "house", Kind: "scenery.service", Name: "house"}
	target := mcpToolTarget{
		Binding: binding, Operation: operation, Execution: execution,
		AssistantAddress: "app/assistant/support", Name: "house__process_scene",
		Durable: true, DurableService: "house", DurableTask: execution.Address,
	}
	var source strings.Builder
	if err := renderMCPToolRegistrations(&source, "sha256:contract", service, []mcpToolTarget{target}, []Resource{binding, operation, execution}); err != nil {
		t.Fatal(err)
	}
	generated := source.String()
	if !strings.Contains(generated, `fmt.Errorf("MCP durable tool returned %T, want scenery.ExecutionReceipt", value)`) {
		t.Fatalf("durable MCP type error is missing its formatting directive:\n%s", generated)
	}
	if strings.Contains(generated, `MCP durable tool returned %%T`) {
		t.Fatalf("durable MCP type error escaped its formatting directive into generated Go:\n%s", generated)
	}
}

func TestGeneratedMCPCompatibilityBindingBuffersHTTPStreamExplicitly(t *testing.T) {
	resultRecord := Resource{Address: "house/record/download_result", Module: "house", Kind: "scenery.record", Name: "download_result", Spec: map[string]any{
		"field": map[string]any{"name": "content", "type": map[string]any{"$ref": "bytes"}},
	}}
	operation := Resource{Address: "house/operation/download", Module: "house", Kind: "scenery.operation", Name: "download", Spec: map[string]any{
		"handler": map[string]any{"method": "Download"},
		"result":  map[string]any{"name": "success", "type": map[string]any{"$ref": "record.download_result"}},
	}}
	streamBinding := Resource{Address: "house/binding/download_http", Module: "house", Kind: "scenery.binding", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.download"}, "protocol": "http", "delivery": "stream",
		"http": map[string]any{"response": map[string]any{
			"name": "success", "when": map[string]any{"$ref": "result.success"}, "status": "200",
			"body": map[string]any{"codec": "bytes", "from": map[string]any{"$ref": "result.success.content"}},
		}},
	}}
	mcpBinding := Resource{Address: "house/binding/download_mcp", Module: "house", Kind: "scenery.binding", Spec: map[string]any{
		"operation": map[string]any{"$ref": "operation.download"}, "protocol": "mcp", "delivery": "call",
		"authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
	}}
	target := mcpToolTarget{Binding: mcpBinding, Operation: operation, AssistantAddress: "app/assistant/support", Name: "download", MaxResultBytes: 1024}
	var source strings.Builder
	resources := []Resource{resultRecord, operation, streamBinding, mcpBinding}
	if err := renderMCPToolRegistrations(&source, "sha256:contract", Resource{Name: "house"}, []mcpToolTarget{target}, resources); err != nil {
		t.Fatal(err)
	}
	generated := source.String()
	for _, fragment := range []string{
		"outcome, stream, err := service.Download(ctx, copied)",
		"if len(typed.Value.Content) != 0",
		"sceneryruntime.BufferContractByteStream(stream, 1024)",
		"typed.Value.Content = buffered",
		"contract.CloneDownloadOutcome(outcome)",
	} {
		if !strings.Contains(generated, fragment) {
			t.Fatalf("generated MCP stream bridge missing %q:\n%s", fragment, generated)
		}
	}
}

func TestGeneratedApplicationCompositionRegistersAssistantSurface(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	var composition string
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/composition/composition.gen.go") {
			composition = string(file.Bytes)
			break
		}
	}
	if composition == "" {
		t.Fatal("generated composition was not rendered")
	}
	want := []string{
		`sceneryruntime "scenery.sh/runtime"`,
		`registry.Register("scenery/assistants"`,
		`CoveredAddresses: []string{"app/assistant/support"}`, // root ownership is explicit
		`"house/binding/process_scene_mcp"`,
		`"house/execution/process_scene_direct"`,
		`Address: "app/assistant/support"`,
		`Name: "support"`,
		`Path: "/assistants/support"`,
		`Access: sceneryruntime.Public`,
		`BindingAddress: "app/assistant/support"`,
		`GatewayAddress: "app/http_gateway/public_api"`,
		`CORS: "none"`,
		`AuthorizationStrategy: "public"`,
		`PipelineSteps: []string{}`,
		`AssistantAddress: "app/assistant/support"`,
		`RuntimeRevision: "runtime-1"`,
		`CapabilityRevision: "` + result.Manifest.ContractRevision + `"`,
		`RegisterAssistantMCPManifestChecked`,
		`"app/mcp_server/support"`,
		`scenery.mcp-capability-manifest`,
	}
	for _, fragment := range want {
		if !strings.Contains(composition, fragment) {
			t.Fatalf("generated composition missing %q:\n%s", fragment, composition)
		}
	}
	if got := strings.Count(composition, "RegisterAssistantChecked"); got != 1 {
		t.Fatalf("generated assistant registration count = %d, want 1:\n%s", got, composition)
	}
	if got := strings.Count(composition, "RegisterAssistantMCPManifestChecked"); got != 1 {
		t.Fatalf("generated assistant MCP manifest registration count = %d, want 1:\n%s", got, composition)
	}
	if strings.Contains(composition, "eve") {
		t.Fatalf("provider adapter leaked into generated runtime registration:\n%s", composition)
	}
	for _, forbidden := range []string{"TokenManager:", "InitiatorSigner:", "Client:", "remote-secret", "secret-plaintext"} {
		if strings.Contains(composition, forbidden) {
			t.Fatalf("generated assistant registration includes runtime secret/client material %q:\n%s", forbidden, composition)
		}
	}
	targets := typescriptTargets(result.Manifest.Resources, "public_api")
	if len(targets) != 1 {
		t.Fatalf("public_api TypeScript target count = %d, want 1", len(targets))
	}
	clientFiles, err := renderTypeScriptTarget(result, targets[0])
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range clientFiles {
		if regexp.MustCompile(`(?i)(^|[^A-Za-z0-9_])eve([^A-Za-z0-9_]|$)`).Match(file.Bytes) {
			t.Fatalf("provider adapter leaked into public generated client/runtime metadata %s", file.Path)
		}
		if strings.Contains(string(file.Bytes), "docs.example.test") || strings.Contains(string(file.Bytes), "AuthScheme") || strings.Contains(string(file.Bytes), "X-Remote-Key") {
			t.Fatalf("private MCP connection data leaked into public generated client/runtime metadata %s", file.Path)
		}
	}
}

func TestGeneratedApplicationCompositionAssistantOrderingAndImplementationRevision(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	result.ImplementationRevisions = map[string]string{
		"z-development": "sha256:" + strings.Repeat("z", 64),
		"a-development": "sha256:" + strings.Repeat("a", 64),
	}
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.HasSuffix(filepath.ToSlash(file.Path), "/composition/composition.gen.go") {
			continue
		}
		source := string(file.Bytes)
		if !strings.Contains(source, `RuntimeRevision: "sha256:`+strings.Repeat("a", 64)+`"`) {
			t.Fatalf("composition did not choose deterministic first implementation revision:\n%s", source)
		}
		if strings.Contains(source, "eve") {
			t.Fatalf("provider adapter leaked into generated runtime registration:\n%s", source)
		}
		return
	}
	t.Fatal("generated composition was not rendered")
}

func TestGeneratedApplicationCompositionSupportsAssistantOnlyApps(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", "compiler", "testdata", "native"))
	if err != nil {
		t.Fatal(err)
	}
	result, err := compiler.Check(root)
	if err != nil || !result.Valid() {
		t.Fatalf("check: %v %#v", err, result.Diagnostics)
	}
	manifest := *result.Manifest
	filtered := make([]Resource, 0, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		// Keep the root graph and gateway/auth resources, but remove native
		// service ownership to exercise the assistant-only composition path.
		switch resource.Kind {
		case "scenery.service", "scenery.operation", "scenery.binding", "scenery.execution", "scenery.module":
			continue
		case "scenery.mcp-server":
			// Keep the server and its external connection, but remove local
			// capabilities whose bindings were removed above. This leaves a
			// valid external-only assistant manifest for projection.
			spec := make(map[string]any, len(resource.Spec))
			maps.Copy(spec, resource.Spec)
			spec["capability"] = []any{}
			resource.Spec = spec
			filtered = append(filtered, resource)
		default:
			filtered = append(filtered, resource)
		}
	}
	manifest.Resources = filtered
	result.Manifest = &manifest
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources))
	if err != nil {
		t.Fatal(err)
	}
	var composition string
	for _, file := range files {
		if strings.HasSuffix(filepath.ToSlash(file.Path), "/composition/composition.gen.go") {
			composition = string(file.Bytes)
			break
		}
	}
	if composition == "" {
		t.Fatal("generated assistant-only composition was not rendered")
	}
	for _, fragment := range []string{
		`var RequiredAddresses = []string{"app/assistant/support", "app/mcp_connection/docs", "app/mcp_server/support"}`,
		`registry.Register("scenery/assistants"`,
		`RegisterAssistantChecked`,
	} {
		if !strings.Contains(composition, fragment) {
			t.Fatalf("assistant-only composition missing %q:\n%s", fragment, composition)
		}
	}
	if strings.Contains(composition, "adapter0") {
		t.Fatalf("assistant-only composition unexpectedly imported an adapter:\n%s", composition)
	}
}

func TestMCPOnlyBindingOwnershipIncludesExecution(t *testing.T) {
	binding := Resource{Address: "house/binding/process_scene_mcp", Kind: "scenery.binding", Spec: map[string]any{
		"execution": map[string]any{"$ref": "house/execution/process_scene_direct"},
	}}
	targets := []mcpToolTarget{{Binding: binding}}
	bindings := mcpBindingResources(targets)
	if got := resourceAddresses(bindings); len(got) != 1 || got[0] != binding.Address {
		t.Fatalf("MCP binding ownership = %#v, want %q", got, binding.Address)
	}
	resources := append([]Resource(nil), bindings...)
	resources = append(resources, Resource{Address: "house/execution/process_scene_direct", Kind: "scenery.execution"})
	got := referencedExecutions(resources, bindings)
	if len(got) != 1 || got[0] != "house/execution/process_scene_direct" {
		t.Fatalf("MCP execution ownership = %#v, want execution address", got)
	}
}

func TestMCPFederationProjectionIsPrivateSymbolicAndDeterministic(t *testing.T) {
	server := Resource{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Spec: map[string]any{
		"max_input_bytes": 1024, "max_result_bytes": 2048,
		"capability": []any{
			map[string]any{"name": "zeta"}, map[string]any{"name": "alpha"},
		},
		"connection": []any{
			map[string]any{"connection": map[string]any{"$ref": "mcp_connection.z"}, "namespace": "z", "required": true},
			map[string]any{"connection": map[string]any{"$ref": "mcp_connection.a"}, "namespace": "a"},
		},
	}}
	sharedServer := Resource{Address: "app/mcp_server/shared", Kind: "scenery.mcp-server", Spec: map[string]any{
		"max_input_bytes": 1024, "max_result_bytes": 2048,
		"capability": []any{map[string]any{"name": "shared"}},
		"connection": []any{map[string]any{"connection": map[string]any{"$ref": "mcp_connection.a"}, "namespace": "shared"}},
	}}
	connectionA := Resource{Address: "app/mcp_connection/a", Kind: "scenery.mcp-connection", Spec: map[string]any{
		"url": "https://a.example.test/mcp", "connect_timeout": "2s", "call_timeout": "3s",
		"auth":  map[string]any{"scheme": "header", "header": "X-Remote-Key", "secret": map[string]any{"$ref": "secret.a"}},
		"tools": map[string]any{"allow": []any{"second", "first"}},
	}}
	connectionZ := Resource{Address: "app/mcp_connection/z", Kind: "scenery.mcp-connection", Spec: map[string]any{
		"url": "https://z.example.test/mcp", "connect_timeout": "4s", "call_timeout": "5s",
		"auth": map[string]any{"scheme": "none"}, "tools": map[string]any{"block": []any{"blocked"}},
	}}
	secret := Resource{Address: "app/secret/a", Kind: "scenery.secret", Spec: map[string]any{
		"store": map[string]any{"$ref": "secret_store.remote"}, "key": "REMOTE_TOKEN",
	}}
	store := Resource{Address: "app/secret_store/remote", Kind: "scenery.secret-store", Spec: map[string]any{}}
	assistantZ := Resource{Address: "app/assistant/z", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.support"}}}
	assistantA := Resource{Address: "app/assistant/a", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.support"}}}
	sharedAssistant := Resource{Address: "app/assistant/shared", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.shared"}}}
	result := &Result{Manifest: &Manifest{ContractRevision: "sha256:" + strings.Repeat("a", 64), Resources: []Resource{server, sharedServer, connectionZ, connectionA, secret, store, assistantZ, assistantA, sharedAssistant}}}
	targets, err := mcpFederationTargets(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Server.Address != "app/mcp_server/shared" || targets[1].Server.Address != "app/mcp_server/support" || !slices.Equal(targets[1].AssistantAddresses, []string{"app/assistant/a", "app/assistant/z"}) || !slices.Equal(targets[1].LocalToolNames, []string{"alpha", "zeta"}) {
		t.Fatalf("deterministic target ordering = %#v", targets)
	}
	if got := targets[1].Connections; len(got) != 2 || got[0].Address != "app/mcp_connection/a" || got[1].Address != "app/mcp_connection/z" {
		t.Fatalf("deterministic connection ordering = %#v", targets[1].Connections)
	}
	if got := targets[0].Connections; len(got) != 1 || got[0].Address != "app/mcp_connection/a" {
		t.Fatalf("shared connection projection = %#v", targets[0].Connections)
	}
	var generated strings.Builder
	if err := renderMCPFederationRegistrations(result, &generated); err != nil {
		t.Fatal(err)
	}
	source := generated.String()
	if strings.Count(source, "RegisterMCPFederationChecked") != 2 || strings.Count(source, "app/mcp_connection/a") != 3 {
		t.Fatalf("shared connection was not emitted once per server spec with one ownership claim:\n%s", source)
	}
	for _, want := range []string{"https://a.example.test/mcp", "AuthScheme: \"header\"", "AuthHeader: \"X-Remote-Key\"", "ResourceAddress: \"app/secret/a\"", "StoreAddress: \"app/secret_store/remote\"", "Key: \"REMOTE_TOKEN\""} {
		if !strings.Contains(source, want) {
			t.Fatalf("private federation projection missing %q:\n%s", want, source)
		}
	}
	for _, forbidden := range []string{"remote-secret", "Bearer ", "Authorization: \""} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("private federation projection leaked secret material %q:\n%s", forbidden, source)
		}
	}
}
