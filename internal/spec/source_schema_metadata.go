package spec

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

type AuthoredFieldKey = authoredFieldKey
type AuthoredFieldOverride = authoredFieldOverride

var authoredFieldOverrides = map[authoredFieldKey]authoredFieldOverride{
	{Name: "config"}: {SensitivitySource: "provider_schema"},

	{Revision: "scenery.authorization.rule", Name: "allow"}:                           {Phase: "runtime"},
	{Revision: "scenery.authorization.rule", Name: "deny"}:                            {Phase: "runtime"},
	{Revision: "scenery.record.validation", Name: "when"}:                             {Phase: "runtime"},
	{Revision: "scenery.operation.idempotency", Name: "key"}:                          {Phase: "runtime", Ordered: true, Constraints: map[string]any{"min_items": 1, "reference_root": "input", "reference_shape": "direct_input_field"}},
	{Revision: "scenery.execution.concurrency", Name: "key"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.context", Name: "from"}:                          {Phase: "runtime"},
	{Revision: "scenery.binding.http.response", Name: "when"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-body", Name: "from"}:                    {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-header", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.http.response-cookie", Name: "from"}:                  {Phase: "runtime"},
	{Revision: "scenery.binding.cli.context", Name: "from"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.cli.output", Name: "from"}:                            {Phase: "runtime"},
	{Revision: "scenery.binding.cli.outcome", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.binding.event.map", Name: "from"}:                             {Phase: "runtime"},
	{Revision: "scenery.binding.event", Name: "ordering_key"}:                         {Phase: "runtime"},
	{Revision: "scenery.binding.event", Name: "deduplication_key"}:                    {Phase: "runtime"},
	{Revision: "scenery.source.event_emission", Name: "ordering_key"}:                 {Phase: "runtime"},
	{Revision: "scenery.source.event_emission", Name: "deduplication_key"}:            {Phase: "runtime"},
	{Revision: "scenery.event-emission.from", Name: "when"}:                           {Phase: "runtime"},
	{Revision: "scenery.event-emission.from", Name: "payload"}:                        {Phase: "runtime"},
	{Revision: "scenery.source.authorization", Name: "strategy"}:                      {Default: "deny_unless_allowed", DefaultSource: "spec", Constraints: enumConstraint("all_must_allow", "allow_if_all", "allow_if_any", "any_allow", "deny_unless_allowed", "first_applicable")},
	{Revision: "scenery.source.record", Name: "unknown_fields"}:                       {Default: "reject", DefaultSource: "spec", Constraints: enumConstraint("preserve", "reject")},
	{Revision: "scenery.source.enum", Name: "open"}:                                   {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.source.union", Name: "open"}:                                  {Default: false, DefaultSource: "spec"},
	{Revision: "scenery.binding.http.response-cookie", Name: "path"}:                  {Default: "/", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "max_age"}:               {Default: 0, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "secure"}:                {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "http_only"}:             {Default: true, DefaultSource: "http_profile"},
	{Revision: "scenery.binding.http.response-cookie", Name: "same_site"}:             {Default: "lax", DefaultSource: "http_profile", Constraints: enumConstraint("lax", "none", "strict")},
	{Revision: "scenery.binding.http", Name: "guarantee"}:                             {Default: "framework_enforced", DefaultSource: "http_profile"},
	{Revision: "scenery.binding.cli", Name: "command"}:                                {Ordered: true},
	{Revision: "scenery.deployment.http-listener", Name: "http_versions"}:             {Ordered: true},
	{Revision: "scenery.source.typescript_client", Name: "module"}:                    {Constraints: enumConstraint("esm")},
	{Revision: "scenery.source.typescript_client", Name: "runtime"}:                   {Constraints: enumConstraint("fetch")},
	{Revision: "scenery.typescript-client.retry", Name: "policy"}:                     {Constraints: enumConstraint("scenery.retry.idempotent")},
	{Revision: "scenery.typescript-client.retry", Name: "maximum_attempts"}:           {Constraints: map[string]any{"minimum": 2, "maximum": 10}},
	{Revision: "scenery.typescript-client.retry", Name: "maximum_delay_milliseconds"}: {Constraints: map[string]any{"maximum": 86_400_000}},
	{Revision: "scenery.typescript-client.retry", Name: "statuses"}:                   {Constraints: map[string]any{"item_minimum": 400, "item_maximum": 599, "unique_items": true}},
	{Revision: "scenery.operation.idempotency", Name: "mode"}:                         {Constraints: enumConstraint("keyed", "none")},
	{Revision: "scenery.source.execution", Name: "mode"}:                              {Constraints: enumConstraint("direct", "durable", "workflow")},
	{Revision: "scenery.execution.retry", Name: "strategy"}:                           {Constraints: enumConstraint("exponential", "none")},
	{Revision: "scenery.execution.deduplication", Name: "conflict"}:                   {Constraints: enumConstraint("return_existing")},
	{Revision: "scenery.source.binding", Name: "protocol"}:                            {Constraints: enumConstraint("cli", "event", "http", "internal")},
	{Revision: "scenery.source.binding", Name: "delivery"}:                            {Constraints: enumConstraint("call", "enqueue", "stream", "wait")},
	{Revision: "scenery.source.binding", Name: "exposure"}:                            {Constraints: enumConstraint("application", "internet", "local", "package", "private_network")},
	{Revision: "scenery.binding.internal", Name: "visibility"}:                        {Constraints: enumConstraint("application", "package")},
	{Revision: "scenery.binding.internal", Name: "principal"}:                         {Constraints: enumConstraint("inherit")},
	{Revision: "scenery.binding.event", Name: "direction"}:                            {Constraints: enumConstraint("consume")},
	{Revision: "scenery.binding.event", Name: "guarantee"}:                            {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.source.event_emission", Name: "guarantee"}:                    {Constraints: enumConstraint("at_least_once", "at_most_once", "exactly_once")},
	{Revision: "scenery.event.broker-retry", Name: "backoff"}:                         {Constraints: enumConstraint("exponential", "fixed", "none")},
	{Revision: "scenery.source.schedule", Name: "overlap"}:                            {Constraints: enumConstraint("allow", "queue", "replace", "skip")},
	{Revision: "scenery.entity.field-default", Name: "strategy"}:                      {Constraints: enumConstraint("current_datetime", "provider", "uuid_v7")},
	{Revision: "scenery.crud.execution", Name: "mode"}:                                {Constraints: enumConstraint("direct", "durable")},
	{Revision: "scenery.source.fixture", Name: "mode"}:                                {Constraints: enumConstraint("insert", "replace", "upsert")},
	{Revision: "scenery.deployment.secret", Name: "value"}:                            {Sensitive: true},
	{Revision: "scenery.deployment.http-listener", Name: "certificate"}:               {UnsupportedDraft: "platform_listener_and_certificate_schemas"},
	{Revision: "scenery.deployment.http-listener", Name: "platform_identity"}:         {UnsupportedDraft: "platform_listener_and_certificate_schemas"},

	{Revision: "scenery.source.service", Name: "implementation"}:         {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "lifecycle"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "config"}:                 {RevisionDomain: "implementation"},
	{Revision: "scenery.source.service", Name: "config_schema"}:          {RevisionDomain: "implementation"},
	{Revision: "scenery.source.operation", Name: "handler"}:              {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider", Name: "source"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.provider", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.data_source", Name: "config"}:             {RevisionDomain: "implementation"},
	{Revision: "scenery.source.execution_engine", Name: "config"}:        {RevisionDomain: "implementation"},
	{Revision: "scenery.source.event_bus", Name: "config"}:               {RevisionDomain: "implementation"},
	{Revision: "scenery.source.secret_store", Name: "config"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.view", Name: "implementation_digest"}:     {RevisionDomain: "implementation"},
	{Revision: "scenery.source.crud", Name: "implementation"}:            {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "module"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "config"}:                {RevisionDomain: "implementation"},
	{Revision: "scenery.source.renderer", Name: "implementation_digest"}: {RevisionDomain: "implementation"},
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

func AuthoredAttributeDefinition(revision, name string) SourceAttributeSchema {
	return authoredAttributeDefinition(revision, name)
}

func AuthoredFieldOverrides() map[AuthoredFieldKey]AuthoredFieldOverride {
	return authoredFieldOverrides
}

func AuthoredRevisionDomain(revision, name string) string {
	return authoredRevisionDomain(revision, name)
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

func UnsupportedDraftCapability(revision, name string) string {
	return authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}].UnsupportedDraft
}

func authoredAttributeType(revision, name string) (map[string]any, string) {
	resourceRef := func(kind string) (map[string]any, string) {
		return map[string]any{"resource_ref": "scenery." + strings.ReplaceAll(kind, "_", "-")}, "exact"
	}
	typeExpression := func() (map[string]any, string) { return map[string]any{"type_expression": "scenery.type"}, "exact" }
	typedReference := func() (map[string]any, string) { return map[string]any{"typed_reference": "schema_path"}, "exact" }
	primitive := func(kind string) (map[string]any, string) { return map[string]any{"primitive": kind}, "exact" }
	list := func(item string) (map[string]any, string) {
		return map[string]any{"collection": "list", "items": map[string]any{"primitive": item}}, "exact"
	}
	object := func(schema string) (map[string]any, string) { return map[string]any{"object": schema}, "exact" }

	switch revision {
	case "scenery.source.go_module":
		return primitive("relative_path_or_import_path")
	case "scenery.source.go_toolchain":
		if name == "experiments" {
			return list("string")
		}
		return primitive("version")
	case "scenery.source.go_target":
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
	case "scenery.go-target.test":
		return list("string")
	case "scenery.source.http_gateway":
		switch name {
		case "cors":
			return map[string]any{"resource_ref": "std.cors"}, "exact"
		case "trusted_proxies":
			return map[string]any{"resource_ref": "std.trusted_proxies"}, "exact"
		case "forwarded":
			return map[string]any{"resource_ref": "std.forwarded_headers"}, "exact"
		case "request_limit", "response_limit", "timeouts":
			return object("scenery.http-effective-policy")
		default:
			return primitive("string")
		}
	case "scenery.source.authentication":
		switch name {
		case "provider":
			return resourceRef("provider")
		case "scheme":
			return primitive("string")
		case "config":
			return object("provider_config")
		}
	case "scenery.source.authorization":
		if name == "principal" {
			return typeExpression()
		}
		return primitive("string")
	case "scenery.authorization.rule":
		return map[string]any{"expression": "typed_predicate"}, "exact"
	case "scenery.source.workload_identity":
		switch name {
		case "issuer":
			return map[string]any{"resource_ref": "std.identity_issuer"}, "exact"
		case "principal_type":
			return typeExpression()
		case "claims":
			return object("typed_claim_map")
		}
	case "scenery.pipeline.step":
		return map[string]any{"resource_ref": "scenery.middleware", "standard_library_allowed": true}, "exact"
	case "scenery.source.provider":
		switch name {
		case "source":
			return primitive("registry_source")
		case "config":
			return object("provider_config")
		default:
			return map[string]any{"canonical_only": true}, "exact"
		}
	case "scenery.source.data_source", "scenery.source.execution_engine", "scenery.source.event_bus", "scenery.source.secret_store":
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
	case "scenery.source.secret":
		if name == "store" {
			return resourceRef("secret_store")
		}
		return primitive("string")
	case "scenery.source.deployment":
		if name == "fixture_policy" {
			return object("scenery.fixture-policy")
		}
		return primitive("string")
	case "scenery.deployment.module":
		if name == "target" {
			return resourceRef("module")
		}
		return object("typed_module_inputs")
	case "scenery.deployment.data-source":
		if name == "target" {
			return resourceRef("data_source")
		}
		return object("provider_config")
	case "scenery.deployment.service":
		switch name {
		case "target":
			return resourceRef("service")
		case "replicas":
			return primitive("positive_int")
		case "placement", "config":
			return object("deployment_value")
		}
	case "scenery.deployment.resources":
		return primitive("resource_quantity")
	case "scenery.deployment.http-gateway":
		return resourceRef("http_gateway")
	case "scenery.deployment.http-listener":
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
	case "scenery.deployment.provider":
		if name == "target" {
			return resourceRef("provider")
		}
		return object("provider_config")
	case "scenery.deployment.secret":
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
	case "scenery.source.typescript_client":
		switch name {
		case "gateways":
			return map[string]any{"collection": "set", "items": map[string]any{"resource_ref": "scenery.http-gateway"}}, "exact"
		case "include":
			return list("string")
		case "output_root":
			return primitive("relative_path")
		default:
			return primitive("string")
		}
	case "scenery.typescript-client.retry":
		switch name {
		case "maximum_attempts", "maximum_delay_milliseconds":
			return primitive("non_negative_int")
		case "statuses":
			return list("http_status")
		default:
			return primitive("string")
		}
	case "scenery.source.patch":
		if name == "target" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.patch.operation":
		if name == "value" {
			return map[string]any{"$ref": "scenery.value"}, "exact"
		}
		return primitive("json_pointer")
	case "scenery.source.module":
		switch name {
		case "source":
			return primitive("module_source")
		case "inputs":
			return object("typed_module_inputs")
		default:
			return map[string]any{"canonical_only": true}, "exact"
		}
	case "scenery.source.record":
		if name == "unknown_fields" {
			return primitive("string")
		}
	case "scenery.record.field":
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
	case "scenery.record.validation":
		switch name {
		case "when":
			return map[string]any{"expression": "typed_predicate"}, "exact"
		case "path":
			return typedReference()
		case "code", "message":
			return primitive("string")
		}
	case "scenery.source.enum":
		return primitive("bool")
	case "scenery.enum.value":
		return primitive("string")
	case "scenery.source.union":
		switch name {
		case "open":
			return primitive("bool")
		case "unknown_variant":
			return typeExpression()
		default:
			return primitive("string")
		}
	case "scenery.source.operation":
		switch name {
		case "service":
			return resourceRef("service")
		case "input":
			return typeExpression()
		}
	case "scenery.operation.handler":
		return primitive("string")
	case "scenery.operation.outcome":
		if name == "type" {
			return typeExpression()
		}
	case "scenery.union.variant":
		if name == "type" {
			return typeExpression()
		}
		return primitive("string")
	case "scenery.operation.idempotency":
		if name == "key" {
			return map[string]any{"collection": "list", "items": map[string]any{"typed_reference": "schema_path"}}, "exact"
		}
		return primitive("string")
	case "scenery.source.service":
		if name == "runtime" {
			return primitive("string")
		}
	case "scenery.service.implementation":
		return primitive("string")
	case "scenery.service.dependency":
		if name == "instance" {
			return map[string]any{"resource_ref_one_of": []string{"scenery.data-source", "scenery.event-bus", "scenery.execution-engine", "scenery.secret-store"}}, "exact"
		}
		return list("string")
	case "scenery.service.lifecycle":
		return primitive("string")
	case "scenery.source.event":
		if name == "payload" {
			return typeExpression()
		}
		return primitive("positive_int")
	case "scenery.source.entity":
		if name == "type" {
			return typeExpression()
		}
		if name == "data_source" {
			return resourceRef("data_source")
		}
	case "scenery.entity.mapping":
		return primitive("string")
	case "scenery.entity.field-default":
		if name == "value" {
			return map[string]any{"value_type_source": "entity_field"}, "exact"
		}
		return primitive("string")
	case "scenery.entity.field":
		if oneOf(name, "primary_key", "tenant_key", "immutable") {
			return primitive("bool")
		}
		return primitive("string")
	case "scenery.entity.index":
		switch name {
		case "fields":
			return list("string")
		case "unique":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.entity.unique":
		return list("string")
	case "scenery.entity.foreign-key":
		switch name {
		case "fields", "target_fields":
			return list("string")
		case "target":
			return resourceRef("entity")
		default:
			return primitive("string")
		}
	case "scenery.entity.deletion":
		return primitive("string")
	case "scenery.source.view":
		switch name {
		case "data_source":
			return resourceRef("data_source")
		case "input", "result":
			return typeExpression()
		}
	case "scenery.view.implementation":
		if name == "file" {
			return primitive("relative_path")
		}
		return primitive("string")
	case "scenery.source.binding":
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
	case "scenery.binding.http":
		switch name {
		case "codec_profile":
			return map[string]any{"resource_ref": "std.codec"}, "exact"
		case "request_limit", "response_limit", "timeouts":
			return object("scenery.http-effective-policy")
		default:
			return primitive("string")
		}
	case "scenery.binding.http.path-parameter":
		return typedReference()
	case "scenery.binding.http.value-parameter", "scenery.binding.http.query-parameter", "scenery.binding.http.request-header", "scenery.binding.http.request-cookie":
		if name == "to" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.http.context", "scenery.binding.event.map":
		return typedReference()
	case "scenery.binding.http.multipart-part":
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
	case "scenery.binding.http.body":
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
	case "scenery.binding.http.response-body":
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
	case "scenery.binding.http.response-header":
		if name == "from" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.http.response-cookie":
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
	case "scenery.binding.http.response":
		if name == "when" {
			return typedReference()
		}
		return primitive("http_status")
	case "scenery.binding.internal":
		if name == "principal" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.cli.context":
		return typedReference()
	case "scenery.binding.cli.argument":
		switch name {
		case "position":
			return primitive("non_negative_int")
		case "to":
			return typedReference()
		case "required":
			return primitive("bool")
		}
	case "scenery.binding.cli.flag":
		switch name {
		case "to":
			return typedReference()
		case "required":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.binding.cli.output":
		if name == "from" {
			return typedReference()
		}
		return primitive("string")
	case "scenery.binding.cli.outcome":
		if name == "when" {
			return typedReference()
		}
		return primitive("int")
	case "scenery.binding.cli":
		return list("string")
	case "scenery.binding.event":
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
	case "scenery.event.broker-retry":
		if name == "attempts" {
			return primitive("positive_int")
		}
		return primitive("duration")
	case "scenery.source.execution":
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
	case "scenery.execution.retry":
		switch name {
		case "initial", "maximum":
			return primitive("duration")
		case "factor", "jitter":
			return primitive("number")
		default:
			return primitive("string")
		}
	case "scenery.execution.concurrency":
		if name == "key" {
			return typedReference()
		}
		return primitive("positive_int")
	case "scenery.execution.retention", "scenery.execution.deduplication":
		if name == "conflict" {
			return primitive("string")
		}
		return primitive("duration")
	case "scenery.source.schedule":
		if name == "overlap" {
			return map[string]any{"primitive": "string"}, "exact"
		}
	case "scenery.schedule.trigger":
		switch name {
		case "every":
			return primitive("duration")
		case "at":
			return primitive("datetime")
		default:
			return primitive("string")
		}
	case "scenery.schedule.invoke":
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
	case "scenery.schedule.catchup":
		return primitive("duration")
	case "scenery.source.event_emission":
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
	case "scenery.event-emission.from":
		return typedReference()
	case "scenery.service.client":
		if name == "binding" {
			return resourceRef("binding")
		}
	case "scenery.source.crud":
		switch name {
		case "entity":
			return resourceRef("entity")
		case "implementation":
			return map[string]any{"resource_ref": "std.crud"}, "exact"
		case "actions":
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		}
	case "scenery.crud.execution":
		if name == "timeout" {
			return primitive("duration")
		}
		return primitive("string")
	case "scenery.crud.http":
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
	case "scenery.crud.internal":
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
	case "scenery.crud.extension":
		return object("provider_config")
	case "scenery.source.fixture":
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
	case "scenery.source.page":
		if name == "load" {
			return resourceRef("binding")
		}
		return primitive("route_path")
	case "scenery.page.action":
		return resourceRef("binding")
	case "scenery.source.renderer":
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
	case "scenery.source.middleware":
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
		return map[string]any{"collection": "list", "items": map[string]any{"$ref": "scenery.value"}}, "inferred"
	}
	return map[string]any{"$ref": "scenery.value"}, "generic"
}

func authoredRevisionDomain(revision, name string) string {
	domain := authoredDefaultRevisionDomain(revision)
	if override := authoredFieldOverrides[authoredFieldKey{Revision: revision, Name: name}]; override.RevisionDomain != "" {
		domain = override.RevisionDomain
	}
	return domain
}

func authoredDefaultRevisionDomain(revision string) string {
	if strings.HasPrefix(revision, "scenery.deployment.") || revision == "scenery.source.deployment" {
		return "deployment"
	}
	if revision == "scenery.source.secret" {
		return "deployment"
	}
	if revision == "scenery.source.module" || revision == "scenery.source.patch" {
		return "workspace_only"
	}
	if revision == "scenery.patch.operation" {
		return "workspace_only"
	}
	if strings.HasPrefix(revision, "scenery.source.go-") || strings.HasPrefix(revision, "scenery.go-target.") || revision == "scenery.source.typescript_client" {
		return "implementation"
	}
	if revision == "scenery.typescript-client.retry" {
		return "implementation"
	}
	switch revision {
	case "scenery.service.implementation", "scenery.service.lifecycle", "scenery.service.config", "scenery.operation.handler", "scenery.view.implementation":
		return "implementation"
	}
	return "contract"
}
