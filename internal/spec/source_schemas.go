package spec

import (
	"maps"
	"slices"
)

const (
	semanticLabelPattern   = "^[a-z][a-z0-9_]*$"
	httpHeaderLabelPattern = "^[!#$%&'*+.^_`|~0-9a-z-]+$"
	httpQueryLabelPattern  = "^[^\\x00-\\x20\\x7f&=#]+$"
	httpCookieLabelPattern = "^[!#$%&'*+.^_`|~0-9A-Za-z-]+$"
	multipartLabelPattern  = "^[^\\x00-\\x1f\\x7f]+$"
)

type authoredBlockSchema struct {
	Revision               string
	Labels                 int
	LabelPattern           string
	LabelPolicy            string
	Attributes             map[string]authoredAttributeSchema
	Required               map[string]bool
	Children               map[string]authoredChildSchema
	AllowUnknownAttributes bool
	DynamicAttribute       *authoredAttributeSchema
}

type authoredChildSchema struct {
	Schema     *authoredBlockSchema
	Repeatable bool
	Ordered    bool
}

func sourceSchema(revision string, labels int, attributes, required []string, children map[string]authoredChildSchema) *authoredBlockSchema {
	schema := &authoredBlockSchema{Revision: revision, Labels: labels, Attributes: map[string]authoredAttributeSchema{}, Required: map[string]bool{}, Children: children}
	if labels > 0 {
		schema.LabelPattern = semanticLabelPattern
		schema.LabelPolicy = "semantic_name"
	}
	for _, name := range attributes {
		schema.Attributes[name] = authoredAttributeDefinition(revision, name)
	}
	for _, name := range required {
		schema.Required[name] = true
	}
	if schema.Children == nil {
		schema.Children = map[string]authoredChildSchema{}
	}
	return schema
}

func wireLabelSchema(schema *authoredBlockSchema, pattern, policy string) *authoredBlockSchema {
	schema.LabelPattern = pattern
	schema.LabelPolicy = policy
	return schema
}

func repeated(schema *authoredBlockSchema) authoredChildSchema {
	return authoredChildSchema{Schema: schema, Repeatable: true}
}

func singleton(schema *authoredBlockSchema) authoredChildSchema {
	return authoredChildSchema{Schema: schema}
}

func ordered(schema *authoredBlockSchema) authoredChildSchema {
	return authoredChildSchema{Schema: schema, Repeatable: true, Ordered: true}
}

