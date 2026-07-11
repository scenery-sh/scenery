package vnext

import "fmt"

type authoredBlockSchema struct {
	Revision               string
	Labels                 int
	Attributes             map[string]bool
	Required               map[string]bool
	Children               map[string]authoredChildSchema
	AllowUnknownAttributes bool
}

type authoredChildSchema struct {
	Schema     *authoredBlockSchema
	Repeatable bool
}

func sourceSchema(revision string, labels int, attributes, required []string, children map[string]authoredChildSchema) *authoredBlockSchema {
	schema := &authoredBlockSchema{Revision: revision, Labels: labels, Attributes: map[string]bool{}, Required: map[string]bool{}, Children: children}
	for _, name := range attributes {
		schema.Attributes[name] = true
	}
	for _, name := range required {
		schema.Required[name] = true
	}
	if schema.Children == nil {
		schema.Children = map[string]authoredChildSchema{}
	}
	return schema
}

func repeated(schema *authoredBlockSchema) authoredChildSchema {
	return authoredChildSchema{Schema: schema, Repeatable: true}
}

func singleton(schema *authoredBlockSchema) authoredChildSchema {
	return authoredChildSchema{Schema: schema}
}

var (
	goContractSourceSchema         = sourceSchema("scenery.source.go-contract/v1", 0, []string{"import_path"}, []string{"import_path"}, nil)
	implementationRootSourceSchema = sourceSchema("scenery.source.workspace.implementation-root/v1", 1,
		[]string{"path", "revision_include", "revision_exclude"}, []string{"path", "revision_include"}, nil)
	revisionInputSourceSchema = sourceSchema("scenery.source.workspace.revision-input/v1", 1,
		[]string{"paths", "optional"}, []string{"paths"}, nil)

	authorizationRuleSourceSchema = sourceSchema("scenery.authorization.rule/v1", 1, []string{"allow", "deny"}, nil, nil)
	pipelineStepSourceSchema      = sourceSchema("scenery.pipeline.step/v1", 1, []string{"use"}, []string{"use"}, nil)
	goTargetTestSourceSchema      = sourceSchema("scenery.go-target.test/v1", 0, []string{"additional_build_tags", "packages"}, nil, nil)

	deploymentResourcesSourceSchema = sourceSchema("scenery.deployment.resources/v1", 0,
		[]string{"cpu", "memory", "ephemeral_storage"}, nil, nil)
	deploymentListenerSourceSchema = sourceSchema("scenery.deployment.http-listener/v1", 0,
		[]string{"host", "address", "port", "tls", "certificate", "secret", "http_versions", "platform_identity"}, []string{"host", "port"}, nil)
	deploymentModuleSourceSchema = sourceSchema("scenery.deployment.module/v1", 0,
		[]string{"target", "inputs"}, []string{"target"}, nil)
	deploymentDataSourceSourceSchema = sourceSchema("scenery.deployment.data-source/v1", 0,
		[]string{"target", "config"}, []string{"target"}, nil)
	deploymentServiceSourceSchema = sourceSchema("scenery.deployment.service/v1", 0,
		[]string{"target", "replicas", "placement", "config"}, []string{"target"}, map[string]authoredChildSchema{"resources": singleton(deploymentResourcesSourceSchema)})
	deploymentHTTPGatewaySourceSchema = sourceSchema("scenery.deployment.http-gateway/v1", 0,
		[]string{"target"}, []string{"target"}, map[string]authoredChildSchema{"listener": repeated(deploymentListenerSourceSchema)})
	deploymentProviderSourceSchema = sourceSchema("scenery.deployment.provider/v1", 0,
		[]string{"target", "config"}, []string{"target"}, nil)
	deploymentSecretSourceSchema = sourceSchema("scenery.deployment.secret/v1", 0,
		[]string{"target", "value", "store", "key"}, []string{"target"}, nil)

	typescriptRetrySourceSchema = sourceSchema("scenery.typescript-client.retry/v1", 0,
		[]string{"policy", "maximum_attempts", "maximum_delay_milliseconds", "statuses"}, []string{"policy", "maximum_attempts"}, nil)
	patchOperationSourceSchema = sourceSchema("scenery.patch.operation/v1", 0, []string{"path", "value"}, []string{"path", "value"}, nil)

	serviceImplementationSourceSchema = sourceSchema("scenery.service.implementation/v1", 0,
		[]string{"constructor", "adapter", "root", "package", "symbol"}, nil, nil)
	serviceDependencySourceSchema = sourceSchema("scenery.service.dependency/v1", 1,
		[]string{"instance", "requires"}, []string{"instance"}, nil)
	serviceConfigSourceSchema    = sourceSchema("scenery.service.config/v1", 0, nil, nil, nil)
	serviceClientSourceSchema    = sourceSchema("scenery.service.client/v1", 1, []string{"binding"}, []string{"binding"}, nil)
	serviceLifecycleSourceSchema = sourceSchema("scenery.service.lifecycle/v1", 0, []string{"start", "stop"}, nil, nil)

	recordFieldSourceSchema = sourceSchema("scenery.record.field/v1", 1,
		[]string{"type", "wire_name", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "immutable", "deprecated", "replacement"}, []string{"type"}, nil)
	recordValidationSourceSchema = sourceSchema("scenery.record.validation/v1", 1,
		[]string{"when", "code", "message", "path"}, []string{"when", "code", "message", "path"}, nil)
	enumValueSourceSchema    = sourceSchema("scenery.enum.value/v1", 1, []string{"wire_value"}, nil, nil)
	unionVariantSourceSchema = sourceSchema("scenery.union.variant/v1", 1, []string{"type", "wire_tag"}, []string{"type"}, nil)

	operationHandlerSourceSchema = sourceSchema("scenery.operation.handler/v1", 0,
		[]string{"method", "adapter", "legacy_symbol", "legacy_file", "legacy_receiver", "legacy_has_payload"}, []string{"method"}, nil)
	operationOutcomeSourceSchema     = sourceSchema("scenery.operation.outcome/v1", 1, []string{"type"}, []string{"type"}, nil)
	operationIdempotencySourceSchema = sourceSchema("scenery.operation.idempotency/v1", 0, []string{"mode", "key"}, []string{"mode"}, nil)

	executionRetrySourceSchema = sourceSchema("scenery.execution.retry/v1", 0,
		[]string{"strategy", "initial", "factor", "maximum", "jitter"}, []string{"strategy"}, nil)
	executionConcurrencySourceSchema   = sourceSchema("scenery.execution.concurrency/v1", 0, []string{"key", "limit"}, []string{"key", "limit"}, nil)
	executionRetentionSourceSchema     = sourceSchema("scenery.execution.retention/v1", 0, []string{"success", "failure"}, []string{"success", "failure"}, nil)
	executionDeduplicationSourceSchema = sourceSchema("scenery.execution.deduplication/v1", 0, []string{"retention", "conflict"}, []string{"retention", "conflict"}, nil)

	httpPathParameterSourceSchema  = sourceSchema("scenery.binding.http.path-parameter/v1", 1, []string{"to"}, []string{"to"}, nil)
	httpValueParameterSourceSchema = sourceSchema("scenery.binding.http.value-parameter/v1", 1, []string{"to", "encoding"}, []string{"to"}, nil)
	httpContextSourceSchema        = sourceSchema("scenery.binding.http.context/v1", 1, []string{"from", "to"}, []string{"from", "to"}, nil)
	httpMultipartPartSourceSchema  = sourceSchema("scenery.binding.http.multipart-part/v1", 1,
		[]string{"to", "kind", "media_types", "max_bytes", "multiple", "retain_filename"}, []string{"to", "kind"}, nil)
	httpBodySourceSchema = sourceSchema("scenery.binding.http.body/v1", 0,
		[]string{"codec", "to", "from", "include", "except", "accepted_media_types", "produced_media_types", "content_encodings", "max_compressed_bytes", "max_decompressed_bytes", "max_parts"}, []string{"codec"},
		map[string]authoredChildSchema{"part": repeated(httpMultipartPartSourceSchema)})
	httpResponseBodySourceSchema = sourceSchema("scenery.binding.http.response-body/v1", 0,
		[]string{"codec", "from", "produced_media_types", "content_encodings", "max_compressed_bytes", "max_decompressed_bytes"}, []string{"codec", "from"}, nil)
	httpResponseHeaderSourceSchema = sourceSchema("scenery.binding.http.response-header/v1", 1,
		[]string{"from", "encoding"}, []string{"from"}, nil)
	httpResponseCookieSourceSchema = sourceSchema("scenery.binding.http.response-cookie/v1", 1,
		[]string{"from", "path", "domain", "max_age", "expires", "secure", "http_only", "same_site"}, []string{"from"}, nil)
	httpResponseSourceSchema = sourceSchema("scenery.binding.http.response/v1", 1,
		[]string{"when", "status"}, []string{"when", "status"}, map[string]authoredChildSchema{"header": repeated(httpResponseHeaderSourceSchema), "cookie": repeated(httpResponseCookieSourceSchema), "body": singleton(httpResponseBodySourceSchema)})
	httpSourceSchema = sourceSchema("scenery.binding.http/v1", 0,
		[]string{"method", "path", "codec_profile", "guarantee", "request_limit", "response_limit", "timeouts"}, []string{"method", "path", "codec_profile"},
		map[string]authoredChildSchema{
			"path_parameter": repeated(httpPathParameterSourceSchema), "query_parameter": repeated(httpValueParameterSourceSchema),
			"header": repeated(httpValueParameterSourceSchema), "cookie": repeated(httpValueParameterSourceSchema),
			"context": repeated(httpContextSourceSchema), "body": singleton(httpBodySourceSchema), "response": repeated(httpResponseSourceSchema),
		})
	internalSourceSchema = sourceSchema("scenery.binding.internal/v1", 0, []string{"visibility", "principal"}, []string{"visibility", "principal"}, nil)

	cliContextSourceSchema  = sourceSchema("scenery.binding.cli.context/v1", 1, []string{"from", "to"}, []string{"from", "to"}, nil)
	cliArgumentSourceSchema = sourceSchema("scenery.binding.cli.argument/v1", 1, []string{"position", "to", "required"}, []string{"position", "to"}, nil)
	cliFlagSourceSchema     = sourceSchema("scenery.binding.cli.flag/v1", 1, []string{"name", "short", "to", "required"}, []string{"name", "to"}, nil)
	cliOutputSourceSchema   = sourceSchema("scenery.binding.cli.output/v1", 0, []string{"codec", "from"}, []string{"codec", "from"}, nil)
	cliOutcomeSourceSchema  = sourceSchema("scenery.binding.cli.outcome/v1", 1, []string{"when", "exit"}, []string{"when", "exit"},
		map[string]authoredChildSchema{"stdout": singleton(cliOutputSourceSchema), "stderr": singleton(cliOutputSourceSchema)})
	cliSourceSchema = sourceSchema("scenery.binding.cli/v1", 0, []string{"command"}, []string{"command"},
		map[string]authoredChildSchema{"context": repeated(cliContextSourceSchema), "argument": repeated(cliArgumentSourceSchema), "flag": repeated(cliFlagSourceSchema), "outcome": repeated(cliOutcomeSourceSchema)})

	eventMapSourceSchema     = sourceSchema("scenery.binding.event.map/v1", 0, []string{"from", "to"}, []string{"from", "to"}, nil)
	eventRetrySourceSchema   = sourceSchema("scenery.event.broker-retry/v1", 0, []string{"attempts", "backoff"}, []string{"attempts", "backoff"}, nil)
	eventBindingSourceSchema = sourceSchema("scenery.binding.event/v1", 0,
		[]string{"direction", "bus", "channel", "contract", "guarantee", "ordering_key", "deduplication_key", "dead_letter_channel"},
		[]string{"direction", "bus", "channel", "contract", "guarantee"}, map[string]authoredChildSchema{"map": repeated(eventMapSourceSchema), "broker_retry": singleton(eventRetrySourceSchema)})

	scheduleTriggerSourceSchema = sourceSchema("scenery.schedule.trigger/v1", 0, []string{"cron", "every", "at", "calendar", "timezone"}, nil, nil)
	scheduleInvokeSourceSchema  = sourceSchema("scenery.schedule.invoke/v1", 0,
		[]string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, []string{"operation", "execution", "identity", "authorization", "pipeline", "input"}, nil)
	scheduleCatchupSourceSchema   = sourceSchema("scenery.schedule.catchup/v1", 0, []string{"maximum_age"}, []string{"maximum_age"}, nil)
	eventEmissionFromSourceSchema = sourceSchema("scenery.event-emission.from/v1", 0, []string{"operation", "when", "payload"}, []string{"operation", "when", "payload"}, nil)

	entityMappingSourceSchema = sourceSchema("scenery.entity.mapping/v1", 0, []string{"relation", "schema"}, []string{"relation"}, nil)
	entityDefaultSourceSchema = sourceSchema("scenery.entity.field-default/v1", 0, []string{"strategy", "value"}, []string{"strategy"}, nil)
	entityFieldSourceSchema   = sourceSchema("scenery.entity.field/v1", 1,
		[]string{"column", "primary_key", "tenant_key", "immutable"}, []string{"column"}, map[string]authoredChildSchema{"default": singleton(entityDefaultSourceSchema)})
	entityIndexSourceSchema      = sourceSchema("scenery.entity.index/v1", 1, []string{"fields", "unique", "method"}, []string{"fields"}, nil)
	entityUniqueSourceSchema     = sourceSchema("scenery.entity.unique/v1", 1, []string{"fields"}, []string{"fields"}, nil)
	entityForeignKeySourceSchema = sourceSchema("scenery.entity.foreign-key/v1", 1,
		[]string{"fields", "target", "target_fields", "on_delete"}, []string{"fields", "target", "target_fields"}, nil)
	entityDeletionSourceSchema     = sourceSchema("scenery.entity.deletion/v1", 0, []string{"strategy", "field"}, []string{"strategy"}, nil)
	viewImplementationSourceSchema = sourceSchema("scenery.view.implementation/v1", 0, []string{"kind", "file", "name"}, []string{"kind", "file", "name"}, nil)
	crudExecutionSourceSchema      = sourceSchema("scenery.crud.execution/v1", 0, []string{"mode", "timeout"}, []string{"mode", "timeout"}, nil)
	crudHTTPSourceSchema           = sourceSchema("scenery.crud.http/v1", 0,
		[]string{"path", "codec_profile", "gateway", "authentication", "authorization", "pipeline"},
		[]string{"path", "codec_profile", "gateway", "authentication", "authorization", "pipeline"}, nil)
	crudInternalSourceSchema = sourceSchema("scenery.crud.internal/v1", 0,
		[]string{"exposure", "authentication", "authorization", "pipeline"}, nil, nil)
	crudExtensionSourceSchema = sourceSchema("scenery.crud.extension/v1", 1, []string{"config"}, nil, nil)
	pageActionSourceSchema    = sourceSchema("scenery.page.action/v1", 1, []string{"invoke"}, []string{"invoke"}, nil)
)

