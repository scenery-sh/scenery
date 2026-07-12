package vnext

import (
	"sort"
	"strings"
)

type resourceSchema struct {
	RevisionDomain string
	Required       []string
	Attributes     []string
	CanonicalOnly  map[string]string
}

type dynamicRevisionDomain struct {
	SchemaField string
	NameField   string
	DomainField string
}

type resourceConditionalRequirement struct {
	Field    string
	Equals   any
	Required []string
}

var resourceSchemas = map[string]resourceSchema{
	"scenery.go-module/v1":         {RevisionDomain: "implementation", Required: []string{"root", "import_path"}, Attributes: []string{"root", "import_path"}},
	"scenery.go-toolchain/v1":      {RevisionDomain: "implementation", Required: []string{"version"}, Attributes: []string{"version", "experiments"}},
	"scenery.go-target/v1":         {RevisionDomain: "implementation", Required: []string{"role", "toolchain", "module", "packages"}, Attributes: []string{"role", "platform", "toolchain", "module", "packages", "cgo", "extends", "inherits", "build_tags", "go_flags", "environment", "goos", "goarch", "architecture_features", "native_input", "native_inputs", "verify_by_default"}},
	"scenery.http-gateway/v1":      {"contract", []string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded"}, []string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded", "request_limit", "response_limit", "timeouts"}, nil},
	"scenery.authentication/v1":    {"contract", []string{"provider", "scheme"}, []string{"provider", "scheme", "config"}, nil},
	"scenery.authorization/v1":     {"contract", []string{"principal"}, []string{"principal", "strategy"}, nil},
	"scenery.workload-identity/v1": {"contract", []string{"issuer", "principal_type", "claims"}, []string{"issuer", "principal_type", "claims"}, nil},
	"scenery.pipeline/v1":          {"contract", nil, nil, nil},
	"scenery.provider/v1": {"contract", []string{"source", "version"}, []string{"source", "version", "config"}, map[string]string{
		"locked_version": "implementation", "locked_integrity": "implementation", "compile_descriptor_digest": "contract", "runtime_abi": "implementation",
		"deployment_abi": "deployment", "migration_abi": "implementation", "config_schema": "contract", "capabilities": "contract", "instance_kinds": "contract",
	}},
	"scenery.data-source/v1": {"contract", []string{"provider", "lifecycle"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.execution-engine/v1": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.event-bus/v1": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.secret-store/v1": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.secret/v1":            {"deployment", []string{"store", "key"}, []string{"store", "key"}, nil},
	"scenery.deployment/v1":        {"deployment", []string{"environment"}, []string{"environment", "fixture_policy"}, nil},
	"scenery.typescript-client/v1": {"implementation", []string{"gateways", "package", "module", "runtime", "output_root"}, []string{"gateways", "package", "module", "runtime", "output_root", "typescript_version", "javascript_target", "include", "version_policy"}, nil},
	"scenery.patch/v1":             {"workspace_only", []string{"target", "module_version", "schema", "expect", "set"}, []string{"target", "module_version", "schema"}, nil},
	"scenery.module/v1": {"workspace_only", []string{"source"}, []string{"source", "version", "inputs"}, map[string]string{
		"package": "contract", "interface_inputs": "contract", "exports": "contract", "export_metadata": "contract", "workspace_package_root": "workspace_only",
		"locked_version": "contract", "locked_integrity": "contract", "compile_descriptor_digest": "contract", "package_contract_abi_revision": "implementation",
	}},
	"scenery.service/v1":        {RevisionDomain: "contract", Required: []string{"runtime", "implementation"}, Attributes: []string{"runtime"}},
	"scenery.record/v1":         {"contract", nil, []string{"unknown_fields"}, nil},
	"scenery.enum/v1":           {"contract", []string{"value"}, []string{"open"}, nil},
	"scenery.union/v1":          {"contract", []string{"variant", "discriminator"}, []string{"open", "discriminator", "unknown_variant"}, nil},
	"scenery.operation/v1":      {"contract", []string{"service", "input", "handler"}, []string{"service", "input"}, nil},
	"scenery.execution/v1":      {"contract", []string{"operation", "mode"}, []string{"operation", "mode", "engine", "revision", "timeout", "lease", "attempts", "external_name"}, nil},
	"scenery.binding/v1":        {"contract", []string{"operation", "execution", "protocol", "delivery", "authentication", "authorization", "pipeline"}, []string{"gateway", "operation", "execution", "protocol", "delivery", "exposure", "authentication", "authorization", "pipeline"}, nil},
	"scenery.schedule/v1":       {"contract", []string{"trigger", "invoke", "overlap"}, []string{"overlap"}, nil},
	"scenery.event/v1":          {"contract", []string{"payload", "version"}, []string{"payload", "version"}, nil},
	"scenery.event-emission/v1": {"contract", []string{"bus", "channel", "contract", "guarantee", "from"}, []string{"bus", "channel", "contract", "guarantee", "ordering_key", "deduplication_key", "dead_letter_channel"}, nil},
	"scenery.entity/v1":         {"contract", []string{"type", "data_source", "mapping", "field"}, []string{"type", "data_source"}, nil},
	"scenery.view/v1": {"contract", []string{"data_source", "input", "result", "implementation"}, []string{"data_source", "input", "result"}, map[string]string{
		"implementation_digest": "implementation",
	}},
	"scenery.crud/v1":    {"contract", []string{"entity", "implementation", "actions", "execution"}, []string{"entity", "implementation", "actions"}, nil},
	"scenery.fixture/v1": {"contract", []string{"entity", "environments", "mode", "values"}, []string{"entity", "environments", "mode", "values"}, nil},
	"scenery.page/v1":    {"contract", []string{"path", "load"}, []string{"path", "load"}, nil},
	"scenery.renderer/v1": {"contract", []string{"page", "runtime", "module"}, []string{"page", "runtime", "module", "config"}, map[string]string{
		"implementation_digest": "implementation",
	}},
	"scenery.middleware/v1": {"contract", []string{"protocols", "phases"}, []string{"protocols", "phases", "before", "after", "exclusive", "effects"}, nil},
}

var resourceConditionalRequirements = map[string][]resourceConditionalRequirement{
	"scenery.binding/v1": {{Field: "protocol", Equals: "http", Required: []string{"gateway"}}},
}

var authoredConditionalRequirements = map[string][]resourceConditionalRequirement{
	"scenery.operation.idempotency/v1": {{Field: "mode", Equals: "keyed", Required: []string{"key"}}},
	"scenery.source.binding/v1":        resourceConditionalRequirements["scenery.binding/v1"],
}

var dynamicResourceRevisionDomains = map[string]map[string]dynamicRevisionDomain{
	"scenery.service/v1": {
		"config":        {SchemaField: "config_schema", NameField: "name", DomainField: "phase"},
		"config_schema": {SchemaField: "config_schema", NameField: "name", DomainField: "phase"},
	},
}

func CoreSchema(kind string) (map[string]any, bool) {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return nil, false
	}
	blockType := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(kind, "/v1"), "scenery."), "-", "_")
	authored, ok := authoredResourceSourceSchema(blockType)
	if !ok {
		return nil, false
	}
	required := append([]string(nil), schema.Required...)
	allowed := resourceSchemaAllowedFields(kind)
	sort.Strings(required)
	result := publicAuthoredBlockSchema(authored)
	result["schema_revision"] = kind
	result["source_schema_revision"] = authored.Revision
	result["kind"] = kind
	result["label_source"] = "address"
	result["required"] = required
	result["allowed"] = allowed
	result["revision_domain"] = schema.RevisionDomain
	if requirements := resourceConditionalRequirements[kind]; len(requirements) > 0 {
		result["conditional_requirements"] = publicConditionalRequirements(requirements)
	}
	canonicalOnly := make([]string, 0, len(schema.CanonicalOnly))
	canonicalDomains := make(map[string]string, len(schema.CanonicalOnly))
	for name, domain := range schema.CanonicalOnly {
		canonicalOnly = append(canonicalOnly, name)
		canonicalDomains[name] = domain
	}
	canonicalDomainSources := map[string]string{}
	for name, rule := range dynamicResourceRevisionDomains[kind] {
		if name != "config" {
			canonicalOnly = append(canonicalOnly, name)
		}
		canonicalDomainSources[name] = rule.SchemaField + "." + rule.DomainField
	}
	sort.Strings(canonicalOnly)
	result["canonical_only_fields"] = canonicalOnly
	result["canonical_field_revision_domains"] = canonicalDomains
	result["field_revision_domain_sources"] = canonicalDomainSources
	return result, true
}