var (
	goContractSourceSchema         = sourceSchema("scenery.source.go-contract", 0, []string{"import_path"}, []string{"import_path"}, nil)
	implementationRootSourceSchema = sourceSchema("scenery.source.workspace.implementation-root", 1,
		[]string{"path", "revision_include", "revision_exclude"}, []string{"path", "revision_include"}, nil)
	revisionInputSourceSchema = sourceSchema("scenery.source.workspace.revision-input", 1,
		[]string{"paths", "optional"}, []string{"paths"}, nil)

	authorizationRuleSourceSchema = sourceSchema("scenery.authorization.rule", 1, []string{"allow", "deny"}, nil, nil)
	pipelineStepSourceSchema      = sourceSchema("scenery.pipeline.step", 1, []string{"use"}, []string{"use"}, nil)
	goTargetTestSourceSchema      = sourceSchema("scenery.go-target.test", 0, []string{"additional_build_tags", "packages"}, nil, nil)

	typescriptRetrySourceSchema = sourceSchema("scenery.typescript-client.retry", 0,
		[]string{"policy", "maximum_attempts", "maximum_delay_milliseconds", "statuses"}, []string{"policy", "maximum_attempts"}, nil)
	typescriptReactSourceSchema = sourceSchema("scenery.typescript-client.react", 0,
		[]string{"tsconfig"}, []string{"tsconfig"}, nil)

	serviceImplementationSourceSchema = sourceSchema("scenery.service.implementation", 0,
		[]string{"constructor"}, nil, nil)
	serviceDependencySourceSchema = sourceSchema("scenery.service.dependency", 1,
		[]string{"instance", "requires"}, []string{"instance"}, nil)
	serviceConfigSourceSchema    = serviceConfigSchema()
	serviceClientSourceSchema    = sourceSchema("scenery.service.client", 1, []string{"binding"}, []string{"binding"}, nil)
	serviceLifecycleSourceSchema = sourceSchema("scenery.service.lifecycle", 0, []string{"start", "stop"}, nil, nil)

	recordFieldSourceSchema = sourceSchema("scenery.record.field", 1,
		[]string{"type", "wire_name", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "immutable", "deprecated", "replacement", "description", "examples"}, []string{"type"}, nil)
	enumValueSourceSchema    = sourceSchema("scenery.enum.value", 1, []string{"wire_value"}, nil, nil)
	unionVariantSourceSchema = sourceSchema("scenery.union.variant", 1, []string{"type", "wire_tag"}, []string{"type"}, nil)

	operationHandlerSourceSchema = sourceSchema("scenery.operation.handler", 0,
		[]string{"method"}, []string{"method"}, nil)
	operationOutcomeSourceSchema     = sourceSchema("scenery.operation.outcome", 1, []string{"type", "description"}, []string{"type"}, nil)
	operationIdempotencySourceSchema = sourceSchema("scenery.operation.idempotency", 0, []string{"mode", "key"}, []string{"mode"}, nil)

	// MCP is a binding protocol rather than a parallel tool declaration. The
	// child schemas below keep the authored source shape explicit while the
	// parent binding/server/assistant resources remain ordinary graph records.
	mcpBindingSourceSchema = sourceSchema("scenery.binding.mcp", 0,
		[]string{"name", "title", "description", "read_only", "destructive", "idempotent", "open_world", "allow_sensitive_output"},
		[]string{"name", "read_only", "destructive", "idempotent", "open_world"}, nil)
	mcpServerCapabilitySourceSchema = sourceSchema("scenery.mcp-server.capability", 1,
		[]string{"binding", "name", "approval"}, []string{"binding", "name", "approval"}, nil)
	assistantImplementationSourceSchema = sourceSchema("scenery.assistant.implementation", 0,
		[]string{"adapter", "source", "package", "package_lock"}, []string{"adapter", "source", "package", "package_lock"}, nil)
	assistantSurfaceSourceSchema = sourceSchema("scenery.assistant.surface", 0,
		[]string{"gateway", "path", "authentication", "authorization", "pipeline", "session_access", "client"},
		[]string{"gateway", "path", "authentication", "authorization", "pipeline", "session_access", "client"}, nil)

	executionRetrySourceSchema = sourceSchema("scenery.execution.retry", 0,
		[]string{"strategy", "initial", "factor", "maximum", "jitter"}, []string{"strategy"}, nil)
	executionConcurrencySourceSchema   = sourceSchema("scenery.execution.concurrency", 0, []string{"key", "limit"}, []string{"key", "limit"}, nil)
	executionRetentionSourceSchema     = sourceSchema("scenery.execution.retention", 0, []string{"success", "failure"}, []string{"success", "failure"}, nil)
	executionDeduplicationSourceSchema = sourceSchema("scenery.execution.deduplication", 0, []string{"retention", "conflict"}, []string{"retention", "conflict"}, nil)

	httpPathParameterSourceSchema  = sourceSchema("scenery.binding.http.path-parameter", 1, []string{"to"}, []string{"to"}, nil)
	httpPathTailSourceSchema       = sourceSchema("scenery.binding.http.path-tail", 1, []string{"to"}, []string{"to"}, nil)
	httpQueryParameterSourceSchema = wireLabelSchema(sourceSchema("scenery.binding.http.query-parameter", 1, []string{"to", "encoding"}, []string{"to"}, nil), httpQueryLabelPattern, "http_query_name")
	httpHeaderSourceSchema         = wireLabelSchema(sourceSchema("scenery.binding.http.request-header", 1, []string{"to", "encoding"}, []string{"to"}, nil), httpHeaderLabelPattern, "http_field_name")
	httpCookieSourceSchema         = wireLabelSchema(sourceSchema("scenery.binding.http.request-cookie", 1, []string{"to", "encoding"}, []string{"to"}, nil), httpCookieLabelPattern, "cookie_name")
	httpContextSourceSchema        = sourceSchema("scenery.binding.http.context", 1, []string{"from", "to"}, []string{"from", "to"}, nil)
	httpMultipartPartSourceSchema  = wireLabelSchema(sourceSchema("scenery.binding.http.multipart-part", 1,
		[]string{"to", "kind", "media_types", "max_bytes", "multiple", "retain_filename"}, []string{"to", "kind"}, nil), multipartLabelPattern, "multipart_field_name")
	httpBodySourceSchema = sourceSchema("scenery.binding.http.body", 0,
		[]string{"codec", "to", "from", "include", "except", "accepted_media_types", "produced_media_types", "content_encodings", "max_compressed_bytes", "max_decompressed_bytes", "max_parts"}, []string{"codec"},
		map[string]authoredChildSchema{"part": repeated(httpMultipartPartSourceSchema)})
	httpResponseBodySourceSchema = sourceSchema("scenery.binding.http.response-body", 0,
		[]string{"codec", "from", "produced_media_types", "content_encodings", "max_compressed_bytes", "max_decompressed_bytes"}, []string{"codec", "from"}, nil)
	httpResponseHeaderSourceSchema = wireLabelSchema(sourceSchema("scenery.binding.http.response-header", 1,
		[]string{"from", "encoding"}, []string{"from"}, nil), httpHeaderLabelPattern, "http_field_name")
	httpResponseCookieSourceSchema = wireLabelSchema(sourceSchema("scenery.binding.http.response-cookie", 1,
		[]string{"from", "path", "domain", "max_age", "expires", "secure", "http_only", "same_site"}, []string{"from"}, nil), httpCookieLabelPattern, "cookie_name")
	httpResponseSourceSchema = sourceSchema("scenery.binding.http.response", 1,
		[]string{"when", "status"}, []string{"when", "status"}, map[string]authoredChildSchema{"header": repeated(httpResponseHeaderSourceSchema), "cookie": repeated(httpResponseCookieSourceSchema), "body": singleton(httpResponseBodySourceSchema)})
	httpSourceSchema = sourceSchema("scenery.binding.http", 0,
		[]string{"method", "path", "codec_profile", "guarantee", "request_limit", "response_limit", "timeouts"}, []string{"method", "path", "codec_profile"},
		map[string]authoredChildSchema{
			"path_parameter": repeated(httpPathParameterSourceSchema), "path_tail": repeated(httpPathTailSourceSchema), "query_parameter": repeated(httpQueryParameterSourceSchema),
			"header": repeated(httpHeaderSourceSchema), "cookie": repeated(httpCookieSourceSchema),
			"context": repeated(httpContextSourceSchema), "body": singleton(httpBodySourceSchema), "response": repeated(httpResponseSourceSchema),
		})
	internalSourceSchema = sourceSchema("scenery.binding.internal", 0, []string{"visibility", "principal"}, []string{"visibility", "principal"}, nil)

	scheduleTriggerSourceSchema = sourceSchema("scenery.schedule.trigger", 0, []string{"every"}, nil, nil)
	scheduleInvokeSourceSchema  = sourceSchema("scenery.schedule.invoke", 0,
		[]string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, []string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, nil)
	scheduleCatchupSourceSchema = sourceSchema("scenery.schedule.catchup", 0, []string{"maximum_age"}, []string{"maximum_age"}, nil)

	pageActionSourceSchema      = sourceSchema("scenery.page.action", 1, []string{"invoke"}, []string{"invoke"}, nil)
	pageParamSourceSchema       = sourceSchema("scenery.page.param", 1, []string{"input"}, []string{"input"}, nil)
	statusMapStatusSourceSchema = sourceSchema("scenery.status-map.status", 1,
		[]string{"label", "variant"}, []string{"label", "variant"}, nil)
	formDialogFieldSourceSchema = sourceSchema("scenery.form-dialog.field", 1,
		[]string{"label", "control", "placeholder", "status_map"}, nil, nil)
	tablePageColumnSourceSchema = sourceSchema("scenery.table-page.column", 1,
		[]string{"label", "appearance", "component", "status_map", "hidden", "export", "export_header", "export_format", "export_empty", "export_zero_empty"}, nil, nil)
	tablePageFilterPresetSourceSchema = sourceSchema("scenery.table-page.filter.preset", 1,
		[]string{"label", "range"}, []string{"label", "range"}, nil)
	tablePageFilterSourceSchema = sourceSchema("scenery.table-page.filter", 1,
		[]string{"label", "component", "status_map", "pinned", "hidden", "input"}, nil,
		map[string]authoredChildSchema{"preset": repeated(tablePageFilterPresetSourceSchema)})
	tablePageSortSourceSchema = sourceSchema("scenery.table-page.sort", 1,
		[]string{"label", "default"}, nil, nil)
	tablePageGroupSourceSchema = sourceSchema("scenery.table-page.group", 1,
		[]string{"label", "order", "default"}, nil, nil)
	tablePagePaginationSourceSchema = sourceSchema("scenery.table-page.pagination", 0,
		[]string{"page", "page_size", "total"}, []string{"page", "page_size", "total"}, nil)
	tablePagePredicateSourceSchema = sourceSchema("scenery.table-page.predicate", 1,
		[]string{"value"}, []string{"value"}, nil)
	tablePageQuerySourceSchema = sourceSchema("scenery.table-page.query", 0,
		[]string{"search", "search_hidden", "sort", "direction"}, nil, nil)
	tablePageSlotSourceSchema = sourceSchema("scenery.table-page.slot", 0,
		[]string{"component"}, []string{"component"}, nil)
	tablePageRowActionSourceSchema = sourceSchema("scenery.table-page.row-action", 0,
		[]string{"component", "prefetch_export"}, []string{"component"}, nil)
	tablePageToolbarSourceSchema = sourceSchema("scenery.table-page.toolbar", 0,
		[]string{"component", "placement"}, []string{"component"}, nil)
	tablePageRowDetailSourceSchema = sourceSchema("scenery.table-page.row-detail", 0,
		[]string{"component", "dialog", "presentation", "panel_width", "prefetch_export"}, []string{"component"}, nil)
	tablePageStatsSourceSchema = sourceSchema("scenery.table-page.stats", 0,
		[]string{"source"}, []string{"source"}, map[string]authoredChildSchema{
			"tile": repeated(sourceSchema("scenery.table-page.stats.tile", 1,
				[]string{"label", "appearance", "sub", "sub_appearance", "sub_label", "icon", "filter", "value", "clear"}, []string{"label"}, nil)),
		})
	tablePageActionSourceSchema = sourceSchema("scenery.table-page.action", 1,
		[]string{"label", "icon", "dialog", "primary"}, []string{"label", "dialog"}, nil)
	tablePageExportSourceSchema = sourceSchema("scenery.table-page.export", 0,
		[]string{"label", "icon", "file_name"}, nil, nil)
	contentPageSlotSourceSchema = sourceSchema("scenery.content-page.slot", 0,
		[]string{"component"}, []string{"component"}, nil)
	workspacePageTabSourceSchema = sourceSchema("scenery.workspace-page.tab", 1,
		[]string{"page", "destination", "label", "description", "group", "count", "available", "unavailable_reason", "application_key", "access_key"}, []string{"label"}, nil)
	workspacePageStatsSourceSchema = sourceSchema("scenery.workspace-page.stats", 0,
		[]string{"source"}, []string{"source"}, map[string]authoredChildSchema{
			"tile": repeated(sourceSchema("scenery.workspace-page.stats.tile", 1,
				[]string{"label", "appearance", "sub", "sub_appearance", "sub_label", "icon"}, []string{"label"}, nil)),
		})
	detailPageParamSourceSchema = sourceSchema("scenery.detail-page.param", 1,
		[]string{"input"}, []string{"input"}, nil)
	detailPageFieldSourceSchema = sourceSchema("scenery.detail-page.field", 1,
		[]string{"label", "appearance", "status_map", "hide_empty"}, nil, nil)
	detailPageSectionSourceSchema = sourceSchema("scenery.detail-page.section", 1,
		[]string{"label", "description"}, []string{"label"}, map[string]authoredChildSchema{"field": ordered(detailPageFieldSourceSchema)})
	detailPageActionSourceSchema = sourceSchema("scenery.detail-page.action", 1,
		[]string{"label", "icon", "dialog", "primary"}, []string{"label", "dialog"}, nil)
	detailPageTableSourceSchema = sourceSchema("scenery.detail-page.table", 1,
		[]string{"label", "page", "param", "input"}, []string{"label", "page", "param", "input"}, nil)
	pageSearchSourceSchema = sourceSchema("scenery.page.search", 1,
		[]string{"type"}, []string{"type"}, nil)
)

