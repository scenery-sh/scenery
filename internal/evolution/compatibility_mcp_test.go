package evolution

import (
	"reflect"
	"testing"
)

func TestSemanticDiffClassifiesMCPToolNameAndEffectChanges(t *testing.T) {
	base := mcpBindingResource("process_scene", false)
	target := base
	target.Spec = cloneMap(base.Spec)
	mcp := cloneMap(target.Spec["mcp"].(map[string]any))
	mcp["destructive"] = true
	mcp["read_only"] = false
	target.Spec["mcp"] = mcp
	diff := CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})

	effect := findSemanticChange(t, diff, "/spec/mcp/process_scene/destructive")
	if got := effect.Classifications["runtime"].Result; got != CompatibilityBreaking {
		t.Fatalf("effect runtime = %q", got)
	}
	if len(effect.Evidence) != 1 {
		t.Fatalf("effect evidence = %#v", effect.Evidence)
	}
	evidence, ok := effect.Evidence[0].(map[string]any)
	if !ok || evidence["kind"] != "mcp_effect" {
		t.Fatalf("effect evidence = %#v", effect.Evidence)
	}
	mcp["name"] = "process_scene_v2"
	target.Spec["mcp"] = mcp
	diff = CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})
	name := findSemanticChange(t, diff, "/spec/mcp/process_scene")
	if got := name.Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("tool rename request wire = %q", got)
	}
	if !reflect.DeepEqual(name.AffectedArtifacts, []string{"mcp_capability_revision[*]"}) {
		t.Fatalf("tool rename consequences = %#v", name.AffectedArtifacts)
	}
}

func TestSemanticDiffClassifiesMCPServerCapabilityAddRemove(t *testing.T) {
	base := mcpServerResource([]any{map[string]any{"name": "search", "binding": map[string]any{"$ref": "binding.search"}, "approval": "never"}})
	added := mcpServerResource([]any{
		map[string]any{"name": "search", "binding": map[string]any{"$ref": "binding.search"}, "approval": "never"},
		map[string]any{"name": "fetch", "binding": map[string]any{"$ref": "binding.fetch"}, "approval": "always"},
	})
	diff := CompareManifests(manifestWith(base), manifestWith(added), CompareOptions{})
	change := findSemanticChange(t, diff, "/spec/capability/fetch")
	if got := change.Classifications["runtime"].Result; got != CompatibilityCompatible {
		t.Fatalf("capability add runtime = %q", got)
	}
	if !reflect.DeepEqual(change.AffectedArtifacts, []string{"mcp_capability_revision[*]"}) {
		t.Fatalf("capability add consequences = %#v", change.AffectedArtifacts)
	}
	diff = CompareManifests(manifestWith(added), manifestWith(base), CompareOptions{})
	change = findSemanticChange(t, diff, "/spec/capability/fetch")
	if got := change.Classifications["runtime"].Result; got != CompatibilityBreaking {
		t.Fatalf("capability remove runtime = %q", got)
	}
}

func TestSemanticDiffClassifiesAssistantSurfaceAndImplementationSeparately(t *testing.T) {
	base := assistantResource("/assistants/support", "./assistants/support", "./assistants/support/package.json")
	target := base
	target.Spec = cloneMap(base.Spec)
	target.Spec["implementation"] = map[string]any{
		"adapter": "eve", "source": "./assistants/support-v2", "package": "./assistants/support/package.json", "package_lock": "./assistants/support/package-lock.json",
	}
	targetSurface := cloneMap(target.Spec["surface"].(map[string]any))
	targetSurface["path"] = "/assistants/help"
	target.Spec["surface"] = targetSurface
	diff := CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})
	implementation := findSemanticChange(t, diff, "/spec/implementation/source")
	if got := implementation.Classifications["source"].Result; got != CompatibilityCompatible {
		t.Fatalf("implementation source source = %q", got)
	}
	if got := implementation.Classifications["runtime"].Result; got != CompatibilityMigrationRequired {
		t.Fatalf("implementation source runtime = %q", got)
	}
	if !reflect.DeepEqual(implementation.AffectedArtifacts, []string{"implementation_revision[*]"}) {
		t.Fatalf("implementation consequences = %#v", implementation.AffectedArtifacts)
	}
	surface := findSemanticChange(t, diff, "/spec/surface/path")
	if got := surface.Classifications["request_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("surface route request wire = %q", got)
	}
	if !reflect.DeepEqual(surface.AffectedArtifacts, []string{
		"assistant_public_revision[support]", "http_surface_revision[public_api]", "openapi_revision[public_api]", "typescript_client_revision[public_api]",
	}) {
		t.Fatalf("surface consequences = %#v", surface.AffectedArtifacts)
	}
}

