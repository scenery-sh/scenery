package spec

import (
	"fmt"
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
	"scenery.go-module":         {RevisionDomain: "implementation", Required: []string{"root", "import_path"}, Attributes: []string{"root", "import_path"}},
	"scenery.go-toolchain":      {RevisionDomain: "implementation", Required: []string{"version"}, Attributes: []string{"version", "experiments"}},
	"scenery.go-target":         {RevisionDomain: "implementation", Required: []string{"role", "toolchain", "module", "packages"}, Attributes: []string{"role", "platform", "toolchain", "module", "packages", "cgo", "extends", "inherits", "build_tags", "go_flags", "environment", "goos", "goarch", "architecture_features", "native_input", "native_inputs", "verify_by_default"}},
	"scenery.http-gateway":      {"contract", []string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded"}, []string{"exposure", "base_path", "cors", "trusted_proxies", "forwarded", "request_limit", "response_limit", "timeouts"}, nil},
	"scenery.authentication":    {"contract", []string{"provider", "scheme"}, []string{"provider", "scheme", "config"}, nil},
	"scenery.authorization":     {"contract", []string{"principal"}, []string{"principal", "strategy"}, nil},
	"scenery.workload-identity": {"contract", []string{"issuer", "principal_type", "claims"}, []string{"issuer", "principal_type", "claims"}, nil},
	"scenery.pipeline":          {"contract", nil, nil, nil},
	"scenery.provider": {"contract", []string{"source"}, []string{"source", "config"}, map[string]string{
		"locked_integrity": "implementation", "compile_descriptor_digest": "contract", "runtime_abi": "implementation",
		"deployment_abi": "deployment", "migration_abi": "implementation", "config_schema": "contract", "capabilities": "contract", "instance_kinds": "contract",
	}},
	"scenery.data-source": {"contract", []string{"provider", "lifecycle"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.execution-engine": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.event-bus": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.secret-store": {"contract", []string{"provider"}, []string{"provider", "lifecycle", "require_capabilities", "config"}, map[string]string{
		"effective_capabilities": "contract", "provider_descriptor_digest": "contract",
	}},
	"scenery.secret":            {"deployment", []string{"store", "key"}, []string{"store", "key"}, nil},
	"scenery.deployment":        {"deployment", []string{"environment"}, []string{"environment", "fixture_policy"}, nil},
	"scenery.typescript-client": {"implementation", []string{"gateways", "package", "module", "runtime", "output_root"}, []string{"gateways", "package", "module", "runtime", "output_root", "materialization", "typescript_version", "javascript_target", "include", "react"}, nil},
	"scenery.patch":             {"workspace_only", []string{"target", "schema", "expect", "set"}, []string{"target", "schema"}, nil},
	"scenery.module": {"workspace_only", []string{"source"}, []string{"source", "inputs"}, map[string]string{
		"package": "contract", "interface_inputs": "contract", "exports": "contract", "export_metadata": "contract", "workspace_package_root": "workspace_only",
		"locked_integrity": "contract", "compile_descriptor_digest": "contract", "package_contract_abi_revision": "implementation",
	}},
	"scenery.service":        {RevisionDomain: "contract", Required: []string{"runtime", "implementation"}, Attributes: []string{"runtime"}},
	"scenery.library":        {RevisionDomain: "contract", Required: []string{"runtime", "package", "version", "artifact"}, Attributes: []string{"runtime", "package", "version"}},
	"scenery.record":         {"contract", nil, []string{"unknown_fields"}, nil},
	"scenery.enum":           {"contract", []string{"value"}, []string{"open"}, nil},
	"scenery.union":          {"contract", []string{"variant", "discriminator"}, []string{"open", "discriminator", "unknown_variant"}, nil},
	"scenery.operation":      {"contract", []string{"input", "handler"}, []string{"service", "library", "input"}, nil},
	"scenery.execution":      {"contract", []string{"operation", "mode"}, []string{"operation", "mode", "engine", "revision", "timeout", "lease", "attempts", "external_name"}, nil},
	"scenery.binding":        {"contract", []string{"operation", "execution", "protocol", "delivery", "authentication", "authorization", "pipeline"}, []string{"gateway", "operation", "execution", "protocol", "delivery", "exposure", "authentication", "authorization", "pipeline"}, nil},
	"scenery.schedule":       {"contract", []string{"trigger", "invoke", "overlap"}, []string{"overlap"}, nil},
	"scenery.event":          {"contract", []string{"payload", "version"}, []string{"payload", "version"}, nil},
	"scenery.event-emission": {"contract", []string{"bus", "channel", "contract", "guarantee", "from"}, []string{"bus", "channel", "contract", "guarantee", "ordering_key", "deduplication_key", "dead_letter_channel"}, nil},
	"scenery.entity":         {"contract", []string{"type", "data_source", "mapping", "field"}, []string{"type", "data_source"}, nil},
	"scenery.view": {"contract", []string{"data_source", "input", "result", "implementation"}, []string{"data_source", "input", "result"}, map[string]string{
		"implementation_digest": "implementation",
	}},
	"scenery.crud":    {"contract", []string{"entity", "implementation", "actions", "execution"}, []string{"entity", "implementation", "actions", "list"}, nil},
	"scenery.fixture": {"contract", []string{"entity", "environments", "mode", "values"}, []string{"entity", "environments", "mode", "values"}, nil},
	"scenery.page":    {"contract", []string{"path"}, []string{"path", "load"}, nil},
	"scenery.renderer": {"contract", []string{"page", "runtime", "module"}, []string{"page", "runtime", "module", "config"}, map[string]string{
		"implementation_digest": "implementation",
	}},
	"scenery.react-component": {"implementation", []string{"module", "export"}, []string{"module", "export"}, nil},
	"scenery.status-map":      {"contract", []string{"status"}, nil, nil},
	"scenery.form-dialog":     {"contract", []string{"source", "title"}, []string{"source", "title", "description", "submit_label"}, nil},
	"scenery.table-page":      {"contract", []string{"path", "source", "title", "column"}, []string{"path", "source", "items", "title", "description", "page_size", "row_link", "hide_header", "nav_group", "nav_order", "nav_label", "nav_icon", "nav_active_paths"}, nil},
	"scenery.split-page":      {"contract", []string{"path", "source", "title", "sidebar", "detail"}, []string{"path", "source", "title", "aria_label", "sidebar_label", "query_parameter", "nav_group", "nav_order", "nav_label", "nav_icon", "nav_active_paths"}, nil},
	"scenery.content-page":    {"contract", []string{"path", "title", "content"}, []string{"path", "source", "title", "aria_label", "max_width", "nav_group", "nav_order", "nav_label", "nav_icon", "nav_active_paths"}, nil},
	"scenery.middleware":      {"contract", []string{"protocols", "phases"}, []string{"protocols", "phases", "before", "after", "exclusive", "effects"}, nil},
}

var resourceConditionalRequirements = map[string][]resourceConditionalRequirement{
	"scenery.binding": {{Field: "protocol", Equals: "http", Required: []string{"gateway"}}},
}

var authoredConditionalRequirements = map[string][]resourceConditionalRequirement{
	"scenery.operation.idempotency": {{Field: "mode", Equals: "keyed", Required: []string{"key"}}},
	"scenery.source.binding":        resourceConditionalRequirements["scenery.binding"],
}

var dynamicResourceRevisionDomains = map[string]map[string]dynamicRevisionDomain{
	"scenery.service": {
		"config":        {SchemaField: "config_schema", NameField: "name", DomainField: "phase"},
		"config_schema": {SchemaField: "config_schema", NameField: "name", DomainField: "phase"},
	},
}

func CoreSchema(kind string) (map[string]any, bool) {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return nil, false
	}
	blockType := strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
	authored, ok := authoredResourceSourceSchema(blockType)
	if !ok {
		return nil, false
	}
	required := append([]string(nil), schema.Required...)
	allowed := resourceSchemaAllowedFields(kind)
	sort.Strings(required)
	result := publicAuthoredBlockSchema(authored)
	result["source_schema_revision"] = string(SourceSchemaRevision(authored))
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
	delete(result, "schema_revision")
	result["schema_revision"] = string(SchemaRevision(result))
	return result, true
}

