package contractagent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestAgentSchemaGetCoversEveryMutationAndDiagnosticValue(t *testing.T) {
	for _, name := range []string{
		"scenery.value", "scenery.diagnostic", "scenery.semantic-operation", DiagnosticCatalog, "SCN8001",
		"resource.create", "resource.delete", "resource.rename", "value.set", "value.unset", "module.configure",
	} {
		t.Run(name, func(t *testing.T) {
			schema, ok := AgentSchema(name)
			if !ok {
				t.Fatalf("schema %s is unavailable", name)
			}
			encoded, _ := json.Marshal(schema)
			if !json.Valid(encoded) || !containsJSONText(encoded, "schema_revision") {
				t.Fatalf("schema = %s", encoded)
			}
		})
	}
}

func TestAgentSchemaDescribesStructuredResourceAuthoring(t *testing.T) {
	schema, ok := AgentSchema("scenery.operation")
	if !ok {
		t.Fatal("operation schema is unavailable")
	}
	fields, _ := schema["fields"].(map[string]any)
	service, _ := fields["service"].(map[string]any)
	serviceType, _ := service["type"].(map[string]any)
	resultField, _ := fields["result"].(map[string]any)
	cardinality, _ := resultField["cardinality"].(map[string]any)
	if schema["source_shape"] != "labeled_block" || schema["labels"] != 1 || schema["label_source"] != "address" ||
		service["source_shape"] != "attribute" || serviceType["resource_ref"] != "scenery.service" || service["phase"] != "compile" ||
		service["revision_domain"] != "contract" || service["required"] != false || service["sensitive"] != false ||
		service["ordered"] != false || service["constraints"] == nil ||
		resultField["source_shape"] != "repeated_labeled_block" || resultField["labels"] != 1 || resultField["label_source"] != "name" || resultField["ordered"] != false ||
		!isCanonicalSHA256Digest(stringValue(resultField["child_schema"])) || cardinality["minimum"] != 0 || cardinality["maximum"] != nil {
		t.Fatalf("operation authoring schema = %#v", schema)
	}

	serviceSchema, _ := CoreSchema("scenery.service")
	serviceFields, _ := serviceSchema["fields"].(map[string]any)
	serviceConfig, _ := serviceFields["config"].(map[string]any)
	config, _ := AgentSchema(stringValue(serviceConfig["child_schema"]))
	dynamic, _ := config["dynamic_attributes"].(map[string]any)
	if config["additional_properties"] != true || dynamic["name_pattern"] != "^[a-z][a-z0-9_]*$" || dynamic["phase"] != "compile" || dynamic["input_phase_source"] != "package_input" || dynamic["revision_domain_source"] != "package_input" || dynamic["sensitivity_source"] != "package_input" {
		t.Fatalf("service config schema = %#v", config)
	}
	authorization, _ := CoreSchema("scenery.authorization")
	authorizationFields, _ := authorization["fields"].(map[string]any)
	rule, _ := authorizationFields["rule"].(map[string]any)
	if rule["ordered"] != true {
		t.Fatalf("authorization rule ordering = %#v", rule)
	}
	binding, _ := CoreSchema("scenery.binding")
	bindingFields, _ := binding["fields"].(map[string]any)
	for _, name := range []string{"operation", "execution", "protocol", "delivery", "authentication", "authorization", "pipeline"} {
		field, _ := bindingFields[name].(map[string]any)
		if field["required"] != true {
			t.Errorf("binding field %s required = %#v", name, field["required"])
		}
	}
	gateway, _ := bindingFields["gateway"].(map[string]any)
	mcp, _ := bindingFields["mcp"].(map[string]any)
	conditional, _ := binding["conditional_requirements"].([]any)
	if gateway["required"] != false || mcp["required"] != false || len(conditional) != 2 {
		t.Fatalf("binding conditional authoring schema = %#v", binding)
	}
	wants := map[string]string{"http": "gateway", "mcp": "mcp"}
	for _, item := range conditional {
		requirement, _ := item.(map[string]any)
		when, _ := requirement["when"].(map[string]any)
		required, _ := requirement["required"].([]string)
		equals, _ := when["equals"].(string)
		if when["field"] != "protocol" || len(required) != 1 || required[0] != wants[equals] {
			t.Fatalf("binding conditional requirement = %#v", requirement)
		}
		delete(wants, equals)
	}
	if len(wants) != 0 {
		t.Fatalf("missing binding conditional requirements %#v", wants)
	}
	sourceBinding, _ := AgentSchema(sourceSchemaRevisionForInternalName("scenery.source.binding"))
	sourceConditional, _ := sourceBinding["conditional_requirements"].([]any)
	if len(sourceConditional) != 2 {
		t.Fatalf("source binding conditional requirement = %#v", sourceBinding)
	}
}

