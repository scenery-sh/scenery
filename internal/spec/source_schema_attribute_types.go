package spec

import "strings"

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
		case "react":
			return object("typescript_client_react")
		case "dev_runtime":
			return primitive("bool")
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
	case "scenery.typescript-client.react":
		return primitive("relative_path")
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
	case "scenery.binding.mcp":
		switch name {
		case "read_only", "destructive", "idempotent", "open_world", "allow_sensitive_output":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.source.mcp_connection":
		switch name {
		case "url":
			return primitive("url")
		case "connect_timeout", "call_timeout":
			return primitive("duration")
		default:
			return primitive("string")
		}
	case "scenery.mcp-connection.auth":
		switch name {
		case "secret":
			return resourceRef("secret")
		default:
			return primitive("string")
		}
	case "scenery.mcp-connection.tools":
		if name == "allow" || name == "block" {
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		}
	case "scenery.source.mcp_server":
		switch name {
		case "max_input_bytes", "max_result_bytes":
			return primitive("positive_int")
		default:
			return primitive("string")
		}
	case "scenery.mcp-server.capability":
		switch name {
		case "binding":
			return resourceRef("binding")
		default:
			return primitive("string")
		}
	case "scenery.mcp-server.connection":
		switch name {
		case "connection":
			return resourceRef("mcp_connection")
		case "required":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.source.assistant":
		if name == "mcp_server" {
			return resourceRef("mcp_server")
		}
		return primitive("string")
	case "scenery.assistant.implementation":
		switch name {
		case "source", "package", "package_lock":
			return primitive("relative_path")
		default:
			return primitive("string")
		}
	case "scenery.assistant.surface":
		switch name {
		case "gateway":
			return resourceRef("http_gateway")
		case "path":
			return primitive("route_path")
		case "authentication":
			return resourceRef("authentication")
		case "authorization":
			return resourceRef("authorization")
		case "pipeline":
			return resourceRef("pipeline")
		case "client":
			return resourceRef("typescript_client")
		default:
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
		case "list":
			return object("crud_list")
		}
	case "scenery.crud.execution":
		if name == "timeout" {
			return primitive("duration")
		}
		return primitive("string")
	case "scenery.crud.list":
		switch name {
		case "filters", "search", "sorts":
			return map[string]any{"collection": "set", "items": map[string]any{"primitive": "string"}}, "exact"
		case "default_sort":
			return object("crud_default_sort")
		case "max_page_size":
			return primitive("positive_int")
		}
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
	case "scenery.source.react_component":
		if name == "module" {
			return primitive("relative_path")
		}
		return primitive("string")
	case "scenery.source.status_map":
		return primitive("string")
	case "scenery.status-map.status":
		return primitive("string")
	case "scenery.source.form_dialog":
		if name == "source" {
			return resourceRef("binding")
		}
		return primitive("string")
	case "scenery.form-dialog.field":
		if name == "status_map" {
			return resourceRef("status_map")
		}
		return primitive("string")
	case "scenery.source.table_page":
		switch name {
		case "source":
			return map[string]any{"resource_ref_one_of": []string{"scenery.binding", "scenery.crud"}}, "exact"
		case "metadata":
			return list("string")
		case "page_size":
			return primitive("positive_int")
		case "hide_header":
			return primitive("bool")
		case "nav_order":
			return primitive("non_negative_int")
		case "nav_active_paths":
			return list("route_path")
		default:
			return primitive("string")
		}
	case "scenery.source.split_page":
		if name == "source" {
			return resourceRef("binding")
		}
		if name == "nav_order" {
			return primitive("non_negative_int")
		}
		if name == "nav_active_paths" {
			return list("route_path")
		}
		return primitive("string")
	case "scenery.source.content_page":
		switch name {
		case "source":
			return resourceRef("binding")
		case "max_width":
			return primitive("positive_int")
		case "nav_order":
			return primitive("non_negative_int")
		case "nav_active_paths":
			return list("route_path")
		default:
			return primitive("string")
		}
	case "scenery.source.workspace_page":
		switch name {
		case "nav_order":
			return primitive("non_negative_int")
		case "nav_active_paths":
			return list("route_path")
		default:
			return primitive("string")
		}
	case "scenery.source.detail_page":
		switch name {
		case "source":
			return resourceRef("binding")
		case "nav_order":
			return primitive("non_negative_int")
		case "nav_active_paths":
			return list("route_path")
		default:
			return primitive("string")
		}
	case "scenery.detail-page.param", "scenery.page.param":
		return primitive("string")
	case "scenery.detail-page.section":
		return primitive("string")
	case "scenery.detail-page.field":
		if name == "status_map" {
			return resourceRef("status_map")
		}
		if name == "hide_empty" {
			return primitive("bool")
		}
		return primitive("string")
	case "scenery.detail-page.action":
		switch name {
		case "dialog":
			return resourceRef("form_dialog")
		case "primary":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.detail-page.table":
		if name == "page" {
			return resourceRef("table_page")
		}
		return primitive("string")
	case "scenery.workspace-page.tab":
		switch name {
		case "page":
			return map[string]any{"resource_ref_one_of": []string{"scenery.table-page", "scenery.content-page"}}, "exact"
		case "destination":
			return primitive("route_path")
		default:
			return primitive("string")
		}
	case "scenery.workspace-page.stats":
		if name == "source" {
			return resourceRef("binding")
		}
		return primitive("string")
	case "scenery.workspace-page.stats.tile":
		return primitive("string")
	case "scenery.page.search":
		return typeExpression()
	case "scenery.table-page.column", "scenery.table-page.filter":
		switch name {
		case "component":
			return resourceRef("react_component")
		case "status_map":
			return resourceRef("status_map")
		case "pinned", "hidden", "export", "export_zero_empty":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.table-page.filter.preset":
		return primitive("string")
	case "scenery.table-page.pagination":
		return primitive("string")
	case "scenery.table-page.query":
		if name == "search_hidden" {
			return primitive("bool")
		}
		return primitive("string")
	case "scenery.table-page.predicate":
		if name == "value" {
			return map[string]any{"$ref": "scenery.value"}, "exact"
		}
		return primitive("string")
	case "scenery.table-page.slot", "scenery.table-page.row-action", "scenery.table-page.toolbar", "scenery.content-page.slot":
		if name == "component" {
			return resourceRef("react_component")
		}
		return primitive("string")
	case "scenery.table-page.row-detail":
		switch name {
		case "dialog":
			return resourceRef("form_dialog")
		case "component":
			return resourceRef("react_component")
		case "panel_width":
			return primitive("positive_int")
		default:
			return primitive("string")
		}
	case "scenery.table-page.sort":
		return primitive("string")
	case "scenery.table-page.group":
		switch name {
		case "order":
			return list("string")
		case "default":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.table-page.stats":
		if name == "source" {
			return resourceRef("binding")
		}
		return primitive("string")
	case "scenery.table-page.stats.tile":
		if name == "clear" {
			return primitive("bool")
		}
		if name == "value" {
			return map[string]any{"$ref": "scenery.value"}, "exact"
		}
		return primitive("string")
	case "scenery.table-page.action":
		switch name {
		case "dialog":
			return resourceRef("form_dialog")
		case "primary":
			return primitive("bool")
		default:
			return primitive("string")
		}
	case "scenery.table-page.export":
		return primitive("string")
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
	if oneOf(name, "required", "optional", "sensitive", "immutable", "deprecated", "unique_items", "open", "secure", "http_only", "multiple", "retain_filename", "unique", "primary_key", "tenant_key", "verify_by_default", "deployment_bindable", "public") {
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
