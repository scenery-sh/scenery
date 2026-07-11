package vnext

import (
	"slices"
	"strings"
	"testing"
)

func TestAgentSchemasReportRuntimeExpressionPhases(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ revision, field string }{
		{"scenery.authorization.rule/v1", "allow"},
		{"scenery.authorization.rule/v1", "deny"},
		{"scenery.record.validation/v1", "when"},
		{"scenery.operation.idempotency/v1", "key"},
		{"scenery.execution.concurrency/v1", "key"},
		{"scenery.binding.http.context/v1", "from"},
		{"scenery.binding.http.response/v1", "when"},
		{"scenery.binding.http.response-body/v1", "from"},
		{"scenery.binding.cli.context/v1", "from"},
		{"scenery.binding.cli.output/v1", "from"},
		{"scenery.binding.cli.outcome/v1", "when"},
		{"scenery.binding.event.map/v1", "from"},
		{"scenery.binding.event/v1", "ordering_key"},
		{"scenery.event-emission.from/v1", "when"},
		{"scenery.event-emission.from/v1", "payload"},
		{"scenery.source.event_emission/v1", "deduplication_key"},
	} {
		t.Run(test.revision+"/"+test.field, func(t *testing.T) {
			field := agentSchemaField(t, test.revision, test.field)
			if field["phase"] != "runtime" {
				t.Fatalf("phase = %#v, want runtime", field["phase"])
			}
		})
	}
}

func TestAgentSchemaReportsOrderedCompositeIdempotencyKey(t *testing.T) {
	t.Parallel()
	field := agentSchemaField(t, "scenery.operation.idempotency/v1", "key")
	typeDefinition, _ := field["type"].(map[string]any)
	items, _ := typeDefinition["items"].(map[string]any)
	constraints, _ := field["constraints"].(map[string]any)
	schema, _ := AgentSchema("scenery.operation.idempotency/v1")
	conditional, _ := schema["conditional_requirements"].([]any)
	if field["phase"] != "runtime" || field["ordered"] != true || typeDefinition["collection"] != "list" || items["typed_reference"] != "schema_path" || constraints["min_items"] != 1 || constraints["reference_root"] != "input" || constraints["reference_shape"] != "direct_input_field" || len(conditional) != 1 {
		t.Fatalf("idempotency key metadata = %#v", field)
	}
}

func TestAuthoredFieldOverridesKeepCombinedMetadataTogether(t *testing.T) {
	t.Parallel()
	strategy := authoredAttributeDefinition("scenery.source.authorization/v1", "strategy")
	if strategy.Default != "deny_unless_allowed" || strategy.DefaultSource != "edition" || !slices.Equal(strategy.Constraints["enum"].([]string), []string{"all_must_allow", "allow_if_all", "allow_if_any", "any_allow", "deny_unless_allowed", "first_applicable"}) {
		t.Fatalf("authorization strategy metadata = %#v", strategy)
	}
	config := authoredAttributeDefinition("scenery.source.provider/v1", "config")
	if config.RevisionDomain != "implementation" || config.SensitivitySource != "provider_schema" {
		t.Fatalf("provider config metadata = %#v", config)
	}
	secret := authoredAttributeDefinition("scenery.deployment.secret/v1", "value")
	if !secret.Sensitive {
		t.Fatalf("deployment secret metadata = %#v", secret)
	}
	if domain := authoredRevisionDomain("scenery.source.service/v1", "implementation"); domain != "implementation" {
		t.Fatalf("service implementation revision domain = %q", domain)
	}
}