func TestAdvertisedResourceCreateSchemasHaveCompleteMetadata(t *testing.T) {
	advertised := resourceCreateSchemaRevisions()
	if len(advertised) != len(resourceSchemas) {
		t.Errorf("resource.create schema coverage = %d/%d; advertised=%v", len(advertised), len(resourceSchemas), advertised)
	}
	for _, required := range []string{"scenery.authentication", "scenery.binding", "scenery.operation", "scenery.record", "scenery.service"} {
		if !containsExactString(advertised, required) {
			t.Errorf("resource.create does not advertise required kind %s; advertised=%v", required, advertised)
		}
	}
	allowedDomains := map[string]bool{"contract": true, "implementation": true, "deployment": true, "workspace_only": true}
	seen := map[string]bool{}
	var inspect func(string)
	inspect = func(revision string) {
		if seen[revision] {
			return
		}
		seen[revision] = true
		schema, ok := AgentSchema(revision)
		if !ok {
			t.Errorf("schema.get missing %s", revision)
			return
		}
		if gaps, _ := schema["metadata_gaps"].([]string); len(gaps) != 0 {
			t.Errorf("schema %s metadata gaps = %v", revision, gaps)
		}
		fields, _ := schema["fields"].(map[string]any)
		for name, raw := range fields {
			field, _ := raw.(map[string]any)
			if field["metadata_status"] == "generic" {
				t.Errorf("schema %s field %s is generic", revision, name)
			}
			if domain, _ := field["revision_domain"].(string); !allowedDomains[domain] && field["revision_domain_source"] == nil {
				t.Errorf("schema %s field %s revision domain = %q without a dynamic source", revision, name, domain)
			}
			if child, _ := field["child_schema"].(string); child != "" {
				inspect(child)
			}
		}
	}
	for _, revision := range advertised {
		inspect(revision)
	}
}

func TestAgentMutationSchemasRequireOperationInputs(t *testing.T) {
	tests := map[string][]string{
		"resource.create": {"op", "address", "value"}, "resource.delete": {"op", "address"}, "resource.rename": {"op", "address", "value"},
		"value.set": {"op", "address", "path", "value"}, "value.unset": {"op", "address", "path"},
		"module.configure": {"op", "address", "value"},
	}
	for operation, required := range tests {
		schema, ok := AgentSchema(operation)
		if !ok {
			t.Fatalf("missing schema %s", operation)
		}
		got, _ := schema["required"].([]string)
		if strings.Join(got, ",") != strings.Join(required, ",") {
			t.Errorf("%s required = %v, want %v", operation, got, required)
		}
		properties, _ := schema["properties"].(map[string]any)
		_, hasPath := properties["path"]
		_, hasValue := properties["value"]
		if operation == "resource.delete" && (hasPath || hasValue) || operation == "value.unset" && hasValue {
			t.Errorf("%s properties = %#v", operation, properties)
		}
	}
}

func containsJSONText(value []byte, want string) bool {
	for index := 0; index+len(want) <= len(value); index++ {
		if string(value[index:index+len(want)]) == want {
			return true
		}
	}
	return false
}
