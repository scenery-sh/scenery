package spec

import (
	"slices"
	"testing"
)

func TestMCPAndAssistantResourceSchemas(t *testing.T) {
	tests := []struct {
		kind     string
		block    string
		revision string
		required []string
	}{
		{kind: "scenery.mcp-server", block: "mcp_server", revision: "contract", required: []string{"capability", "max_input_bytes", "max_result_bytes"}},
		{kind: "scenery.assistant", block: "assistant", revision: "contract", required: []string{"mcp_server", "implementation", "surface"}},
	}
	for _, test := range tests {
		schema, ok := ResourceSchemaForKind(test.kind)
		if !ok {
			t.Fatalf("resource schema %s is unavailable", test.kind)
		}
		if schema.RevisionDomain != test.revision {
			t.Errorf("%s revision domain = %q, want %q", test.kind, schema.RevisionDomain, test.revision)
		}
		for _, required := range test.required {
			if !containsString(schema.Required, required) {
				t.Errorf("%s required fields = %v, missing %q", test.kind, schema.Required, required)
			}
		}
		if _, ok := ResourceSourceSchema(test.block); !ok {
			t.Errorf("%s source schema is unavailable", test.block)
		}
	}
}

func TestMCPBindingSourceSchemaMetadata(t *testing.T) {
	binding, ok := ResourceSourceSchema("binding")
	if !ok {
		t.Fatal("binding source schema is unavailable")
	}
	mcp, ok := binding.Children["mcp"]
	if !ok || mcp.Repeatable || mcp.Schema.Labels != 0 {
		t.Fatalf("binding.mcp shape = %#v, want an unlabeled singleton", mcp)
	}
	for _, name := range []string{"name", "read_only", "destructive", "idempotent", "open_world"} {
		if !mcp.Schema.Required[name] {
			t.Errorf("binding.mcp %s must be required", name)
		}
	}
	for _, name := range []string{"title", "description"} {
		if mcp.Schema.Required[name] {
			t.Errorf("binding.mcp %s must remain optional metadata", name)
		}
	}
	if got := mcp.Schema.Attributes["allow_sensitive_output"].Default; got != false {
		t.Errorf("binding.mcp allow_sensitive_output default = %#v, want false", got)
	}
	if got := mcp.Schema.Attributes["allow_sensitive_output"].DefaultSource; got != "spec" {
		t.Errorf("binding.mcp allow_sensitive_output default source = %q, want spec", got)
	}
	protocol := AuthoredAttributeDefinition("scenery.source.binding", "protocol")
	if !AuthoredEnumAllows(protocol, "mcp") || AuthoredEnumAllows(protocol, "unsupported") {
		t.Fatalf("binding protocol enum = %#v", protocol.Constraints["enum"])
	}
	if got := mcp.Schema.Attributes["name"].Constraints["name_pattern"]; got != "^[a-z][a-z0-9_]{0,127}$" {
		t.Errorf("binding.mcp name pattern = %#v", got)
	}
}

func TestMCPServerAndAssistantChildren(t *testing.T) {
	server, _ := ResourceSourceSchema("mcp_server")
	capability := server.Children["capability"]
	if !capability.Repeatable || capability.Ordered || capability.Schema.Labels != 1 {
		t.Errorf("mcp_server capability shape = %#v", capability)
	}
	if got := capability.Schema.Attributes["binding"].Type["resource_ref"]; got != "scenery.binding" {
		t.Errorf("mcp_server capability.binding type = %#v", capability.Schema.Attributes["binding"].Type)
	}
	approval := AuthoredAttributeDefinition("scenery.mcp-server.capability", "approval")
	if values, ok := approval.Constraints["enum"].([]string); !ok || !sameStrings(values, []string{"always", "never"}) {
		t.Errorf("mcp_server capability.approval enum = %#v", approval.Constraints["enum"])
	}

	assistant, _ := ResourceSourceSchema("assistant")
	implementation := assistant.Children["implementation"]
	if implementation.Repeatable || implementation.Schema.Labels != 0 {
		t.Errorf("assistant implementation shape = %#v", implementation)
	}
	if got := implementation.Schema.Attributes["source"].Type["primitive"]; got != "relative_path" {
		t.Errorf("assistant implementation.source type = %#v", implementation.Schema.Attributes["source"].Type)
	}
	if got, ok := ResourceFieldRevisionDomain("scenery.assistant", "implementation"); !ok || got != "implementation" {
		t.Errorf("assistant implementation parent revision domain = %q, %t; want implementation", got, ok)
	}
	surface := assistant.Children["surface"]
	if surface.Repeatable || surface.Schema.Labels != 0 {
		t.Errorf("assistant surface shape = %#v", surface)
	}
	if got := surface.Schema.Attributes["path"].Type["primitive"]; got != "route_path" {
		t.Errorf("assistant surface.path type = %#v", surface.Schema.Attributes["path"].Type)
	}
	session := AuthoredAttributeDefinition("scenery.assistant.surface", "session_access")
	if values, ok := session.Constraints["enum"].([]string); !ok || !sameStrings(values, []string{"initiator"}) {
		t.Errorf("assistant surface.session_access enum = %#v", session.Constraints["enum"])
	}
}

func TestAssistantMCPDiagnostics(t *testing.T) {
	for _, code := range []string{"SCN2419", "SCN2420", "SCN2421", "SCN2422", "SCN2423", "SCN2424", "SCN2425", "SCN2426", "SCN2427", "SCN2430"} {
		definition, ok := DiagnosticDefinitionFor(code)
		if !ok {
			t.Errorf("diagnostic %s is missing", code)
		} else if definition.Category != "binding_and_cli" {
			t.Errorf("diagnostic %s category = %q, want binding_and_cli", code, definition.Category)
		}
	}
}

func containsString(values []string, want string) bool {
	return slices.Contains(values, want)
}

func sameStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for index := range got {
		if got[index] != want[index] {
			return false
		}
	}
	return true
}

func ResourceSourceSchema(blockType string) (*SourceBlockSchema, bool) {
	schema, ok := authoredResourceSourceSchema(blockType)
	if !ok {
		return nil, false
	}
	return cloneSourceBlockSchema(schema, map[*authoredBlockSchema]*authoredBlockSchema{}), true
}