func serviceConfigSchema() *authoredBlockSchema {
	schema := sourceSchema("scenery.service.config", 0, nil, nil, nil)
	schema.AllowUnknownAttributes = true
	schema.DynamicAttribute = &authoredAttributeSchema{
		Type: map[string]any{"$ref": "scenery.value", "type_source": "package_input"}, Phase: "compile", InputPhaseSource: "package_input", RevisionSource: "package_input",
		SensitivitySource: "package_input", DefaultSource: "none", Constraints: map[string]any{"name_pattern": "^[a-z][a-z0-9_]*$"}, MetadataStatus: "dynamic",
	}
	return schema
}

var authoredResourceChildren = map[string]map[string]authoredChildSchema{
	"go_target":         {"test": singleton(goTargetTestSourceSchema)},
	"authorization":     {"rule": ordered(authorizationRuleSourceSchema)},
	"pipeline":          {"step": ordered(pipelineStepSourceSchema)},
	"typescript_client": {"retry": singleton(typescriptRetrySourceSchema), "react": singleton(typescriptReactSourceSchema)},
	"service":           {"implementation": singleton(serviceImplementationSourceSchema), "dependency": repeated(serviceDependencySourceSchema), "config": singleton(serviceConfigSourceSchema), "client": repeated(serviceClientSourceSchema), "lifecycle": singleton(serviceLifecycleSourceSchema)},
	"record":            {"field": repeated(recordFieldSourceSchema)},
	"enum":              {"value": repeated(enumValueSourceSchema)},
	"union":             {"variant": repeated(unionVariantSourceSchema)},
	"operation":         {"handler": singleton(operationHandlerSourceSchema), "result": repeated(operationOutcomeSourceSchema), "error": repeated(operationOutcomeSourceSchema), "idempotency": singleton(operationIdempotencySourceSchema)},
	"execution":         {"retry": singleton(executionRetrySourceSchema), "concurrency": singleton(executionConcurrencySourceSchema), "retention": singleton(executionRetentionSourceSchema), "deduplication": singleton(executionDeduplicationSourceSchema)},
	"binding":           {"http": singleton(httpSourceSchema), "internal": singleton(internalSourceSchema), "mcp": singleton(mcpBindingSourceSchema)},
	"schedule":          {"trigger": singleton(scheduleTriggerSourceSchema), "invoke": singleton(scheduleInvokeSourceSchema), "catchup": singleton(scheduleCatchupSourceSchema)},
	"page":              {"action": repeated(pageActionSourceSchema), "param": repeated(pageParamSourceSchema)},
	"status_map":        {"status": repeated(statusMapStatusSourceSchema)},
	"form_dialog":       {"field": repeated(formDialogFieldSourceSchema)},
	"table_page":        {"column": repeated(tablePageColumnSourceSchema), "filter": repeated(tablePageFilterSourceSchema), "sort": repeated(tablePageSortSourceSchema), "group": repeated(tablePageGroupSourceSchema), "pagination": singleton(tablePagePaginationSourceSchema), "predicate": repeated(tablePagePredicateSourceSchema), "query": singleton(tablePageQuerySourceSchema), "action": repeated(tablePageActionSourceSchema), "stats": singleton(tablePageStatsSourceSchema), "row_detail": singleton(tablePageRowDetailSourceSchema), "row_action": singleton(tablePageRowActionSourceSchema), "export": singleton(tablePageExportSourceSchema), "toolbar": singleton(tablePageToolbarSourceSchema), "footer": singleton(tablePageSlotSourceSchema), "empty": singleton(tablePageSlotSourceSchema), "search": repeated(pageSearchSourceSchema)},
	"split_page":        {"sidebar": singleton(tablePageSlotSourceSchema), "detail": singleton(tablePageSlotSourceSchema), "sidebar_actions": singleton(tablePageSlotSourceSchema), "detail_header": singleton(tablePageSlotSourceSchema), "search": repeated(pageSearchSourceSchema)},
	"content_page":      {"content": singleton(contentPageSlotSourceSchema), "actions": singleton(contentPageSlotSourceSchema), "search": repeated(pageSearchSourceSchema)},
	"workspace_page":    {"tab": ordered(workspacePageTabSourceSchema), "stats": singleton(workspacePageStatsSourceSchema), "actions": singleton(contentPageSlotSourceSchema), "search": repeated(pageSearchSourceSchema)},
	"detail_page":       {"param": repeated(detailPageParamSourceSchema), "section": ordered(detailPageSectionSourceSchema), "action": ordered(detailPageActionSourceSchema), "actions": singleton(contentPageSlotSourceSchema), "table": ordered(detailPageTableSourceSchema), "search": repeated(pageSearchSourceSchema)},
	"mcp_server":        {"capability": repeated(mcpServerCapabilitySourceSchema)},
	"assistant":         {"implementation": singleton(assistantImplementationSourceSchema), "surface": singleton(assistantSurfaceSourceSchema)},
}