func blockTypeForKind(kind string) string {
	return strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(kind, "/v1"), "scenery."), "-", "_")
}

func resourceSchemaAllowedFields(kind string) []string {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return nil
	}
	values := append([]string(nil), schema.Attributes...)
	for name := range authoredResourceChildren[blockTypeForKind(kind)] {
		values = append(values, name)
	}
	for name := range schema.CanonicalOnly {
		values = append(values, name)
	}
	for name := range dynamicResourceRevisionDomains[kind] {
		values = append(values, name)
	}
	return canonicalStrings(values)
}

func resourceFieldRevisionDomain(kind, name string) (string, bool) {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return "", false
	}
	if domain, canonical := schema.CanonicalOnly[name]; canonical {
		return domain, true
	}
	if _, dynamic := dynamicResourceRevisionDomains[kind][name]; dynamic {
		return "", true
	}
	authored, ok := authoredResourceSourceSchema(blockTypeForKind(kind))
	if !ok {
		return "", false
	}
	if attribute, exists := authored.Attributes[name]; exists {
		return attribute.RevisionDomain, true
	}
	if _, exists := authored.Children[name]; exists {
		return authoredRevisionDomain(authored.Revision, name), true
	}
	return "", false
}

func authoredPublicSchema(revision string) (map[string]any, bool) {
	kinds := make([]string, 0, len(resourceSchemas))
	for kind := range resourceSchemas {
		kinds = append(kinds, kind)
	}
	sort.Strings(kinds)
	seen := map[*authoredBlockSchema]bool{}
	var find func(*authoredBlockSchema) (*authoredBlockSchema, bool)
	find = func(schema *authoredBlockSchema) (*authoredBlockSchema, bool) {
		if schema == nil || seen[schema] {
			return nil, false
		}
		seen[schema] = true
		if schema.Revision == revision {
			return schema, true
		}
		children := make([]string, 0, len(schema.Children))
		for name := range schema.Children {
			children = append(children, name)
		}
		sort.Strings(children)
		for _, name := range children {
			if found, ok := find(schema.Children[name].Schema); ok {
				return found, true
			}
		}
		return nil, false
	}
	for _, kind := range kinds {
		blockType := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(kind, "/v1"), "scenery."), "-", "_")
		root, ok := authoredResourceSourceSchema(blockType)
		if !ok {
			continue
		}
		if schema, ok := find(root); ok {
			return publicAuthoredBlockSchema(schema), true
		}
	}
	return nil, false
}

