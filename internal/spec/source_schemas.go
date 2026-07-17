package spec

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

	deploymentResourcesSourceSchema = sourceSchema("scenery.deployment.resources", 0,
		[]string{"cpu", "memory", "ephemeral_storage"}, nil, nil)
	deploymentListenerSourceSchema = sourceSchema("scenery.deployment.http-listener", 0,
		[]string{"host", "address", "port", "tls", "certificate", "secret", "http_versions", "platform_identity"}, []string{"host", "port"}, nil)
	deploymentModuleSourceSchema = sourceSchema("scenery.deployment.module", 0,
		[]string{"target", "inputs"}, []string{"target"}, nil)
	deploymentDataSourceSourceSchema = sourceSchema("scenery.deployment.data-source", 0,
		[]string{"target", "config"}, []string{"target"}, nil)
	deploymentServiceSourceSchema = sourceSchema("scenery.deployment.service", 0,
		[]string{"target", "replicas", "placement", "config"}, []string{"target"}, map[string]authoredChildSchema{"resources": singleton(deploymentResourcesSourceSchema)})
	deploymentHTTPGatewaySourceSchema = sourceSchema("scenery.deployment.http-gateway", 0,
		[]string{"target"}, []string{"target"}, map[string]authoredChildSchema{"listener": repeated(deploymentListenerSourceSchema)})
	deploymentProviderSourceSchema = sourceSchema("scenery.deployment.provider", 0,
		[]string{"target", "config"}, []string{"target"}, nil)
	deploymentSecretSourceSchema = sourceSchema("scenery.deployment.secret", 0,
		[]string{"target", "value", "store", "key"}, []string{"target"}, nil)

	typescriptRetrySourceSchema = sourceSchema("scenery.typescript-client.retry", 0,
		[]string{"policy", "maximum_attempts", "maximum_delay_milliseconds", "statuses"}, []string{"policy", "maximum_attempts"}, nil)
	typescriptReactSourceSchema = sourceSchema("scenery.typescript-client.react", 0,
		[]string{"tsconfig"}, []string{"tsconfig"}, nil)
	patchOperationSourceSchema = sourceSchema("scenery.patch.operation", 0, []string{"path", "value"}, []string{"path", "value"}, nil)

	serviceImplementationSourceSchema = sourceSchema("scenery.service.implementation", 0,
		[]string{"constructor", "adapter", "root", "package", "symbol"}, nil, nil)
	serviceDependencySourceSchema = sourceSchema("scenery.service.dependency", 1,
		[]string{"instance", "requires"}, []string{"instance"}, nil)
	serviceConfigSourceSchema    = sourceSchema("scenery.service.config", 0, nil, nil, nil)
	serviceClientSourceSchema    = sourceSchema("scenery.service.client", 1, []string{"binding"}, []string{"binding"}, nil)
	serviceLifecycleSourceSchema = sourceSchema("scenery.service.lifecycle", 0, []string{"start", "stop"}, nil, nil)

	recordFieldSourceSchema = sourceSchema("scenery.record.field", 1,
		[]string{"type", "wire_name", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "immutable", "deprecated", "replacement"}, []string{"type"}, nil)
	recordValidationSourceSchema = sourceSchema("scenery.record.validation", 1,
		[]string{"when", "code", "message", "path"}, []string{"when", "code", "message", "path"}, nil)
	enumValueSourceSchema    = sourceSchema("scenery.enum.value", 1, []string{"wire_value"}, nil, nil)
	unionVariantSourceSchema = sourceSchema("scenery.union.variant", 1, []string{"type", "wire_tag"}, []string{"type"}, nil)

	operationHandlerSourceSchema = sourceSchema("scenery.operation.handler", 0,
		[]string{"method", "adapter"}, []string{"method"}, nil)
	operationOutcomeSourceSchema     = sourceSchema("scenery.operation.outcome", 1, []string{"type"}, []string{"type"}, nil)
	operationIdempotencySourceSchema = sourceSchema("scenery.operation.idempotency", 0, []string{"mode", "key"}, []string{"mode"}, nil)

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

	cliContextSourceSchema  = sourceSchema("scenery.binding.cli.context", 1, []string{"from", "to"}, []string{"from", "to"}, nil)
	cliArgumentSourceSchema = sourceSchema("scenery.binding.cli.argument", 1, []string{"position", "to", "required"}, []string{"position", "to"}, nil)
	cliFlagSourceSchema     = sourceSchema("scenery.binding.cli.flag", 1, []string{"name", "short", "to", "required"}, []string{"name", "to"}, nil)
	cliOutputSourceSchema   = sourceSchema("scenery.binding.cli.output", 0, []string{"codec", "from"}, []string{"codec", "from"}, nil)
	cliOutcomeSourceSchema  = sourceSchema("scenery.binding.cli.outcome", 1, []string{"when", "exit"}, []string{"when", "exit"},
		map[string]authoredChildSchema{"stdout": singleton(cliOutputSourceSchema), "stderr": singleton(cliOutputSourceSchema)})
	cliSourceSchema = sourceSchema("scenery.binding.cli", 0, []string{"command"}, []string{"command"},
		map[string]authoredChildSchema{"context": repeated(cliContextSourceSchema), "argument": repeated(cliArgumentSourceSchema), "flag": repeated(cliFlagSourceSchema), "outcome": repeated(cliOutcomeSourceSchema)})

	eventMapSourceSchema     = sourceSchema("scenery.binding.event.map", 0, []string{"from", "to"}, []string{"from", "to"}, nil)
	eventRetrySourceSchema   = sourceSchema("scenery.event.broker-retry", 0, []string{"attempts", "backoff"}, []string{"attempts", "backoff"}, nil)
	eventBindingSourceSchema = sourceSchema("scenery.binding.event", 0,
		[]string{"direction", "bus", "channel", "contract", "guarantee", "ordering_key", "deduplication_key", "dead_letter_channel"},
		[]string{"direction", "bus", "channel", "contract", "guarantee"}, map[string]authoredChildSchema{"map": repeated(eventMapSourceSchema), "broker_retry": singleton(eventRetrySourceSchema)})

	scheduleTriggerSourceSchema = sourceSchema("scenery.schedule.trigger", 0, []string{"cron", "every", "at", "calendar", "timezone"}, nil, nil)
	scheduleInvokeSourceSchema  = sourceSchema("scenery.schedule.invoke", 0,
		[]string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, []string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, nil)
	scheduleCatchupSourceSchema   = sourceSchema("scenery.schedule.catchup", 0, []string{"maximum_age"}, []string{"maximum_age"}, nil)
	eventEmissionFromSourceSchema = sourceSchema("scenery.event-emission.from", 0, []string{"operation", "when", "payload"}, []string{"operation", "when", "payload"}, nil)

	entityMappingSourceSchema = sourceSchema("scenery.entity.mapping", 0, []string{"relation", "schema"}, []string{"relation"}, nil)
	entityDefaultSourceSchema = sourceSchema("scenery.entity.field-default", 0, []string{"strategy", "value"}, []string{"strategy"}, nil)
	entityFieldSourceSchema   = sourceSchema("scenery.entity.field", 1,
		[]string{"column", "primary_key", "tenant_key", "immutable"}, []string{"column"}, map[string]authoredChildSchema{"default": singleton(entityDefaultSourceSchema)})
	entityIndexSourceSchema      = sourceSchema("scenery.entity.index", 1, []string{"fields", "unique", "method"}, []string{"fields"}, nil)
	entityUniqueSourceSchema     = sourceSchema("scenery.entity.unique", 1, []string{"fields"}, []string{"fields"}, nil)
	entityForeignKeySourceSchema = sourceSchema("scenery.entity.foreign-key", 1,
		[]string{"fields", "target", "target_fields", "on_delete"}, []string{"fields", "target", "target_fields"}, nil)
	entityDeletionSourceSchema     = sourceSchema("scenery.entity.deletion", 0, []string{"strategy", "field"}, []string{"strategy"}, nil)
	viewImplementationSourceSchema = sourceSchema("scenery.view.implementation", 0, []string{"kind", "file", "name"}, []string{"kind", "file", "name"}, nil)
	crudExecutionSourceSchema      = sourceSchema("scenery.crud.execution", 0, []string{"mode", "timeout"}, []string{"mode", "timeout"}, nil)
	crudListSourceSchema           = sourceSchema("scenery.crud.list", 0, []string{"filters", "sorts", "default_sort", "max_page_size"}, nil, nil)
	crudHTTPSourceSchema           = sourceSchema("scenery.crud.http", 0,
		[]string{"path", "codec_profile", "gateway", "authentication", "authorization", "pipeline"},
		[]string{"path", "codec_profile", "gateway", "authentication", "authorization", "pipeline"}, nil)
	crudInternalSourceSchema = sourceSchema("scenery.crud.internal", 0,
		[]string{"exposure", "authentication", "authorization", "pipeline"}, nil, nil)
	crudExtensionSourceSchema   = sourceSchema("scenery.crud.extension", 1, []string{"config"}, nil, nil)
	pageActionSourceSchema      = sourceSchema("scenery.page.action", 1, []string{"invoke"}, []string{"invoke"}, nil)
	tablePageColumnSourceSchema = sourceSchema("scenery.table-page.column", 1,
		[]string{"label", "appearance", "component"}, nil, nil)
	tablePageFilterSourceSchema = sourceSchema("scenery.table-page.filter", 1,
		[]string{"label", "component"}, nil, nil)
	tablePageSortSourceSchema = sourceSchema("scenery.table-page.sort", 1,
		[]string{"label", "default"}, nil, nil)
	tablePageSlotSourceSchema = sourceSchema("scenery.table-page.slot", 0,
		[]string{"component"}, []string{"component"}, nil)
)