func TestSemanticDiffClassifiesMCPConnectionReadinessChanges(t *testing.T) {
	base := mcpConnectionResource("https://docs.example.test/mcp", "none")
	target := base
	target.Spec = cloneMap(base.Spec)
	target.Spec["url"] = "https://docs.example.test/v2/mcp"
	diff := CompareManifests(manifestWith(base), manifestWith(target), CompareOptions{})
	change := findSemanticChange(t, diff, "/spec/url")
	if got := change.Classifications["runtime"].Result; got != CompatibilityMigrationRequired {
		t.Fatalf("connection URL runtime = %q", got)
	}
	if got := change.Classifications["deployment"].Result; got != CompatibilityMigrationRequired {
		t.Fatalf("connection URL deployment = %q", got)
	}
	if !reflect.DeepEqual(change.AffectedArtifacts, []string{"assistant_readiness[*]", "implementation_revision[*]"}) {
		t.Fatalf("connection URL consequences = %#v", change.AffectedArtifacts)
	}
	if len(change.Evidence) != 1 {
		t.Fatalf("connection URL evidence = %#v", change.Evidence)
	}
}

func TestSemanticDiffUsesDirectionalTypeRulesForMCPReachableRecords(t *testing.T) {
	inputBase := Resource{Address: "house/record/input", Kind: "scenery.record", Name: "input", Module: "house", Spec: map[string]any{
		"field": map[string]any{"name": "value", "type": map[string]any{"$ref": "string"}},
	}}
	inputTarget := inputBase
	inputTarget.Spec = map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$expression": "optional(string)"}}}
	outputBase := Resource{Address: "house/record/output", Kind: "scenery.record", Name: "output", Module: "house", Spec: map[string]any{
		"field": map[string]any{"name": "value", "type": map[string]any{"$ref": "string"}},
	}}
	outputTarget := outputBase
	outputTarget.Spec = map[string]any{"field": map[string]any{"name": "value", "type": map[string]any{"$expression": "optional(string)"}}}
	operation := Resource{Address: "house/operation/use", Kind: "scenery.operation", Name: "use", Module: "house", Spec: map[string]any{
		"input":  map[string]any{"$ref": "record.input"},
		"result": map[string]any{"name": "ok", "type": map[string]any{"$ref": "record.output"}},
	}}
	binding := mcpBindingResource("use", false)
	binding.Spec["operation"] = map[string]any{"$ref": "operation.use"}
	base := manifestWith(inputBase, outputBase, operation, binding)
	target := manifestWith(inputTarget, outputTarget, operation, binding)
	diff := CompareManifests(base, target, CompareOptions{})
	inputChange := findSemanticChangeContaining(t, diff, "/spec/field/value/type")
	if got := inputChange.Classifications["request_wire"].Result; got != CompatibilityCompatible {
		t.Fatalf("MCP input contravariance = %q", got)
	}
	// The same field path appears in both records; locate the output change by
	// its resource address to prove response covariance independently.
	var outputChange SemanticChange
	for _, change := range diff.Changes {
		if change.Address == outputBase.Address && change.Path == "/spec/field/value/type" {
			outputChange = change
			break
		}
	}
	if outputChange.Address == "" {
		t.Fatalf("MCP output type change not found: %#v", diff.Changes)
	}
	if got := outputChange.Classifications["response_wire"].Result; got != CompatibilityBreaking {
		t.Fatalf("MCP output covariance = %q", got)
	}
}

func mcpBindingResource(name string, destructive bool) Resource {
	return Resource{Address: "house/binding/" + name, Kind: "scenery.binding", Name: name, Module: "house", Spec: map[string]any{
		"protocol": "mcp",
		"delivery": "call",
		"mcp": map[string]any{
			"name": name, "title": "Process", "description": "Process one scene", "read_only": !destructive, "destructive": destructive, "idempotent": false, "open_world": false,
		},
	}}
}

func mcpServerResource(capabilities []any) Resource {
	return Resource{Address: "app/mcp_server/support", Kind: "scenery.mcp-server", Name: "support", Module: "app", Spec: map[string]any{
		"capability": capabilities, "max_input_bytes": 262144, "max_result_bytes": 1048576,
	}}
}

func assistantResource(path, source, packagePath string) Resource {
	return Resource{Address: "app/assistant/support", Kind: "scenery.assistant", Name: "support", Module: "app", Spec: map[string]any{
		"mcp_server":     map[string]any{"$ref": "mcp_server.support"},
		"implementation": map[string]any{"adapter": "eve", "source": source, "package": packagePath, "package_lock": "./assistants/support/package-lock.json"},
		"surface":        map[string]any{"gateway": map[string]any{"$ref": "http_gateway.public_api"}, "path": path, "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}, "session_access": "initiator", "client": map[string]any{"$ref": "typescript_client.public_api"}},
	}}
}

func mcpConnectionResource(url, scheme string) Resource {
	return Resource{Address: "app/mcp_connection/docs", Kind: "scenery.mcp-connection", Name: "docs", Module: "app", Spec: map[string]any{
		"transport": "streamable_http", "url": url, "auth": map[string]any{"scheme": scheme}, "tools": map[string]any{"allow": []any{"search", "fetch"}}, "connect_timeout": "5s", "call_timeout": "30s",
	}}
}

func cloneMap(value map[string]any) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		if nested, ok := item.(map[string]any); ok {
			result[key] = cloneMap(nested)
			continue
		}
		result[key] = item
	}
	return result
}
