package vnext

import "strings"

type authoredAttributeSchema struct {
	Type              map[string]any
	Phase             string
	InputPhaseSource  string
	RevisionDomain    string
	RevisionSource    string
	Sensitive         bool
	SensitivitySource string
	Patchable         bool
	Ordered           bool
	Default           any
	Constraints       map[string]any
	MetadataStatus    string
	DefaultSource     string
	UnsupportedDraft  string
}

type authoredFieldKey struct {
	Revision string
	Name     string
}

type authoredFieldOverride struct {
	Phase             string
	RevisionDomain    string
	Sensitive         bool
	SensitivitySource string
	Ordered           bool
	Default           any
	DefaultSource     string
	Constraints       map[string]any
	UnsupportedDraft  string
}

var authoredFieldOverrides = map[authoredFieldKey]authoredFieldOverride{
	{Name: "config"}: {SensitivitySource: "provider_schema"},

	{Revision: "scenery.authorization.rule/v1", Name: "allow"}:                           {Phase: "runtime"},
	{Revision: "scenery.authorization.rule/v1", Name: "deny"}:                            {Phase: "runtime"},
	{Revision: "scenery.record.validation/v1", Name: "when"}:                             {Phase: "runtime"},
	{Revision: "scenery.operation.idempotency/v1", Name: "key"}:                          {Phase: "runtime", Ordered: true, Constraints: map[string]any{"min_items": 1, "reference_root": "input", "reference_shape": "direct_input_field"}},
	{Revision: "scenery.execution.concurrency/v1", Name: "key"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.context/v1", Name: "from"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.response/v1", Name: "when"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-body/v1", Name: "from"}:                    {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-header/v1", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.cli.context/v1", Name: "from"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.cli.output/v1", Name: "from"}:                            {Phase: "runtime"},
	{Revision: "scenery.binding.cli.outcome/v1", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.event.map/v1", Name: "from"}:                             {Phase: "runtime"},
	{Revision: "scenery.binding.event/v1", Name: "ordering_key"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.event/v1", Name: "deduplication_key"}:                    {Phase: "runtime"},
	{Revision: "scenery.source.event_emission/v1", Name: "ordering_key"}:                 {Phase: "runtime"},
	{Revision: "scenery.source.event_emission/v1", Name: "deduplication_key"}:            {Phase: "runtime"},
	{Revision: "scenery.event-emission.from/v1", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.event-emission.from/v1", Name: "payload"}:                        {Phase: "runtime"},
	{Revision: "scenery.source.authorization/v1", Name: "strategy"}:                      {Default: "deny_unless_allowed", DefaultSource: "edition", Constraints: enumConstraint("all_must_allow", "allow_if_all", "allow_if_any", "any_allow", "deny_unless_allowed", "first_applicable")},
	{Revision: "scenery.source.record/v1", Name: "unknown_fields"}:                       {Default: "reject", DefaultSource: "edition", Constraints: enumConstraint("preserve", "reject")},
	{Revision: "scenery.source.enum/v1", Name: "open"}:                                   {Default: false, DefaultSource: "edition"},
	{Revision: "scenery.source.union/v1", Name: "open"}:                                  {Default: false, DefaultSource: "edition"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "path"}:                  {Default: "/", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "max_age"}:               {Default: 0, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "secure"}:                {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "http_only"}:             {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie/v1", Name: "same_site"}:             {Default: "lax", DefaultSource: "http_profile", Constraints: enumConstraint("lax", "none", "strict")},
	{Revision: "scenery.binding.http/v1", Name: "guarantee"}:                             {Default: "framework_enforced", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.cli/v1", Name: "command"}:                                {Ordered: true},
	{Revision: "scenery.deployment.http-listener/v1", Name: "http_versions"}:             {Ordered: true},
	{Revision: "scenery.source.typescript_client/v1", Name: "module"}:                    {Constraints: enumConstraint("esm")},
	{Revision: "scenery.source.typescript_client/v1", Name: "runtime"}:                   {Constraints: enumConstraint("fetch")},
	{Revision: "scenery.typescript-client.retry/v1", Name: "policy"}:                     {Constraints: enumConstraint("scenery.retry.idempotent/v1")},
	{Revision: "scenery.typescript-client.retry/v1", Name: "maximum_attempts"}:           {Constraints: map[string]any{"minimum": 2, "maximum": 10}},
	{Revision: "scenery.typescript-client.retry/v1", Name: "maximum_delay_milliseconds"}: {Constraints: map[string]any{"maximum": 86_400_000}},
	{Revision: "scenery.typescript-client.retry/v1", Name: "statuses"}:                   {Constraints: map[string]any{"item_minimum": 400, "item_maximum": 599, "unique_items": true}},
	{Revision: "scenery.operation.idempotency/v1", Name: "mode"}:                         {Constraints: enumConstraint("keyed", "none")},
	{Revision: "scenery.source.execution/v1", Name: "mode"}:                              {Constraints: enumConstraint("direct", "durable", "workflow")},
	{Revision: "scenery.execution.retry/v1", Name: "strategy"}:                           {Constraints: enumConstraint("exponential", "none")},
	{Revision: "scenery.execution.deduplication/v1", Name: "conflict"}:                   {Constraints: enumConstraint("return_existing")},
	{Revision: "scenery.source.binding/v1", Name: "protocol"}:                            {Constraints: enumConstraint("cli", "event", "http", "internal")},
	{Revision: "scenery.source.binding/v1", Name: "delivery"}:                            {Constraints: enumConstraint("call", "enqueue", "stream", "wait")},
	{Revision: "scenery.source.binding/v1", Name: "exposure"}:                            {Constraints: enumConstraint("application", "internet", "local", "package", "private_network")},
	{Revision: "scenery.binding.internal/v1", Name: "visibility"}:                        {Constraints: enumConstraint("application", "package")},
	{Revision: "scenery.binding.internal/v1", Name: "principal"}:                         {Constraints: enumConstraint("inherit")},
	{Revision: "scenery.binding.event/v1", Name: "direction"}:                            {Constraints: enumConstraint("consume")},
	{Revision: "scenery.binding.event/v1", Name: "guarantee"}:                            {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.source.event_emission/v1", Name: "guarantee"}:                    {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.event.broker-retry/v1", Name: "backoff"}:                         {Constraints: enumConstraint("exponential", "fixed", "none")},
	{Revision: "scenery.source.schedule/v1", Name: "overlap"}:                            {Constraints: enumConstraint("allow", "queue", "replace", "skip")},
	{Revision: "scenery.entity.field-default/v1", Name: "strategy"}:                      {Constraints: enumConstraint("current_datetime", "provider", "uuid_v7")},
	{Revision: "scenery.crud.execution/v1", Name: "mode"}:                                {Constraints: enumConstraint("direct", "durable")},
	{Revision: "scenery.source.fixture/v1", Name: "mode"}:                                {Constraints: enumConstraint("insert", "replace", "upsert")},
	{Revision: "scenery.deployment.secret/v1", Name: "value"}:                            {Sensitive: true},
	{Revision: "scenery.deployment.http-listener/v1", Name: "certificate"}:               {UnsupportedDraft: "platform_listener_and_certificate_schemas"},
	{Revision: "scenery.deployment.http-listener/v1", Name: "platform_identity"}:         {UnsupportedDraft: "platform_listener_and_certificate_schemas"},

	{Revision: "scenery.source.service/v1", Name: "implementation"}:         {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service/v1", Name: "lifecycle"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service/v1", Name: "config"}:                 {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service/v1", Name: "config_schema"}:          {RevisionDomain: "implementation"},
	{Revision: "scenery.source.operation/v1", Name: "handler"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider/v1", Name: "source"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider/v1", Name: "version"}:               {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider/v1", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.data_source/v1", Name: "config"}:             {RevisionDomain: "implementation"},
	{Revision: "scenery.source.execution_engine/v1", Name: "config"}:        {RevisionDomain: "implementation"},
	{Revision: "scenery.source.event_bus/v1", Name: "config"}:               {RevisionDomain: "implementation"},
	{Revision: "scenery.source.secret_store/v1", Name: "config"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view/v1", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view/v1", Name: "implementation_digest"}:     {RevisionDomain: "implementation"},
	{Revision: "scenery.source.crud/v1", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer/v1", Name: "module"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer/v1", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer/v1", Name: "implementation_digest"}: {RevisionDomain: "implementation"},
}

func authoredAttributeDefinition(revision, name string) authoredAttributeSchema {
	typeDefinition, status := authoredAttributeType(revision, name)
	definition := authoredAttributeSchema{
		Type: typeDefinition, Phase: "compile", RevisionDomain: authoredRevisionDomain(revision, name), Patchable: true,
		DefaultSource: "none", Constraints: authoredPrimitiveConstraints(typeDefinition), MetadataStatus: status,
	}
	applyAuthoredFieldOverride(&definition, authoredFieldOverrides[authoredFieldKey{Name: name}])
	applyAuthoredFieldOverride(&definition, authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}])
	return definition
}

func enumConstraint(values ...string) map[string]any {
	return map[string]any{"enum": values}
}

func authoredPrimitiveConstraints(typeDefinition map[string]any) map[string]any {
	constraints := map[string]any{}
	switch typeDefinition["primitive"] {
	case "non_negative_int":
		constraints["minimum"] = 0
	case "positive_int":
		constraints["minimum"] = 1
	case "tcp_port":
		constraints["minimum"], constraints["maximum"] = 1, 65535
	case "http_status":
		constraints["minimum"], constraints["maximum"] = 100, 599
	case "relative_path":
		constraints["format"] = "normalized_relative_path"
	case "route_path":
		constraints["format"] = "absolute_normalized_route"
	case "json_pointer":
		constraints["format"] = "json_pointer"
	case "version_constraint":
		constraints["format"] = "semantic_version_constraint"
	}
	return constraints
}

func applyAuthoredFieldOverride(definition *authoredAttributeSchema, override authoredFieldOverride) {
	if override.Phase != "" {
		definition.Phase = override.Phase
	}
	if override.RevisionDomain != "" {
		definition.RevisionDomain = override.RevisionDomain
	}
	if override.DefaultSource != "" {
		definition.Default = override.Default
		definition.DefaultSource = override.DefaultSource
	}
	if override.Sensitive {
		definition.Sensitive = true
	}
	if override.SensitivitySource != "" {
		definition.SensitivitySource = override.SensitivitySource
	}
	if override.Ordered {
		definition.Ordered = true
	}
	if override.UnsupportedDraft != "" {
		definition.UnsupportedDraft = override.UnsupportedDraft
	}
	for name, value := range override.Constraints {
		definition.Constraints[name] = value
	}
}

func authoredAttributeType(revision, name string) (map[string]any, string) {
	resourceRef := func(kind string) (map[string]any, string) {
		return map[string]any{"resource_ref": "scenery." + strings.ReplaceAll(kind, "_", "-") + "/v1"}, "exact"
	}
	typeExpression := func() (map[string]any, string) { return map[string]any{"type_expression": "scenery.type/v1"}, "exact" }
	typedReference := func() (map[string]any, string) { return map[string]any{"typed_reference": "schema_path"}, "exact" }
	primitive := func(kind string) (map[string]any, string) { return map[string]any{"primitive": kind}, "exact" }
	list := func(item string) (map[string]any, string) {
		return map[string]any{"collection": "list", "items": map[string]any{"primitive": item}}, "exact"
	}
	object := func(schema string) (map[string]any, string) { return map[string]any{"object": schema}, "exact" }

	switch revision {
	case "scenery.source.go_module/v1":
		return primitive("relative_path_or_import_path")
	case "scenery.source.go_toolchain/v1":
		if name == "experiments" {
			return list("string")
		}
		return primitive("version")
	case "scenery.source.go_target/v1":
		switch name {
		case "toolchain":
			return resourceRef("go_toolchain")
		case "module":
			return resourceRef("go_module")
		case "packages", "build_tags", "go_flags", "architecture_features", "native_input", "native_inputs":
			return list("string")
		case "environment":
			return object("string_map")
		case "verify_by_default":
			return primitive("bool")
		case "extends", "inherits":
			return resourceRef("go_target")
		default:
			return primitive("string")
		}
	case "scenery.go-target.test/v1":
		return list("string")
	case "scenery.source.http_gateway/v1":
		switch name {
		case "cors":
			return map[string]any{"resource_ref": "std.cors"}, "exact"
		case "trusted_proxies":
			return map[string]any{"resource_ref": "std.trusted_proxies"}, "exact"
		case "forwarded":
			return map[string]any{"resource_ref": "std.forwarded_headers"}, "exact"
		case "request_limit", "response_limit", "timeouts":
			return object("scenery.http-effective-policy/v1")
		default:
			return primitive("string")
		}
	case "scenery.source.authentication/v1":
		switch name {
		case "provider":
			return resourceRef("provider")
		case "scheme":
			return primitive("string")
		case "config":
			return object("provider_config")
		}
	case "scenery.source.authorization/v1":
		if name == "principal" {
			return typeExpression()
		}
		return primitive("string")
	case "scenery.authorization.rule/v1":
		return map[string]any{"expression": "typed_predicate"}, "exact"
	case "scenery.source.workload_identity/v1":
		switch name {
		case "issuer":
			return map[string]any{"resource_ref": "std.identity_issuer"}, "exact"
		case "principal_type":
			return typeExpression()
		case "claims":
			return object("typed_claim_map")
		}
	case "scenery.pipeline.step/v1":
		return map[string]any{"resource_ref": "scenery.middleware/v1", "standard_library_allowed": true}, "exact"
	case "scenery.source.provider/v1":
		switch name {
		case "source":
			return primitive("registry_source")
		case "version":
			return primitive("version_constraint")
		case "config":
			return object("provider_config")
		default:
			return map[string]any{"canonical_only": true}, "exact"
		}
	case "scenery.source.data_source/v1", "scenery.source.execution_engine/v1", "scenery.source.event_bus/v1", "scenery.source.secret_store/v1":
		switch name {
		case "provider":
			return resourceRef("provider")
		case "require_capabilities":
			return list("string")
		case "config":
			return object("provider_config")
		default:
			return primitive("string")
		}
	case "scenery.source.secret/v1":
		if name == "store" {
			return resourceRef("secret_store")
		}
		return primitive("string")
	case "scenery.source.deployment/v1":
		if name == "fixture_policy" {
			return object("scenery.fixture-policy/v1")
		}
		return primitive("string")
	case "scenery.deployment.module/v1":
		if name == "target" {
			return resourceRef("module")
		}
		return object("typed_module_inputs")
	case "scenery.deployment.data-source/v1":
		if name == "target" {
			return resourceRef("data_source")
		}
		return object("provider_config")
	case "scenery.deployment.service/v1":
		switch name {
		case "target":
			return resourceRef("service")
		case "replicas":
			return primitive("positive_int")
		case "placement", "config":
			return object("deployment_value")
		}
	case "scenery.deployment.resources/v1":
		return primitive("resource_quantity")
	case "scenery.deployment.http-gateway/v1":
		return resourceRef("http_gateway")
	case "scenery.deployment.http-listener/v1":
		switch name {
		case "port":
			return primitive("tcp_port")
		case "secret":
			return resourceRef("secret")
		case "http_versions":
			return list("string")
		default:
			return primitive("string")
		}
	case "scenery.deployment.provider/v1":
		if name == "target" {
			return resourceRef("provider")
		}
		return object("provider_config")
	case "scenery.deployment.secret/v1":
		switch name {
		case "target":
			return resourceRef("secret")
		case "store":
			return resourceRef("secret_store")
		case "value":
			return map[string]any{"secret_value": true}, "exact"
		default:
			return primitive("string")
		}
	case "scenery.source.typescript_client/v1":
		switch name {
		case "gateways":
			return map[string]any{"collection": "set", "items": map[string]any{"resource_ref": "scenery.http-gateway/v1"}}, "exact"
		case "include":
			return list("string")
		case "output_root":
			return primitive("relative_path")
		default:
			return primitive("string")
		}
	case "scenery.typescript-client.retry/v1":
		switch name {
		case "maximum_attempts", "maximum_delay_milliseconds":
			return primitive("non_negative_int")
		case "statuses":
			return list("http_status")
		default:
			return primitive("string")
		}
	case "scenery.source.patch/v1":
		if name == "target" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.patch.operation/v1":
		if name == "value" {
			return map[string]any{"$ref": "scenery.value/v1"}, "exact"
		}
		return primitive("json_pointer")
	case "scenery.source.module/v1":
		switch name {
		case "source":
			return primitive("module_source")
		case "version":
			return primitive("version_constraint")
		case "inputs":
			return object("typed_module_inputs")
		default:
			return map[string]any{"canonical_only": true}, "exact"
		}
	case "scenery.source.record/v1":
		if name == "unknown_fields" {
			return primitive("string")
		}
	case "scenery.record.field/v1":
		switch name {
		case "type":
			return typeExpression()
		case "default":
			return map[string]any{"value_type_source": "type"}, "exact"
		case "minimum", "maximum":
			return primitive("number")
		case "min_length", "max_length", "min_items", "max_items":
			return primitive("non_negative_int")
		case "unique_items", "sensitive", "immutable", "deprecated":
			return primitive("bool")
		case "wire_name", "pattern", "format", "replacement":
			return primitive("string")
		}
	case "scenery.record.validation/v1":
		switch name {
		case "when":
			return map[string]any{"expression": "typed_predicate"}, "exact"
		case "path":
			return typedReference()
		case "code", "message":
			return primitive("string")
		}
	case "scenery.source.enum/v1":
		return primitive("bool")
	case "scenery.enum.value/v1":
		return primitive("string")
	case "scenery.source.union/v1":
		switch name {
		case "open":
			return primitive("bool")
		case "unknown_variant":
			return typeExpression()
		default:
			return primitive("string")
		}
	case "scenery.source.operation/v1":
		switch name {
		case "service":
			return resourceRef("service")
		case "input":
			return typeExpression()
		}
	case "scenery.operation.handler/v1":
		return primitive("string")
	case "scenery.operation.outcome/v1":
		if name == "type" {
			return typeExpression()
		}
	case "scenery.union.variant/v1":
		if name == "type" {
			return typeExpression()
		}
		return primitive("string")
	case "scenery.operation.idempotency/v1":
		if name == "key" {
			return map[string]any{"collection": "list", "items": map[string]any{"typed_reference": "schema_path"}}, "exact"
		}
		return primitive("string")
	case "scenery.source.service/v1":
		if name == "runtime" {
			return primitive("string")
		}
	case "scenery.service.implementation/v1":
		return primitive("string")
	case "scenery.service.dependency/v1":
		if name == "instance" {
			return map[string]any{"resource_ref_one_of": []string{"scenery.data-source/v1", "scenery.event-bus/v1", "scenery.execution-engine/v1", "scenery.secret-store/v1"}}, "exact"
		}
		return list("string")
	case "scenery.service.lifecycle/v1":
		return primitive("string")
	case "scenery.source.event/v1":
		if name == "payload" {
			return typeExpression()
		}
		return primitive("positive_int")
	case "scenery.source.entity/v1":
		if name == "type" {
			return typeExpression()
		}
		if name == "data_source" {
			return resourceRef("data_source")
		}
	case "scenery.entity.mapping/v1":
		return primitive("string")
	case "scenery.entity.field-default/v1":
		if name == "value" {
			return map[string]any{"value_type_source": "entity_field"}, "exact"
		}
		return primitive("string")
	case "scenery.entity.field/v1":
		if oneOf(name, "primary_key", "tenant_key", "immutable") {
			return primitive("bool")
		}
		return primitive("string")
	case "scenery.entity.index/v1":
		switch name {
		case "fields":
			return list("string")
		case "unique":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.entity.unique/v1":
		return list("string")
	case "scenery.entity.foreign-key/v1":
		switch name {
		case "fields", "target_fields":
			return list("string")
		case "target":
			return resourceRef("entity")
		default:
			return primitive("string")
		}
	case "scenery.entity.deletion/v1":
		return primitive("string")
	case "scenery.source.view/v1":
		switch name {
		case "data_source":
			return resourceRef("data_source")
		case "input", "result":
			return typeExpression()
		}
	case "scenery.view.implementation/v1":
		if name == "file" {
			return primitive("relative_path")
		}
		return primitive("string")
	case "scenery.source.binding/v1":
		switch name {
		case "gateway":
			return resourceRef("http_gateway")
		case "operation":
			return resourceRef("operation")
		case "execution":
			return resourceRef("execution")
		case "authentication":
			return resourceRef("authentication")
		case "authorization":
			return resourceRef("authorization")
		case "pipeline":
			return resourceRef("pipeline")
		case "protocol", "delivery", "exposure":
			return primitive("string")
		}
	case "scenery.binding.http/v1":
		switch name {
		case "codec_profile":
			return map[string]any{"resource_ref": "std.codec"}, "exact"
		case "request_limit", "response_limit", "timeouts":
			return object("scenery.http-effective-policy/v1")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.path-parameter/v1":
		return typedReference()
	case "scenery.binding.http.value-parameter/v1", "scenery.binding.http.query-parameter/v1", "scenery.binding.http.request-header/v1", "scenery.binding.http.request-cookie/v1":
		if name == "to" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.http.context/v1", "scenery.binding.event.map/v1":
		return typedReference()
	case "scenery.binding.http.multipart-part/v1":
		switch name {
		case "to":
			return typedReference()
		case "media_types":
			return list("string")
		case "max_bytes":
			return primitive("non_negative_int")
		case "multiple", "retain_filename":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.body/v1":
		switch name {
		case "to", "from":
			return typedReference()
		case "include", "except", "accepted_media_types", "produced_media_types", "content_encodings":
			return list("string")
		case "max_compressed_bytes", "max_decompressed_bytes", "max_parts":
			return primitive("non_negative_int")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.response-body/v1":
		switch name {
		case "from":
			return typedReference()
		case "produced_media_types", "content_encodings":
			return list("string")
		case "max_compressed_bytes", "max_decompressed_bytes":
			return primitive("non_negative_int")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.response-header/v1":
		if name == "from" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.http.response-cookie/v1":
		switch name {
		case "from":
			return typedReference()
		case "max_age":
			return primitive("int")
		case "secure", "http_only":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.response/v1":
		if name == "when" {
			return typedReference()
		}
		return primitive("http_status")
	case "scenery.binding.internal/v1":
		if name == "principal" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.cli.context/v1":
		return typedReference()
	case "scenery.binding.cli.argument/v1":
		switch name {
		case "position":
			return primitive("non_negative_int")
		case "to":
			return typedReference()
		case "required":
			return primitive("bool")
		}
	case "scenery.binding.cli.flag/v1":
		switch name {
		case "to":
			return typedReference()
		case "required":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.binding.cli.output/v1":
		if name == "from" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.cli.outcome/v1":
		if name == "when" {
			return typedReference()
		}
		return primitive("int")
	case "scenery.binding.cli/v1":
		return list("string")
	case "scenery.binding.event/v1":
		switch name {
		case "bus":
			return resourceRef("event_bus")
		case "contract":
			return typeExpression()
		case "ordering_key", "deduplication_key":
			return typedReference()
		default:
			return primitive("string")
		}
	case "scenery.event.broker-retry/v1":
		if name == "attempts" {
			return primitive("positive_int")
		}
		return primitive("duration")
	case "scenery.source.execution/v1":
		switch name {
		case "operation":
			return resourceRef("operation")
		case "engine":
			return resourceRef("execution_engine")
		case "revision", "attempts":
			return primitive("positive_int")
		case "timeout", "lease":
			return primitive("duration")
		default:
			return primitive("string")
		}
	case "scenery.execution.retry/v1":
		switch name {
		case "initial", "maximum":
			return primitive("duration")
		case "factor", "jitter":
			return primitive("number")
		default:
			return primitive("string")
		}
	case "scenery.execution.concurrency/v1":
		if name == "key" {
			return typedReference()
		}
		return primitive("positive_int")
	case "scenery.execution.retention/v1", "scenery.execution.deduplication/v1":
		if name == "conflict" {
			return primitive("string")
		}
		return primitive("duration")
	case "scenery.source.schedule/v1":
		if name == "overlap" {
			return map[string]any{"primitive": "string"}, "exact"
		}
	case "scenery.schedule.trigger/v1":
		switch name {
		case "every":
			return primitive("duration")
		case "at":
			return primitive("datetime")
		default:
			return primitive("string")
		}
	case "scenery.schedule.invoke/v1":
		switch name {
		case "operation":
			return resourceRef("operation")
		case "execution":
			return resourceRef("execution")
		case "authorization":
			return resourceRef("authorization")
		case "pipeline":
			return resourceRef("pipeline")
		case "identity":
			return resourceRef("workload_identity")
		case "input":
			return object("typed_operation_input")
		}
	case "scenery.schedule.catchup/v1":
		return primitive("duration")
	case "scenery.source.event_emission/v1":
		switch name {
		case "bus":
			return resourceRef("event_bus")
		case "contract":
			return resourceRef("event")
		case "ordering_key", "deduplication_key":
			return typedReference()
		default:
			return primitive("string")
		}
	case "scenery.event-emission.from/v1":
		return typedReference()
	case "scenery.service.client/v1":
		if name == "binding" {
			return resourceRef("binding")
		}
	case "scenery.source.crud/v1":
		switch name {
		case "entity":
			return resourceRef("entity")
		case "implementation":
			return map[string]any{"resource_ref": "std.crud"}, "exact"
		case "actions":
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		}
	case "scenery.crud.execution/v1":
		if name == "timeout" {
			return primitive("duration")
		}
		return primitive("string")
	case "scenery.crud.http/v1":
		switch name {
		case "codec_profile":
			return map[string]any{"resource_ref": "std.codec"}, "exact"
		case "gateway":
			return resourceRef("http_gateway")
		case "authentication":
			return resourceRef("authentication")
		case "authorization":
			return resourceRef("authorization")
		case "pipeline":
			return resourceRef("pipeline")
		default:
			return primitive("string")
		}
	case "scenery.crud.internal/v1":
		switch name {
		case "authentication":
			return resourceRef("authentication")
		case "authorization":
			return resourceRef("authorization")
		case "pipeline":
			return resourceRef("pipeline")
		default:
			return primitive("string")
		}
	case "scenery.crud.extension/v1":
		return object("provider_config")
	case "scenery.source.fixture/v1":
		switch name {
		case "entity":
			return resourceRef("entity")
		case "environments":
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		case "values":
			return map[string]any{"collection": "list", "items": map[string]any{"value_type_source": "entity"}}, "exact"
		default:
			return primitive("string")
		}
	case "scenery.source.page/v1":
		if name == "load" {
			return resourceRef("binding")
		}
		return primitive("route_path")
	case "scenery.page.action/v1":
		return resourceRef("binding")
	case "scenery.source.renderer/v1":
		switch name {
		case "page":
			return resourceRef("page")
		case "module":
			return primitive("relative_path")
		case "config":
			return object("renderer_config")
		default:
			return primitive("string")
		}
	case "scenery.source.middleware/v1":
		switch name {
		case "protocols", "phases", "before", "after", "effects":
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		case "exclusive":
			return primitive("bool")
		}
	}

	if oneOf(name, "when", "to", "from", "path", "key", "payload", "value", "target") && !oneOf(name, "path") {
		return typedReference()
	}
	if name == "type" {
		return typeExpression()
	}
	if oneOf(name, "required", "optional", "sensitive", "immutable", "deprecated", "unique_items", "open", "secure", "http_only", "multiple", "retain_filename", "unique", "primary_key", "tenant_key", "verify_by_default", "deployment_bindable") {
		return map[string]any{"primitive": "bool"}, "inferred"
	}
	if oneOf(name, "position", "status", "exit", "port", "replicas", "revision", "attempts", "limit", "maximum_attempts", "maximum_delay_milliseconds", "max_bytes", "max_parts", "max_age", "version") {
		return map[string]any{"primitive": "int"}, "inferred"
	}
	if oneOf(name, "packages", "build_tags", "go_flags", "environment", "gateways", "include", "statuses", "fields", "target_fields", "environments", "actions", "protocols", "phases", "before", "after", "effects", "require_capabilities", "capabilities", "instance_kinds") {
		return map[string]any{"collection": "list", "items": map[string]any{"$ref": "scenery.value/v1"}}, "inferred"
	}
	return map[string]any{"$ref": "scenery.value/v1"}, "generic"
}

func authoredRevisionDomain(revision, name string) string {
	domain := authoredDefaultRevisionDomain(revision)
	if override := authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}]; override.RevisionDomain != "" {
		domain = override.RevisionDomain
	}
	return domain
}

func authoredDefaultRevisionDomain(revision string) string {
	if strings.HasPrefix(revision, "scenery.deployment.") || revision == "scenery.source.deployment/v1" {
		return "deployment"
	}
	if revision == "scenery.source.secret/v1" {
		return "deployment"
	}
	if revision == "scenery.source.module/v1" || revision == "scenery.source.patch/v1" {
		return "workspace_only"
	}
	if revision == "scenery.patch.operation/v1" {
		return "workspace_only"
	}
	if strings.HasPrefix(revision, "scenery.source.go-") || strings.HasPrefix(revision, "scenery.go-target.") || revision == "scenery.source.typescript_client/v1" {
		return "implementation"
	}
	if revision == "scenery.typescript-client.retry/v1" {
		return "implementation"
	}
	switch revision {
	case "scenery.service.implementation/v1", "scenery.service.lifecycle/v1", "scenery.service.config/v1", "scenery.operation.handler/v1", "scenery.view.implementation/v1":
		return "implementation"
	}
	return "contract"
}