func publicAuthoredBlockSchema(schema *authoredBlockSchema) map[string]any {
	fields := map[string]any{}
	attributeNames := make([]string, 0, len(schema.Attributes))
	for name := range schema.Attributes {
		attributeNames = append(attributeNames, name)
	}
	sort.Strings(attributeNames)
	for _, name := range attributeNames {
		fields[name] = publicAuthoredAttribute(schema.Attributes[name], schema.Required[name])
	}
	childNames := make([]string, 0, len(schema.Children))
	for name := range schema.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		child := schema.Children[name]
		shape := "singleton_block"
		maximum := any(1)
		if child.Repeatable {
			shape, maximum = "repeated_block", nil
		}
		if child.Schema.Labels > 0 {
			if child.Repeatable {
				shape = "repeated_labeled_block"
			} else {
				shape = "singleton_labeled_block"
			}
		}
		minimum := 0
		if schema.Required[name] {
			minimum = 1
		}
		domain := authoredRevisionDomain(schema.Revision, name)
		var domainSource any
		if rule, dynamic := dynamicRevisionRuleForSource(schema.Revision, name); dynamic {
			domain = ""
			domainSource = rule.SchemaField + "." + rule.DomainField
		}
		fields[name] = map[string]any{
			"source_shape": shape, "type": map[string]any{"schema_ref": child.Schema.Revision}, "child_schema": child.Schema.Revision,
			"labels": child.Schema.Labels, "label_source": map[bool]any{true: "name", false: nil}[child.Schema.Labels > 0],
			"cardinality": map[string]any{"minimum": minimum, "maximum": maximum}, "required": schema.Required[name],
			"phase": authoredPhase(child.Schema), "revision_domain": domain, "revision_domain_source": domainSource, "default": nil,
			"default_source": "none", "constraints": map[string]any{"label_pattern": child.Schema.LabelPattern, "label_policy": child.Schema.LabelPolicy},
			"sensitive": false, "patchable": true, "patch_scope": "authored_source", "ordered": child.Ordered,
		}
	}
	shape := "block"
	if schema.Labels > 0 {
		shape = "labeled_block"
	}
	result := map[string]any{
		"schema_revision": schema.Revision, "source_shape": shape, "labels": schema.Labels, "fields": fields,
		"label_source":          map[bool]any{true: "name", false: nil}[schema.Labels > 0],
		"label_pattern":         schema.LabelPattern,
		"label_policy":          schema.LabelPolicy,
		"additional_properties": schema.AllowUnknownAttributes, "ordering": "schema_defined",
	}
	var metadataGaps []string
	for _, name := range attributeNames {
		if schema.Attributes[name].MetadataStatus == "generic" {
			metadataGaps = append(metadataGaps, "/fields/"+name+"/type")
		}
	}
	result["metadata_gaps"] = metadataGaps
	if dynamic := schema.DynamicAttribute; dynamic != nil {
		descriptor := publicAuthoredAttribute(*dynamic, false)
		if pattern, ok := dynamic.Constraints["name_pattern"]; ok {
			descriptor["name_pattern"] = pattern
		}
		result["dynamic_attributes"] = descriptor
	}
	if requirements := authoredConditionalRequirements[schema.Revision]; len(requirements) > 0 {
		result["conditional_requirements"] = publicConditionalRequirements(requirements)
	}
	return result
}