func init() {
	serviceConfigSourceSchema.AllowUnknownAttributes = true
}

var authoredResourceChildren = map[string]map[string]authoredChildSchema{
	"go_target":         {"test": singleton(goTargetTestSourceSchema)},
	"authorization":     {"rule": repeated(authorizationRuleSourceSchema)},
	"pipeline":          {"step": repeated(pipelineStepSourceSchema)},
	"deployment":        {"module": repeated(deploymentModuleSourceSchema), "data_source": repeated(deploymentDataSourceSourceSchema), "service": repeated(deploymentServiceSourceSchema), "http_gateway": repeated(deploymentHTTPGatewaySourceSchema), "provider": repeated(deploymentProviderSourceSchema), "secret": repeated(deploymentSecretSourceSchema)},
	"typescript_client": {"retry": singleton(typescriptRetrySourceSchema)},
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
	"crud":              {"execution": singleton(crudExecutionSourceSchema), "http": singleton(crudHTTPSourceSchema), "internal": singleton(crudInternalSourceSchema), "extension": repeated(crudExtensionSourceSchema)},
	"page":              {"action": repeated(pageActionSourceSchema)},
}

var authoredStructuralSchemas = map[string]*authoredBlockSchema{
	"language": sourceSchema("scenery.source.language/v1", 0, []string{"edition", "require_profiles"}, []string{"edition"}, nil),
	"workspace": sourceSchema("scenery.source.workspace/v1", 0, []string{"managed_generated_roots"}, nil,
		map[string]authoredChildSchema{"implementation_root": repeated(implementationRootSourceSchema), "revision_input": repeated(revisionInputSourceSchema)}),
	"application": sourceSchema("scenery.source.application/v1", 1, []string{"version"}, []string{"version"}, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"module":      sourceSchema("scenery.source.module/v1", 1, []string{"source", "version", "inputs"}, []string{"source"}, nil),
	"package":     sourceSchema("scenery.source.package/v1", 1, []string{"version", "scenery_version"}, []string{"version", "scenery_version"}, map[string]authoredChildSchema{"go_contract": singleton(goContractSourceSchema)}),
	"input": sourceSchema("scenery.source.input/v1", 1,
		[]string{"type", "phase", "default", "minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items", "sensitive", "optional", "requires", "deployment_bindable"}, []string{"type"}, nil),
	"export": sourceSchema("scenery.source.export/v1", 1, []string{"value", "patchable"}, []string{"value"}, nil),
}

func authoredResourceSourceSchema(blockType string) (*authoredBlockSchema, bool) {
	semantic, ok := resourceSchemas[kindForBlock(blockType)]
	if !ok {
		return nil, false
	}
	children := authoredResourceChildren[blockType]
	attributes := make([]string, 0, len(semantic.Allowed))
	for _, name := range semantic.Allowed {
		if _, nested := children[name]; !nested {
			attributes = append(attributes, name)
		}
	}
	return sourceSchema("scenery.source."+blockType+"/v1", 1, attributes, semantic.Required, children), true
}

func validateAuthoredBlockSchemas(sources []*Source, packageScope bool) []Diagnostic {
	var diagnostics []Diagnostic
	for _, source := range sources {
		for _, block := range source.Blocks {
			schema, ok := authoredStructuralSchemas[block.Type]
			allowedStructural := block.Type == "module" || !packageScope && (block.Type == "language" || block.Type == "workspace" || block.Type == "application") || packageScope && (block.Type == "package" || block.Type == "input" || block.Type == "export")
			if !ok || !allowedStructural {
				schema, ok = authoredResourceSourceSchema(block.Type)
			}
			if !ok {
				continue
			}
			diagnostics = append(diagnostics, validateAuthoredBlock(block, schema)...)
		}
	}
	return diagnostics
}

func validateAuthoredBlock(block *Block, schema *authoredBlockSchema) []Diagnostic {
	var diagnostics []Diagnostic
	add := func(code, message string, target *Block) {
		rng := target.Range
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message + " (schema " + schema.Revision + ")", Range: &rng})
	}
	if len(block.Labels) != schema.Labels {
		add("SCN1011", fmt.Sprintf("%s requires exactly %d labels; found %d", block.Type, schema.Labels, len(block.Labels)), block)
	}
	for name := range block.Attributes {
		if _, expectsBlock := schema.Children[name]; expectsBlock {
			add("SCN1012", "field "+name+" must be authored as a block", block)
		} else if !schema.AllowUnknownAttributes && !schema.Attributes[name] {
			add("SCN1012", "unknown attribute "+name+" in "+block.Type, block)
		}
	}
	counts := map[string]int{}
	labels := map[string]map[string]bool{}
	for _, child := range block.Blocks {
		rule, ok := schema.Children[child.Type]
		if !ok {
			add("SCN1013", "unknown nested block "+child.Type+" in "+block.Type, child)
			continue
		}
		counts[child.Type]++
		if !rule.Repeatable && counts[child.Type] > 1 {
			add("SCN1014", "duplicate singleton block "+child.Type+" in "+block.Type, child)
		}
		if rule.Repeatable && rule.Schema.Labels > 0 && len(child.Labels) == rule.Schema.Labels {
			key := fmt.Sprint(child.Labels)
			if labels[child.Type] == nil {
				labels[child.Type] = map[string]bool{}
			}
			if labels[child.Type][key] {
				add("SCN1014", "duplicate "+child.Type+" labels in "+block.Type, child)
			}
			labels[child.Type][key] = true
		}
		diagnostics = append(diagnostics, validateAuthoredBlock(child, rule.Schema)...)
	}
	for name := range schema.Required {
		_, attribute := block.Attributes[name]
		if !attribute && counts[name] == 0 {
			add("SCN1015", "missing required field "+name+" in "+block.Type, block)
		}
	}
	return diagnostics
}

func validateMigrationAuthoredSchema(source *Source) []Diagnostic {
	if source == nil || len(source.Blocks) != 1 || source.Blocks[0].Type != "migration" {
		return nil
	}
	gateway := sourceSchema("scenery.migration.legacy-gateway/v1", 1, []string{"target"}, []string{"target"}, nil)
	legacy := sourceSchema("scenery.migration.legacy-service/v1", 1, []string{"package", "namespace", "target"}, []string{"package"}, nil)
	shadow := sourceSchema("scenery.migration.shadow-service/v1", 1, []string{"package", "namespace", "module", "target", "legacy_target", "active"}, []string{"package", "module", "active"}, nil)
	native := sourceSchema("scenery.migration.native-service/v1", 1, []string{"module"}, []string{"module"}, nil)
	migration := sourceSchema("scenery.migration/v1", 0, []string{"frontend", "legacy_config"}, []string{"frontend"}, map[string]authoredChildSchema{
		"legacy_gateway": repeated(gateway), "legacy_service": repeated(legacy), "shadow_service": repeated(shadow), "native_service": repeated(native),
	})
	return validateAuthoredBlock(source.Blocks[0], migration)
}
