package generate

import (
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/mcpprojection"
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
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
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
	if err := renderMCPToolRegistrations(&source, Resource{Name: "house"}, []mcpToolTarget{target}, resources); err != nil {
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
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
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
	capabilityRevision, err := mcpprojection.CapabilityRevision(result.Manifest, "app/assistant/support", "app/mcp_server/support")
	if err != nil || capabilityRevision == result.Manifest.ContractRevision {
		t.Fatalf("assistant capability revision = %q, %v", capabilityRevision, err)
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
		`Access: sceneryruntime.Auth`,
		`BindingAddress: "app/assistant/support"`,
		`GatewayAddress: "app/http_gateway/public_api"`,
		`CORS: "none"`,
		`AuthorizationStrategy: "public"`,
		`PipelineSteps: []string{}`,
		`AssistantAddress: "app/assistant/support"`,
		`RuntimeRevision: "runtime-1"`,
		// The assistant's capability revision, not the application's.
		`CapabilityRevision: "` + capabilityRevision + `"`,
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
	for _, forbidden := range []string{"TokenManager:", "Client:", "secret-plaintext"} {
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
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
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
			// Keep the server, but remove local capabilities whose bindings
			// were removed above. This leaves a valid assistant manifest
			// without capabilities for projection.
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
	files, err := generateApplicationArtifacts(result, newResourceIndex(result.Manifest.Resources), newProjectionInput(result))
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
		`var RequiredAddresses = []string{"app/assistant/support", "app/mcp_server/support"}`,
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
	got := referencedExecutions(resourcesByAddress(&Manifest{Resources: resources}), bindings)
	if len(got) != 1 || got[0] != "house/execution/process_scene_direct" {
		t.Fatalf("MCP execution ownership = %#v, want execution address", got)
	}
}

func TestMCPFederationProjectionIsDeterministic(t *testing.T) {
	server := Resource{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Spec: map[string]any{
		"max_input_bytes": 1024, "max_result_bytes": 2048,
		"capability": []any{
			map[string]any{"name": "zeta"}, map[string]any{"name": "alpha"},
		},
	}}
	sharedServer := Resource{Address: "app/mcp_server/shared", Kind: "scenery.mcp-server", Spec: map[string]any{
		"max_input_bytes": 1024, "max_result_bytes": 2048,
		"capability": []any{map[string]any{"name": "shared"}},
	}}
	assistantZ := Resource{Address: "app/assistant/z", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.support"}}}
	assistantA := Resource{Address: "app/assistant/a", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.support"}}}
	sharedAssistant := Resource{Address: "app/assistant/shared", Kind: "scenery.assistant", Spec: map[string]any{"mcp_server": map[string]any{"$ref": "mcp_server.shared"}}}
	result := &Result{Manifest: &Manifest{ContractRevision: "sha256:" + strings.Repeat("a", 64), Resources: []Resource{server, sharedServer, assistantZ, assistantA, sharedAssistant}}}
	targets, err := mcpFederationTargets(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 2 || targets[0].Server.Address != "app/mcp_server/shared" || targets[1].Server.Address != "app/mcp_server/support" || !slices.Equal(targets[1].AssistantAddresses, []string{"app/assistant/a", "app/assistant/z"}) || !slices.Equal(targets[1].LocalToolNames, []string{"alpha", "zeta"}) {
		t.Fatalf("deterministic target ordering = %#v", targets)
	}
	var generated strings.Builder
	if err := renderMCPFederationRegistrations(result, &generated); err != nil {
		t.Fatal(err)
	}
	if source := generated.String(); strings.Count(source, "RegisterMCPFederationChecked") != 2 {
		t.Fatalf("federation was not emitted once per server:\n%s", source)
	}
}