func publicConditionalRequirements(requirements []resourceConditionalRequirement) []any {
	conditional := make([]any, 0, len(requirements))
	for _, requirement := range requirements {
		conditional = append(conditional, map[string]any{
			"when":     map[string]any{"field": requirement.Field, "equals": cloneSemanticValue(requirement.Equals)},
			"required": append([]string(nil), requirement.Required...),
		})
	}
	return conditional
}

func publicAuthoredAttribute(attribute authoredAttributeSchema, required bool) map[string]any {
	constraints := cloneMapValue(attribute.Constraints)
	if constraints == nil {
		constraints = map[string]any{}
	}
	result := map[string]any{
		"source_shape": "attribute", "type": cloneMapValue(attribute.Type), "phase": attribute.Phase,
		"revision_domain": attribute.RevisionDomain, "required": required, "default": cloneSemanticValue(attribute.Default),
		"default_source": attribute.DefaultSource, "constraints": constraints, "sensitive": attribute.Sensitive, "patchable": attribute.Patchable,
		"patch_scope": "authored_source", "ordered": attribute.Ordered, "metadata_status": attribute.MetadataStatus,
	}
	if attribute.UnsupportedDraft != "" {
		result["support_status"] = "unsupported_draft"
		result["unsupported_capability"] = attribute.UnsupportedDraft
	}
	if attribute.SensitivitySource != "" {
		result["sensitivity_source"] = attribute.SensitivitySource
	}
	if attribute.InputPhaseSource != "" {
		result["input_phase_source"] = attribute.InputPhaseSource
	}
	if attribute.RevisionSource != "" {
		result["revision_domain_source"] = attribute.RevisionSource
		result["revision_domain"] = nil
	}
	return result
}