func TestAuthoredFieldOverrideCatalogTargetsKnownFields(t *testing.T) {
	t.Parallel()
	known := map[authoredFieldKey]bool{}
	seen := map[*authoredBlockSchema]bool{}
	var visit func(*authoredBlockSchema)
	visit = func(schema *authoredBlockSchema) {
		if schema == nil || seen[schema] {
			return
		}
		seen[schema] = true
		for name := range schema.Attributes {
			known[authoredFieldKey{Revision: schema.Revision, Name: name}] = true
		}
		for name, child := range schema.Children {
			known[authoredFieldKey{Revision: schema.Revision, Name: name}] = true
			visit(child.Schema)
		}
	}
	for kind, resource := range resourceSchemas {
		root, ok := authoredResourceSourceSchema(blockTypeForKind(kind))
		if !ok {
			continue
		}
		visit(root)
		for name := range resource.CanonicalOnly {
			known[authoredFieldKey{Revision: root.Revision, Name: name}] = true
		}
		for name := range dynamicResourceRevisionDomains[kind] {
			known[authoredFieldKey{Revision: root.Revision, Name: name}] = true
		}
	}
	for _, schema := range authoredStructuralSchemas {
		visit(schema)
	}
	for key := range authoredFieldOverrides {
		if key.Revision != "" && !known[key] {
			t.Errorf("authored field override targets unknown field %#v", key)
		}
	}
}

func TestAgentSchemasExposeCompilerEnums(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		revision, field string
		values          []string
	}{
		{"scenery.source.authorization/v1", "strategy", []string{"all_must_allow", "allow_if_all", "allow_if_any", "any_allow", "deny_unless_allowed", "first_applicable"}},
		{"scenery.source.execution/v1", "mode", []string{"direct", "durable", "workflow"}},
		{"scenery.execution.retry/v1", "strategy", []string{"exponential", "none"}},
		{"scenery.execution.deduplication/v1", "conflict", []string{"return_existing"}},
		{"scenery.source.binding/v1", "protocol", []string{"cli", "event", "http", "internal"}},
		{"scenery.source.binding/v1", "delivery", []string{"call", "enqueue", "stream", "wait"}},
		{"scenery.source.binding/v1", "exposure", []string{"application", "internet", "local", "package", "private_network"}},
		{"scenery.binding.internal/v1", "visibility", []string{"application", "package"}},
		{"scenery.binding.event/v1", "direction", []string{"consume"}},
		{"scenery.binding.event/v1", "guarantee", []string{"at_least_once", "at_most_once", "exactly_once"}},
		{"scenery.event.broker-retry/v1", "backoff", []string{"exponential", "fixed", "none"}},
		{"scenery.source.schedule/v1", "overlap", []string{"allow", "queue", "replace", "skip"}},
		{"scenery.entity.field-default/v1", "strategy", []string{"current_datetime", "provider", "uuid_v7"}},
		{"scenery.crud.execution/v1", "mode", []string{"direct", "durable"}},
		{"scenery.source.fixture/v1", "mode", []string{"insert", "replace", "upsert"}},
		{"scenery.source.typescript_client/v1", "module", []string{"esm"}},
		{"scenery.source.typescript_client/v1", "runtime", []string{"fetch"}},
		{"scenery.typescript-client.retry/v1", "policy", []string{"scenery.retry.idempotent/v1"}},
	} {
		t.Run(test.revision+"/"+test.field, func(t *testing.T) {
			field := agentSchemaField(t, test.revision, test.field)
			constraints, _ := field["constraints"].(map[string]any)
			values, _ := constraints["enum"].([]string)
			if !slices.Equal(values, test.values) {
				t.Fatalf("enum = %#v, want %#v", values, test.values)
			}
		})
	}
}

func TestResourceSchemaDefinitionsDriveAuthoredAndCanonicalFields(t *testing.T) {
	t.Parallel()
	for kind, schema := range resourceSchemas {
		blockType := blockTypeForKind(kind)
		authored, ok := authoredResourceSourceSchema(blockType)
		if !ok {
			t.Errorf("%s has no authored schema", kind)
			continue
		}
		allowed := resourceSchemaAllowedFields(kind)
		core, ok := CoreSchema(kind)
		if !ok {
			t.Errorf("%s has no public schema", kind)
			continue
		}
		if got, _ := core["allowed"].([]string); !slices.Equal(got, allowed) {
			t.Errorf("%s public allowed = %#v, want %#v", kind, got, allowed)
		}
		for name := range authored.Attributes {
			if !slices.Contains(allowed, name) {
				t.Errorf("%s authored attribute %s is not canonically allowed", kind, name)
			}
		}
		for name := range authored.Children {
			if !slices.Contains(allowed, name) {
				t.Errorf("%s authored block %s is not canonically allowed", kind, name)
			}
		}
		for _, name := range schema.Required {
			if !slices.Contains(allowed, name) {
				t.Errorf("%s required field %s is not allowed", kind, name)
			}
		}
		if schema.RevisionDomain == "" {
			t.Errorf("%s has no resource revision domain", kind)
		}
	}
}