func init() {
	serviceConfigSourceSchema.AllowUnknownAttributes = true
	serviceConfigSourceSchema.DynamicAttribute = &authoredAttributeSchema{
		Type: map[string]any{"$ref": "scenery.value", "type_source": "package_input"}, Phase: "compile", InputPhaseSource: "package_input", RevisionSource: "package_input",
		SensitivitySource: "package_input", Patchable: true, DefaultSource: "none", Constraints: map[string]any{"name_pattern": "^[a-z][a-z0-9_]*$"}, MetadataStatus: "dynamic",
	}
}

var authoredResourceChildren = map[string]map[string]authoredChildSchema{
	"go_target":         {"test": singleton(goTargetTestSourceSchema)},
	"authorization":     {"rule": ordered(authorizationRuleSourceSchema)},
	"pipeline":          {"step": ordered(pipelineStepSourceSchema)},
	"deployment":        {"module": repeated(deploymentModuleSourceSchema), "data_source": repeated(deploymentDataSourceSourceSchema), "service": repeated(deploymentServiceSourceSchema), "http_gateway": repeated(deploymentHTTPGatewaySourceSchema), "provider": repeated(deploymentProviderSourceSchema), "secret": repeated(deploymentSecretSourceSchema)},
	"typescript_client": {"retry": singleton(typescriptRetrySourceSchema), "react": singleton(typescriptReactSourceSchema)},
	"patch":             {"expect": repeated(patchOperationSourceSchema), "set": repeated(patchOperationSourceSchema)},
	"service":           {"implementation": singleton(serviceImplementationSourceSchema), "dependency": repeated(serviceDependencySourceSchema), "config": singleton(serviceConfigSourceSchema), "client": repeated(serviceClientSourceSchema), "lifecycle": singleton(serviceLifecycleSourceSchema)},
	"record":            {"field": repeated(recordFieldSourceSchema), "validation": repeated(recordValidationSourceSchema)},
	"enum":              {"value": repeated(enumValueSourceSchema)},
	"union":             {"variant": repeated(unionVariantSourceSchema)},
	"operation":         {"handler": singleton(operationHandlerSourceSchema), "result": repeated(operationOutcomeSourceSchema), "error": repeated(operationOutcomeSourceSchema), "idempotency": singleton(operationIdempotencySourceSchema)},
	"execution":         {"retry": singleton(executionRetrySourceSchema), "concurrency": singleton(executionConcurrencySourceSchema), "retention": singleton(executionRetentionSourceSchema), "deduplication": singleton(executionDeduplicationSourceSchema)},
	"binding":           {"http": singleton(httpSourceSchema), "internal": singleton(internalSourceSchema), "cli": singleton(cliSourceSchema), "event": singleton(eventBindingSourceSchema)},
	"schedule":          {"trigger": singleton(scheduleTriggerSourceSchema), "invoke": singleton(scheduleInvokeSourceSchema), "catchup": singleton(scheduleCatchupSourceSchema)},
	"event_emission":    {"broker_retry": singleton(eventRetrySourceSchema), "from": singleton(eventEmissionFromSourceSchema)},
	"entity":            {"mapping": singleton(entityMappingSourceSchema), "field": repeated(entityFieldSourceSchema), "index": repeated(entityIndexSourceSchema), "unique": repeated(entityUniqueSourceSchema), "foreign_key": repeated(entityForeignKeySourceSchema), "deletion": singleton(entityDeletionSourceSchema)},
	"view":              {"implementation": singleton(viewImplementationSourceSchema)},
	"crud":              {"execution": singleton(crudExecutionSourceSchema), "list": singleton(crudListSourceSchema), "http": singleton(crudHTTPSourceSchema), "internal": singleton(crudInternalSourceSchema), "extension": repeated(crudExtensionSourceSchema)},
	"page":              {"action": repeated(pageActionSourceSchema)},
	"table_page":        {"column": repeated(tablePageColumnSourceSchema), "filter": repeated(tablePageFilterSourceSchema), "sort": repeated(tablePageSortSourceSchema), "toolbar": singleton(tablePageSlotSourceSchema), "empty": singleton(tablePageSlotSourceSchema)},
	"split_page":        {"sidebar": singleton(tablePageSlotSourceSchema), "detail": singleton(tablePageSlotSourceSchema), "sidebar_actions": singleton(tablePageSlotSourceSchema), "detail_header": singleton(tablePageSlotSourceSchema)},
}