func dynamicRevisionRuleForSource(revision, name string) (dynamicRevisionDomain, bool) {
	if !strings.HasPrefix(revision, "scenery.source.") || !strings.HasSuffix(revision, "/v1") {
		return dynamicRevisionDomain{}, false
	}
	blockType := strings.TrimSuffix(strings.TrimPrefix(revision, "scenery.source."), "/v1")
	kind := kindForBlock(blockType)
	rule, ok := dynamicResourceRevisionDomains[kind][name]
	return rule, ok
}

func authoredPhase(schema *authoredBlockSchema) string {
	return "compile"
}

func resourceCreateSchemaRevisions() []string {
	var revisions []string
	for kind := range resourceSchemas {
		blockType := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(kind, "/v1"), "scenery."), "-", "_")
		schema, ok := authoredResourceSourceSchema(blockType)
		if ok && authoredSchemaMetadataComplete(schema, map[*authoredBlockSchema]bool{}) {
			revisions = append(revisions, kind)
		}
	}
	sort.Strings(revisions)
	return revisions
}

func resourceCreateKindSupported(kind string) bool {
	for _, supported := range resourceCreateSchemaRevisions() {
		if supported == kind {
			return true
		}
	}
	return false
}

func authoredSchemaMetadataComplete(schema *authoredBlockSchema, seen map[*authoredBlockSchema]bool) bool {
	if schema == nil || seen[schema] {
		return schema != nil
	}
	seen[schema] = true
	for _, attribute := range schema.Attributes {
		if attribute.MetadataStatus != "exact" || len(attribute.Type) == 0 || attribute.Phase == "" || attribute.RevisionDomain == "" {
			return false
		}
	}
	if schema.AllowUnknownAttributes && (schema.DynamicAttribute == nil || !oneOf(schema.DynamicAttribute.MetadataStatus, "exact", "dynamic")) {
		return false
	}
	for _, child := range schema.Children {
		if !authoredSchemaMetadataComplete(child.Schema, seen) {
			return false
		}
	}
	return true
}

func validateResourceSchemas(resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		schema, ok := resourceSchemas[resource.Kind]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN1008", Severity: "error", Message: "unknown resource schema " + resource.Kind, Address: resource.Address})
			continue
		}
		allowed := map[string]bool{}
		for _, name := range resourceSchemaAllowedFields(resource.Kind) {
			allowed[name] = true
		}
		for name := range resource.Spec {
			if !allowed[name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1007", Severity: "error", Message: "unknown field " + name + " for " + resource.Kind, Address: resource.Address})
			}
		}
		for _, name := range schema.Required {
			if resource.Spec[name] == nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1009", Severity: "error", Message: "missing required field " + name, Address: resource.Address})
			}
		}
		for _, requirement := range resourceConditionalRequirements[resource.Kind] {
			if !semanticEqual(resource.Spec[requirement.Field], requirement.Equals) {
				continue
			}
			for _, name := range requirement.Required {
				if resource.Spec[name] == nil {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1009", Severity: "error", Message: "missing required field " + name + " when " + requirement.Field + " is " + stringValue(requirement.Equals), Address: resource.Address})
				}
			}
		}
	}
	return diagnostics
}