var authoredStructuralSchemas = map[string]*authoredBlockSchema{
	"workspace": sourceSchema("scenery.source.workspace", 0, []string{"managed_generated_roots"}, nil,
		map[string]authoredChildSchema{"implementation_root": repeated(implementationRootSourceSchema), "revision_input": repeated(revisionInputSourceSchema)}),
	"application": sourceSchema("scenery.source.application", 1, nil, nil, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"module":      sourceSchema("scenery.source.module", 1, []string{"source", "inputs"}, []string{"source"}, nil),
	"package":     sourceSchema("scenery.source.package", 1, nil, nil, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"input": sourceSchema("scenery.source.input", 1,
		[]string{"type", "phase", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "optional", "requires", "public"}, []string{"type"}, nil),
	"export": sourceSchema("scenery.source.export", 1, []string{"value"}, []string{"value"}, nil),
}

var authoredResourceSchemas = buildAuthoredResourceSchemas()

func buildAuthoredResourceSchemas() map[string]*authoredBlockSchema {
	schemas := make(map[string]*authoredBlockSchema, len(resourceSchemas))
	for kind, resource := range resourceSchemas {
		blockType := blockTypeForKind(kind)
		schemas[blockType] = sourceSchema(
			"scenery.source."+blockType,
			1,
			resource.Attributes,
			resource.Required,
			authoredResourceChildren[blockType],
		)
	}
	return schemas
}

func authoredResourceSourceSchema(blockType string) (*authoredBlockSchema, bool) {
	schema, ok := authoredResourceSchemas[blockType]
	return schema, ok
}

type SourceBlockSchema = authoredBlockSchema
type SourceAttributeSchema = authoredAttributeSchema
type SourceChildSchema = authoredChildSchema

// ResourceSourceSchemas returns one detached resource-schema catalog. Shared
// child schemas remain shared within the returned catalog, while no pointer or
// nested value aliases the canonical specification storage.
func ResourceSourceSchemas() map[string]*SourceBlockSchema {
	result := make(map[string]*SourceBlockSchema, len(authoredResourceSchemas))
	cloned := map[*authoredBlockSchema]*authoredBlockSchema{}
	for blockType, schema := range authoredResourceSchemas {
		result[blockType] = cloneSourceBlockSchema(schema, cloned)
	}
	return result
}

func StructuralSourceSchemas() map[string]*SourceBlockSchema {
	result := make(map[string]*SourceBlockSchema, len(authoredStructuralSchemas))
	cloned := map[*authoredBlockSchema]*authoredBlockSchema{}
	for name, schema := range authoredStructuralSchemas {
		result[name] = cloneSourceBlockSchema(schema, cloned)
	}
	return result
}

func ResourceSourceChildren() map[string]map[string]SourceChildSchema {
	result := make(map[string]map[string]SourceChildSchema, len(authoredResourceChildren))
	cloned := map[*authoredBlockSchema]*authoredBlockSchema{}
	for blockType, children := range authoredResourceChildren {
		childCopies := make(map[string]SourceChildSchema, len(children))
		for name, child := range children {
			child.Schema = cloneSourceBlockSchema(child.Schema, cloned)
			childCopies[name] = child
		}
		result[blockType] = childCopies
	}
	return result
}

func AuthoredEnumAllows(field SourceAttributeSchema, value string) bool {
	values, constrained := field.Constraints["enum"].([]string)
	if !constrained {
		return true
	}
	return slices.Contains(values, value)
}

func NamedSourceSchemas() map[string]*SourceBlockSchema {
	live := map[string]*SourceBlockSchema{
		"http":                     httpSourceSchema,
		"http_cookie":              httpCookieSourceSchema,
		"http_header":              httpHeaderSourceSchema,
		"http_multipart_part":      httpMultipartPartSourceSchema,
		"http_path_parameter":      httpPathParameterSourceSchema,
		"http_path_tail":           httpPathTailSourceSchema,
		"http_query_parameter":     httpQueryParameterSourceSchema,
		"http_response_cookie":     httpResponseCookieSourceSchema,
		"http_response_header":     httpResponseHeaderSourceSchema,
		"mcp":                      mcpBindingSourceSchema,
		"mcp_server_capability":    mcpServerCapabilitySourceSchema,
		"assistant_implementation": assistantImplementationSourceSchema,
		"assistant_surface":        assistantSurfaceSourceSchema,
		"operation_idempotency":    operationIdempotencySourceSchema,
	}
	result := make(map[string]*SourceBlockSchema, len(live))
	cloned := map[*authoredBlockSchema]*authoredBlockSchema{}
	for name, schema := range live {
		result[name] = cloneSourceBlockSchema(schema, cloned)
	}
	return result
}

func cloneSourceBlockSchema(schema *authoredBlockSchema, cloned map[*authoredBlockSchema]*authoredBlockSchema) *authoredBlockSchema {
	if schema == nil {
		return nil
	}
	if existing := cloned[schema]; existing != nil {
		return existing
	}
	copy := &authoredBlockSchema{
		Revision: schema.Revision, Labels: schema.Labels, LabelPattern: schema.LabelPattern, LabelPolicy: schema.LabelPolicy,
		Attributes: make(map[string]authoredAttributeSchema, len(schema.Attributes)), Required: make(map[string]bool, len(schema.Required)),
		Children: make(map[string]authoredChildSchema, len(schema.Children)), AllowUnknownAttributes: schema.AllowUnknownAttributes,
	}
	cloned[schema] = copy
	for name, attribute := range schema.Attributes {
		copy.Attributes[name] = cloneAuthoredAttributeSchema(attribute)
	}
	maps.Copy(copy.Required, schema.Required)
	for name, child := range schema.Children {
		child.Schema = cloneSourceBlockSchema(child.Schema, cloned)
		copy.Children[name] = child
	}
	if schema.DynamicAttribute != nil {
		dynamic := cloneAuthoredAttributeSchema(*schema.DynamicAttribute)
		copy.DynamicAttribute = &dynamic
	}
	return copy
}

func cloneAuthoredAttributeSchema(attribute authoredAttributeSchema) authoredAttributeSchema {
	attribute.Type = cloneMapValue(attribute.Type)
	attribute.Default = cloneSemanticValue(attribute.Default)
	attribute.Constraints = cloneMapValue(attribute.Constraints)
	return attribute
}
