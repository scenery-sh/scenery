package spec

import "slices"

import "testing"

func TestMCPAndAssistantResourceSchemas(t *testing.T) {
	tests := []struct {
		kind     string
		block    string
		revision string
		required []string
	}{
		{kind: "scenery.mcp-connection", block: "mcp_connection", revision: "deployment", required: []string{"transport", "url", "auth", "connect_timeout", "call_timeout"}},
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

func TestMCPConnectionServerAndAssistantChildren(t *testing.T) {
	connection, _ := ResourceSourceSchema("mcp_connection")
	auth := connection.Children["auth"]
	if auth.Repeatable || auth.Schema.Labels != 0 || !auth.Schema.Required["scheme"] {
		t.Fatalf("mcp_connection auth shape = %#v", auth)
	}
	if got := auth.Schema.Attributes["secret"].Type["resource_ref"]; got != "scenery.secret" {
		t.Errorf("mcp_connection auth.secret type = %#v", auth.Schema.Attributes["secret"].Type)
	}
	tools := connection.Children["tools"]
	if tools.Repeatable || tools.Schema.Labels != 0 || tools.Schema.Required["allow"] || tools.Schema.Required["block"] {
		t.Fatalf("mcp_connection tools shape = %#v", tools)
	}
	if got := tools.Schema.Attributes["allow"].Type["collection"]; got != "set" {
		t.Errorf("mcp_connection tools.allow collection = %#v, want set", got)
	}
	if got := tools.Schema.Attributes["block"].Type["collection"]; got != "set" {
		t.Errorf("mcp_connection tools.block collection = %#v, want set", got)
	}
	if got := AuthoredRevisionDomain(tools.Schema.Revision, "allow"); got != "contract" {
		t.Errorf("mcp_connection tools.allow revision domain = %q, want contract", got)
	}
	if got, ok := ResourceFieldRevisionDomain("scenery.mcp-connection", "tools"); !ok || got != "contract" {
		t.Errorf("mcp_connection tools parent revision domain = %q, %t; want contract", got, ok)
	}
	scheme := AuthoredAttributeDefinition("scenery.mcp-connection.auth", "scheme")
	if values, ok := scheme.Constraints["enum"].([]string); !ok || !sameStrings(values, []string{"none", "bearer", "header"}) {
		t.Errorf("mcp_connection auth.scheme enum = %#v", scheme.Constraints["enum"])
	}

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

func TestAssistantMCPConditionalRequirementsAndDiagnostics(t *testing.T) {
	conditions := AuthoredConditionalRequirements()
	auth := conditions["scenery.mcp-connection.auth"]
	if len(auth) != 2 || !containsString(auth[0].Required, "secret") || !containsString(auth[1].Required, "header") {
		t.Fatalf("mcp auth conditional requirements = %#v", auth)
	}
	for _, code := range []string{"SCN2419", "SCN2420", "SCN2421", "SCN2422", "SCN2423", "SCN2424", "SCN2425", "SCN2426", "SCN2427", "SCN2428", "SCN2429", "SCN2430"} {
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