func TestBindingConditionalSchemaRequiresHTTPGateway(t *testing.T) {
	t.Parallel()
	binding := Resource{Address: "app/binding/create", Kind: "scenery.binding/v1", Origin: Origin{Kind: "authored"}, Spec: map[string]any{
		"operation": map[string]any{"$ref": "app/operation/create"}, "execution": map[string]any{"$ref": "app/execution/create"},
		"protocol": "http", "delivery": "call", "authentication": map[string]any{"$ref": "std.authentication.none"},
		"authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
	}}
	diagnostics := validateResourceSchemas([]Resource{binding})
	if len(diagnostics) != 1 || diagnostics[0].Code != "SCN1009" || !strings.Contains(diagnostics[0].Message, "gateway when protocol is http") {
		t.Fatalf("missing-gateway diagnostics = %#v", diagnostics)
	}
	binding.Spec["gateway"] = map[string]any{"$ref": "app/http_gateway/public"}
	if diagnostics := validateResourceSchemas([]Resource{binding}); len(diagnostics) != 0 {
		t.Fatalf("valid binding diagnostics = %#v", diagnostics)
	}
}

func TestContractRevisionProjectionUsesSchemaDomains(t *testing.T) {
	t.Parallel()
	provider := Resource{Address: "app/provider/database", Module: "app", Name: "database", Kind: "scenery.provider/v1", Spec: map[string]any{
		"source": "registry.example/database", "version": "1.0.0", "locked_version": "1.0.0", "locked_integrity": "sha256:source",
		"compile_descriptor_digest": "sha256:descriptor", "runtime_abi": "runtime/v1", "deployment_abi": "deployment/v1",
		"config_schema": map[string]any{"database": "string"}, "capabilities": []any{"sql.query/v1"},
	}}
	base, err := contractRevision([]Resource{provider}, []string{"scenery.compiler-core/v1"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	runtimeChanged := provider
	runtimeChanged.Spec = cloneMapValue(provider.Spec)
	runtimeChanged.Spec["runtime_abi"] = "runtime/v2"
	runtimeRevision, _ := contractRevision([]Resource{runtimeChanged}, []string{"scenery.compiler-core/v1"}, "app")
	if runtimeRevision != base {
		t.Fatal("implementation-domain provider runtime ABI changed contract revision")
	}
	contractChanged := provider
	contractChanged.Spec = cloneMapValue(provider.Spec)
	contractChanged.Spec["config_schema"] = map[string]any{"database": "relative_path"}
	contractRevision, _ := contractRevision([]Resource{contractChanged}, []string{"scenery.compiler-core/v1"}, "app")
	if contractRevision == base {
		t.Fatal("contract-domain provider config schema did not change contract revision")
	}
}

func TestContractProjectionIncludesModuleContractFields(t *testing.T) {
	t.Parallel()
	module := Resource{Address: "app/module/house", Module: "app", Name: "house", Kind: "scenery.module/v1", Spec: map[string]any{
		"source": "./house", "workspace_package_root": "house", "package": map[string]any{"version": "1.0.0"},
		"exports": map[string]any{"service": map[string]any{"$ref": "house/service/house"}},
	}}
	projected, include := contractResourceProjection(module)
	if !include || projected.Spec["package"] == nil || projected.Spec["exports"] == nil {
		t.Fatalf("module contract projection = %#v, include=%v", projected.Spec, include)
	}
	if projected.Spec["source"] != nil || projected.Spec["workspace_package_root"] != nil {
		t.Fatalf("module projection retained workspace fields: %#v", projected.Spec)
	}
}

func TestServiceConfigProjectionUsesPackageInputDomains(t *testing.T) {
	t.Parallel()
	service := Resource{Address: "house/service/house", Module: "house", Name: "house", Kind: "scenery.service/v1", Spec: map[string]any{
		"runtime": "go", "implementation": map[string]any{"constructor": "NewService"},
		"config": map[string]any{"api_path": "/v1", "model_path": "models/roof.onnx", "token": "secret"},
		"config_schema": []any{
			map[string]any{"name": "api_path", "type": "string", "phase": "contract"},
			map[string]any{"name": "model_path", "type": "relative_path", "phase": "implementation"},
			map[string]any{"name": "token", "type": `resource_ref("secret")`, "phase": "deployment", "sensitive": true},
		},
	}}
	projected, include := contractResourceProjection(service)
	if !include {
		t.Fatal("service was omitted from contract projection")
	}
	config, _ := projected.Spec["config"].(map[string]any)
	descriptors := namedChildren(projected.Spec, "config_schema")
	if len(config) != 1 || config["api_path"] != "/v1" || len(descriptors) != 1 || descriptors[0]["name"] != "api_path" {
		t.Fatalf("service config projection = %#v", projected.Spec)
	}
}

func TestAuthoredEnumMetadataDrivesValidation(t *testing.T) {
	t.Parallel()
	schema, ok := authoredResourceSourceSchema("typescript_client")
	if !ok {
		t.Fatal("TypeScript client source schema unavailable")
	}
	block := &Block{Type: "typescript_client", Labels: []string{"public"}, Attributes: map[string]Expression{
		"gateways": {Kind: "literal", Value: []any{"public"}}, "package": {Kind: "literal", Value: "@example/public"},
		"module": {Kind: "literal", Value: "commonjs"}, "runtime": {Kind: "literal", Value: "fetch"}, "output_root": {Kind: "literal", Value: "gen"},
	}}
	if diagnostics := validateAuthoredBlock(block, schema); !hasDiagnostic(diagnostics, "SCN1020") {
		t.Fatalf("invalid metadata enum was not enforced: %#v", diagnostics)
	}
	block.Attributes["module"] = Expression{Kind: "literal", Value: "esm"}
	if diagnostics := validateAuthoredBlock(block, schema); hasErrors(diagnostics) {
		t.Fatalf("valid metadata enum was rejected: %#v", diagnostics)
	}
}

func TestRuntimeExpressionValidationUsesAuthoredPhaseMetadata(t *testing.T) {
	t.Parallel()
	runtime := &Source{Blocks: []*Block{{Type: "binding", Blocks: []*Block{{Type: "http", Blocks: []*Block{{Type: "response", Blocks: []*Block{{Type: "body", Attributes: map[string]Expression{
		"from": {Kind: "expression", Raw: `result.value`, Static: false},
	}}}}}}}}}}
	if diagnostics := validateStaticExpressions([]*Source{runtime}); hasErrors(diagnostics) {
		t.Fatalf("runtime-phase metadata field was rejected: %#v", diagnostics)
	}
	compile := &Source{Blocks: []*Block{{Type: "binding", Blocks: []*Block{{Type: "http", Attributes: map[string]Expression{
		"method": {Kind: "expression", Raw: `input.method`, Static: false},
	}}}}}}
	if diagnostics := validateStaticExpressions([]*Source{compile}); !hasDiagnostic(diagnostics, "SCN1010") {
		t.Fatalf("compile-phase metadata field was accepted: %#v", diagnostics)
	}
}

func agentSchemaField(t *testing.T, revision, name string) map[string]any {
	t.Helper()
	schema, ok := AgentSchema(revision)
	if !ok {
		t.Fatalf("schema %s is unavailable", revision)
	}
	fields, _ := schema["fields"].(map[string]any)
	field, _ := fields[name].(map[string]any)
	if field == nil {
		t.Fatalf("schema %s has no field %s", revision, name)
	}
	return field
}