var authoredStructuralSchemas = map[string]*authoredBlockSchema{
	"workspace": sourceSchema("scenery.source.workspace", 0, []string{"managed_generated_roots"}, nil,
		map[string]authoredChildSchema{"implementation_root": repeated(implementationRootSourceSchema), "revision_input": repeated(revisionInputSourceSchema)}),
	"application": sourceSchema("scenery.source.application", 1, nil, nil, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"module":      sourceSchema("scenery.source.module", 1, []string{"source", "inputs"}, []string{"source"}, nil),
	"package":     sourceSchema("scenery.source.package", 1, nil, nil, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"input": sourceSchema("scenery.source.input", 1,
		[]string{"type", "phase", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "optional", "requires", "deployment_bindable"}, []string{"type"}, nil),
	"export": sourceSchema("scenery.source.export", 1, []string{"value", "patchable"}, []string{"value"}, nil),
}

func authoredResourceSourceSchema(blockType string) (*authoredBlockSchema, bool) {
	schema, ok := resourceSchemas[kindForBlock(blockType)]
	if !ok {
		return nil, false
	}
	children := authoredResourceChildren[blockType]
	return sourceSchema("scenery.source."+blockType, 1, schema.Attributes, schema.Required, children), true
}

type SourceBlockSchema = authoredBlockSchema
type SourceAttributeSchema = authoredAttributeSchema
type SourceChildSchema = authoredChildSchema

func ResourceSourceSchema(blockType string) (*SourceBlockSchema, bool) {
	schema, ok := authoredResourceSourceSchema(blockType)
	if !ok {
		return nil, false
	}
	return cloneSourceBlockSchema(schema, map[*authoredBlockSchema]*authoredBlockSchema{}), true
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
	for _, allowed := range values {
		if value == allowed {
			return true
		}
	}
	return false
}

func NamedSourceSchemas() map[string]*SourceBlockSchema {
	live := map[string]*SourceBlockSchema{
		"deployment_listener":   deploymentListenerSourceSchema,
		"http":                  httpSourceSchema,
		"http_cookie":           httpCookieSourceSchema,
		"http_header":           httpHeaderSourceSchema,
		"http_multipart_part":   httpMultipartPartSourceSchema,
		"http_path_parameter":   httpPathParameterSourceSchema,
		"http_path_tail":        httpPathTailSourceSchema,
		"http_query_parameter":  httpQueryParameterSourceSchema,
		"http_response_cookie":  httpResponseCookieSourceSchema,
		"http_response_header":  httpResponseHeaderSourceSchema,
		"operation_idempotency": operationIdempotencySourceSchema,
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
	for name, required := range schema.Required {
		copy.Required[name] = required
	}
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