func blockTypeForKind(kind string) string {
	return strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
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
		if string(SourceSchemaRevision(schema)) == revision {
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
		blockType := strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
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
		childRevision := string(SourceSchemaRevision(child.Schema))
		fields[name] = map[string]any{
			"source_shape": shape, "type": map[string]any{"schema_ref": childRevision}, "child_schema": childRevision,
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
		"source_shape": shape, "labels": schema.Labels, "fields": fields,
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
	result["schema_revision"] = string(SchemaRevision(result))
	return result
}

func SourceSchemaRevision(schema *SourceBlockSchema) Revision {
	if schema == nil {
		return ""
	}
	public := publicAuthoredBlockSchema(schema)
	delete(public, "schema_revision")
	return SchemaRevision(public)
}

func SourceSchemaRevisionForInternalName(name string) Revision {
	seen := map[*SourceBlockSchema]bool{}
	var find func(*SourceBlockSchema) Revision
	find = func(schema *SourceBlockSchema) Revision {
		if schema == nil || seen[schema] {
			return ""
		}
		seen[schema] = true
		if schema.Revision == name {
			return SourceSchemaRevision(schema)
		}
		for _, child := range schema.Children {
			if revision := find(child.Schema); revision != "" {
				return revision
			}
		}
		return ""
	}
	for _, schema := range authoredStructuralSchemas {
		if revision := find(schema); revision != "" {
			return revision
		}
	}
	for kind := range resourceSchemas {
		schema, _ := authoredResourceSourceSchema(blockTypeForKind(kind))
		if revision := find(schema); revision != "" {
			return revision
		}
	}
	return ""
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
	for kind := range resourceSchemas {
		schema, ok := authoredResourceSourceSchema(blockTypeForKind(kind))
		if ok && schema.Revision == revision {
			rule, dynamic := dynamicResourceRevisionDomains[kind][name]
			return rule, dynamic
		}
	}
	return dynamicRevisionDomain{}, false
}

func authoredPhase(schema *authoredBlockSchema) string {
	return "compile"
}

func resourceCreateSchemaRevisions() []string {
	var revisions []string
	for kind := range resourceSchemas {
		blockType := strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
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

type ResourceSchema = resourceSchema
type ConditionalRequirement = resourceConditionalRequirement
type DynamicRevisionDomain = dynamicRevisionDomain

type ResourceViolation struct {
	Code    string
	Message string
}

func ValidateResource(kind string, fields map[string]any) []ResourceViolation {
	schema, ok := resourceSchemas[kind]
	if !ok {
		return []ResourceViolation{{Code: "SCN1008", Message: "unknown resource schema " + kind}}
	}
	allowed := map[string]bool{}
	for _, name := range resourceSchemaAllowedFields(kind) {
		allowed[name] = true
	}
	var violations []ResourceViolation
	for name := range fields {
		if !allowed[name] {
			violations = append(violations, ResourceViolation{Code: "SCN1007", Message: "unknown field " + name + " for " + kind})
		}
	}
	for _, name := range schema.Required {
		if fields[name] == nil {
			violations = append(violations, ResourceViolation{Code: "SCN1009", Message: "missing required field " + name})
		}
	}
	for _, requirement := range resourceConditionalRequirements[kind] {
		left, leftErr := MarshalCanonical(fields[requirement.Field])
		right, rightErr := MarshalCanonical(requirement.Equals)
		if leftErr != nil || rightErr != nil || string(left) != string(right) {
			continue
		}
		for _, name := range requirement.Required {
			if fields[name] == nil {
				violations = append(violations, ResourceViolation{Code: "SCN1009", Message: "missing required field " + name + " when " + requirement.Field + " is " + fmt.Sprint(requirement.Equals)})
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool {
		if violations[i].Code != violations[j].Code {
			return violations[i].Code < violations[j].Code
		}
		return violations[i].Message < violations[j].Message
	})
	return violations
}

func ResourceSchemas() map[string]ResourceSchema {
	result := make(map[string]ResourceSchema, len(resourceSchemas))
	for kind, schema := range resourceSchemas {
		schema.Required = append([]string(nil), schema.Required...)
		schema.Attributes = append([]string(nil), schema.Attributes...)
		if schema.CanonicalOnly != nil {
			schema.CanonicalOnly = cloneSemanticValue(schema.CanonicalOnly).(map[string]string)
		}
		result[kind] = schema
	}
	return result
}

func ConditionalRequirements() map[string][]ConditionalRequirement {
	return cloneConditionalRequirements(resourceConditionalRequirements)
}

func AuthoredConditionalRequirements() map[string][]ConditionalRequirement {
	return cloneConditionalRequirements(authoredConditionalRequirements)
}

func ResourceSchemaAllowedFields(kind string) []string {
	return resourceSchemaAllowedFields(kind)
}

func ResourceFieldRevisionDomain(kind, name string) (string, bool) {
	return resourceFieldRevisionDomain(kind, name)
}

func AuthoredPublicSchema(revision string) (map[string]any, bool) {
	return authoredPublicSchema(revision)
}

func PublicAuthoredBlockSchema(schema *SourceBlockSchema) map[string]any {
	return publicAuthoredBlockSchema(schema)
}

func DynamicResourceRevisionDomains() map[string]map[string]DynamicRevisionDomain {
	result := make(map[string]map[string]DynamicRevisionDomain, len(dynamicResourceRevisionDomains))
	for kind, fields := range dynamicResourceRevisionDomains {
		fieldCopies := make(map[string]DynamicRevisionDomain, len(fields))
		for name, rule := range fields {
			fieldCopies[name] = rule
		}
		result[kind] = fieldCopies
	}
	return result
}

func cloneConditionalRequirements(source map[string][]resourceConditionalRequirement) map[string][]resourceConditionalRequirement {
	result := make(map[string][]resourceConditionalRequirement, len(source))
	for kind, requirements := range source {
		copies := make([]resourceConditionalRequirement, len(requirements))
		for index, requirement := range requirements {
			requirement.Equals = cloneSemanticValue(requirement.Equals)
			requirement.Required = append([]string(nil), requirement.Required...)
			copies[index] = requirement
		}
		result[kind] = copies
	}
	return result
}

func ResourceCreateSchemaRevisions() []string {
	return resourceCreateSchemaRevisions()
}

func ResourceCreateKindSupported(kind string) bool {
	return resourceCreateKindSupported(kind)
}
