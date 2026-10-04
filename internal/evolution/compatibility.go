package evolution

import (
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/scn"
)

type Manifest = graph.Manifest
type Resource = graph.Resource
type Diagnostic = graph.Diagnostic
type Result = compiler.Result
type Source = scn.Source
type Range = scn.Range

const (
	CompatibilityCompatible        = "compatible"
	CompatibilityBreaking          = "breaking"
	CompatibilityMigrationRequired = "migration_required"
	CompatibilityUnknown           = "unknown"
	SecurityEqual                  = "equal"
	SecurityStronger               = "stronger"
	SecurityWeaker                 = "weaker"
	SecurityIncomparable           = "incomparable"
	SecurityUnknown                = "unknown"
)

var compatibilityDimensions = []string{"source", "request_wire", "response_wire", "generated_client", "internal_call", "runtime", "security", "storage", "deployment"}

// compatibilityRuleCatalog is intentionally kept next to the comparison
// implementation. The catalog digest is part of every semantic diff, so a
// rule that changes the result must also change the digest. MCP and assistant
// rules live here rather than in a provider adapter: they describe the
// provider-neutral contract and therefore remain useful to every adapter.
var compatibilityRuleCatalog = []string{
	"SCN_COMPAT_ASSISTANT_CHANGE_UNKNOWN",
	"SCN_COMPAT_ASSISTANT_IMPLEMENTATION_REBUILD_REQUIRED",
	"SCN_COMPAT_ASSISTANT_PUBLIC_SURFACE_CHANGED",
	"SCN_COMPAT_MCP_APPROVAL_POLICY_CHANGED",
	"SCN_COMPAT_MCP_CAPABILITY_ADDED",
	"SCN_COMPAT_MCP_CAPABILITY_BINDING_CHANGED",
	"SCN_COMPAT_MCP_CAPABILITY_REMOVED",
	"SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED",
	"SCN_COMPAT_MCP_CONNECTION_CHANGE_UNKNOWN",
	"SCN_COMPAT_MCP_CONNECTION_READINESS_CHANGED",
	"SCN_COMPAT_MCP_EFFECT_METADATA_CHANGED",
	"SCN_COMPAT_MCP_FILTER_CHANGED",
	"SCN_COMPAT_MCP_LIMIT_CHANGED",
	"SCN_COMPAT_MCP_SERVER_CHANGE_UNKNOWN",
	"SCN_COMPAT_MCP_TOOL_NAME_CHANGED",
}

var compatibilityConsequenceCatalog = map[string][]string{
	"mcp_binding":              {"mcp_capability_revision[*]"},
	"mcp_server":               {"mcp_capability_revision[*]"},
	"mcp_connection":           {"assistant_readiness[*]", "implementation_revision[*]"},
	"assistant_surface":        {"assistant_public_revision[*]", "http_surface_revision[*]", "openapi_revision[*]", "typescript_client_revision[*]"},
	"assistant_implementation": {"implementation_revision[*]"},
}

type RenameReceipt struct {
	From                   string `json:"from"`
	To                     string `json:"to"`
	BaseContractRevision   string `json:"base_contract_revision"`
	TargetContractRevision string `json:"target_contract_revision"`
	Digest                 string `json:"digest"`
}

type CompareOptions struct {
	View       string           `json:"view,omitempty"`
	Dimensions []string         `json:"dimensions,omitempty"`
	Scope      string           `json:"scope,omitempty"`
	Renames    []RenameReceipt  `json:"renames,omitempty"`
	Rebinds    []RevisionRebind `json:"revision_rebinds,omitempty"`
}

type Classification struct {
	Applicable bool   `json:"applicable"`
	Result     string `json:"result,omitempty"`
	Relation   string `json:"relation,omitempty"`
	Rule       string `json:"rule,omitempty"`
}

type SemanticChange struct {
	ChangeID             string                    `json:"change_id"`
	Operation            string                    `json:"operation"`
	Address              string                    `json:"address"`
	ExpectedKind         string                    `json:"expected_kind,omitempty"`
	BaseSchemaRevision   string                    `json:"base_schema_revision,omitempty"`
	TargetSchemaRevision string                    `json:"target_schema_revision,omitempty"`
	Path                 string                    `json:"path,omitempty"`
	Base                 any                       `json:"base,omitempty"`
	Target               any                       `json:"target,omitempty"`
	Classifications      map[string]Classification `json:"classifications"`
	AffectedArtifacts    []string                  `json:"affected_artifacts,omitempty"`
	Evidence             []any                     `json:"evidence"`
}

type DiffSummary struct {
	Compatible        int `json:"compatible"`
	Breaking          int `json:"breaking"`
	MigrationRequired int `json:"migration_required"`
	Unknown           int `json:"unknown"`
}

type SemanticDiff struct {
	machine.ArtifactIdentity
	CatalogDigest         string           `json:"catalog_digest"`
	BaseRevision          string           `json:"base_revision,omitempty"`
	TargetRevision        string           `json:"target_revision,omitempty"`
	View                  string           `json:"view"`
	Scope                 string           `json:"scope,omitempty"`
	Dimensions            []string         `json:"dimensions"`
	Changes               []SemanticChange `json:"changes"`
	Summary               DiffSummary      `json:"summary"`
	RequiredMigrations    []any            `json:"required_migrations"`
	GeneratedConsequences []string         `json:"generated_consequences"`
	RiskRecords           []any            `json:"risk_records"`
	Digest                string           `json:"comparison_digest"`
}

type comparisonContext struct {
	base, target  map[string]Resource
	dimensions    []string
	typePositions map[string]typePosition
}

type typePosition struct {
	input  bool
	output bool
}

type valueDifference struct {
	operation string
	path      string
	base      any
	target    any
}

func CompareManifests(base, target *Manifest, options CompareOptions) SemanticDiff {
	if options.View == "" {
		options.View = "expanded"
	}
	dimensions := selectedCompatibilityDimensions(options.Dimensions)
	diff := SemanticDiff{
		ArtifactIdentity:      machine.NewArtifactIdentity(semanticDiffKind, semanticDiffSchemaDescriptor),
		CatalogDigest:         compatibilityCatalogDigest(),
		View:                  options.View,
		Scope:                 options.Scope,
		Dimensions:            dimensions,
		RequiredMigrations:    []any{},
		GeneratedConsequences: []string{},
		RiskRecords:           []any{},
	}
	if base != nil {
		diff.BaseRevision = base.ContractRevision
	}
	if target != nil {
		diff.TargetRevision = target.ContractRevision
		diff.SpecRevision = target.SpecRevision
	} else if base != nil {
		diff.SpecRevision = base.SpecRevision
	}
	ctx := comparisonContext{base: resourcesByAddress(base), target: resourcesByAddress(target), dimensions: dimensions}
	ctx.typePositions = compatibilityTypePositions(ctx.base, ctx.target)
	consumedBase, consumedTarget := map[string]bool{}, map[string]bool{}
	renames := ValidRenameReceiptsWithRebinds(base, target, options.Renames, options.Rebinds)
	for _, receipt := range renames {
		before, beforeOK := ctx.base[receipt.From]
		after, afterOK := ctx.target[receipt.To]
		if !beforeOK || !afterOK || before.Kind != after.Kind || consumedBase[receipt.From] || consumedTarget[receipt.To] {
			continue
		}
		consumedBase[receipt.From], consumedTarget[receipt.To] = true, true
		change := classifyChange(ctx, "rename", &before, &after, "/address", receipt.From, receipt.To)
		change.Evidence = append(change.Evidence, map[string]any{
			"kind": "rename_receipt", "from": receipt.From, "to": receipt.To,
			"base_contract_revision": receipt.BaseContractRevision, "target_contract_revision": receipt.TargetContractRevision,
			"digest": receipt.Digest,
		})
		for _, rebind := range validRevisionRebinds(base, options.Rebinds) {
			change.Evidence = append(change.Evidence, map[string]any{"kind": "revision_rebind", "from_contract_revision": rebind.FromContractRevision, "to_contract_revision": rebind.ToContractRevision, "contract_projection_hash": rebind.ProjectionHash, "reason": rebind.Reason, "digest": rebind.Digest})
		}
		for _, rebind := range validRevisionRebinds(target, options.Rebinds) {
			change.Evidence = append(change.Evidence, map[string]any{"kind": "revision_rebind", "from_contract_revision": rebind.FromContractRevision, "to_contract_revision": rebind.ToContractRevision, "contract_projection_hash": rebind.ProjectionHash, "reason": rebind.Reason, "digest": rebind.Digest})
		}
		diff.Changes = append(diff.Changes, change)
		for _, difference := range semanticDifferences(before.Spec, after.Spec, "/spec") {
			diff.Changes = append(diff.Changes, classifyChange(ctx, difference.operation, &before, &after, difference.path, difference.base, difference.target))
		}
	}
	for _, address := range stringUnion(ctx.base, ctx.target) {
		before, beforeOK := ctx.base[address]
		after, afterOK := ctx.target[address]
		if beforeOK && consumedBase[address] || afterOK && consumedTarget[address] {
			continue
		}
		switch {
		case !beforeOK:
			diff.Changes = append(diff.Changes, classifyChange(ctx, "add", nil, &after, "", nil, after.Spec))
		case !afterOK:
			diff.Changes = append(diff.Changes, classifyChange(ctx, "remove", &before, nil, "", before.Spec, nil))
		case before.Kind != after.Kind:
			diff.Changes = append(diff.Changes, classifyChange(ctx, "replace", &before, &after, "/kind", before.Kind, after.Kind))
		default:
			for _, difference := range semanticDifferences(before.Spec, after.Spec, "/spec") {
				diff.Changes = append(diff.Changes, classifyChange(ctx, difference.operation, &before, &after, difference.path, difference.base, difference.target))
			}
		}
	}
	sort.Slice(diff.Changes, func(i, j int) bool {
		a, b := diff.Changes[i], diff.Changes[j]
		if a.Address != b.Address {
			return a.Address < b.Address
		}
		if a.Path != b.Path {
			return a.Path < b.Path
		}
		return a.Operation < b.Operation
	})
	consequences := map[string]bool{}
	for _, change := range diff.Changes {
		switch strongestClassification(change.Classifications) {
		case CompatibilityBreaking:
			diff.Summary.Breaking++
		case CompatibilityMigrationRequired:
			diff.Summary.MigrationRequired++
		case CompatibilityUnknown:
			diff.Summary.Unknown++
		default:
			diff.Summary.Compatible++
		}
		for dimension, classification := range change.Classifications {
			if classification.Applicable && classification.Result == CompatibilityMigrationRequired {
				diff.RequiredMigrations = append(diff.RequiredMigrations, map[string]any{"change_id": change.ChangeID, "address": change.Address, "path": change.Path, "dimension": dimension, "rule": classification.Rule})
			}
		}
		if risk := semanticRisk(change); risk != nil {
			diff.RiskRecords = append(diff.RiskRecords, risk)
		}
		for _, artifact := range change.AffectedArtifacts {
			consequences[artifact] = true
		}
	}
	for consequence := range consequences {
		diff.GeneratedConsequences = append(diff.GeneratedConsequences, consequence)
	}
	sort.Strings(diff.GeneratedConsequences)
	sort.Slice(diff.RequiredMigrations, func(i, j int) bool { return canonicalLess(diff.RequiredMigrations[i], diff.RequiredMigrations[j]) })
	sort.Slice(diff.RiskRecords, func(i, j int) bool { return canonicalLess(diff.RiskRecords[i], diff.RiskRecords[j]) })
	diff.Digest = semanticDiffDigest(diff)
	return diff
}

func selectedCompatibilityDimensions(requested []string) []string {
	if len(requested) == 0 {
		return append([]string(nil), compatibilityDimensions...)
	}
	known := map[string]bool{}
	for _, dimension := range compatibilityDimensions {
		known[dimension] = true
	}
	set := map[string]bool{}
	for _, dimension := range requested {
		if known[dimension] {
			set[dimension] = true
		}
	}
	selected := make([]string, 0, len(set))
	for _, dimension := range compatibilityDimensions {
		if set[dimension] {
			selected = append(selected, dimension)
		}
	}
	return selected
}

func semanticDifferences(base, target any, path string) []valueDifference {
	if semanticEqual(base, target) {
		return nil
	}
	if typeExpressionObject(base) || typeExpressionObject(target) {
		return []valueDifference{{operation: "replace", path: path, base: typeExpression(base), target: typeExpression(target)}}
	}
	baseNamed, baseIsNamed := namedSemanticValues(base)
	targetNamed, targetIsNamed := namedSemanticValues(target)
	if baseIsNamed && targetIsNamed {
		var result []valueDifference
		for _, name := range stringUnion(baseNamed, targetNamed) {
			before, beforeOK := baseNamed[name]
			after, afterOK := targetNamed[name]
			childPath := path + "/" + escapeJSONPointer(name)
			switch {
			case !beforeOK:
				result = append(result, valueDifference{operation: "add", path: childPath, target: after})
			case !afterOK:
				result = append(result, valueDifference{operation: "remove", path: childPath, base: before})
			default:
				result = append(result, semanticDifferences(withoutSemanticName(before), withoutSemanticName(after), childPath)...)
			}
		}
		return result
	}
	baseObject, baseIsObject := base.(map[string]any)
	targetObject, targetIsObject := target.(map[string]any)
	if baseIsObject && targetIsObject {
		var result []valueDifference
		for _, key := range stringUnion(baseObject, targetObject) {
			before, beforeOK := baseObject[key]
			after, afterOK := targetObject[key]
			childPath := path + "/" + escapeJSONPointer(key)
			switch {
			case !beforeOK:
				result = append(result, addedSemanticDifferences(childPath, after)...)
			case !afterOK:
				result = append(result, removedSemanticDifferences(childPath, before)...)
			default:
				result = append(result, semanticDifferences(before, after, childPath)...)
			}
		}
		return result
	}
	return []valueDifference{{operation: "replace", path: path, base: base, target: target}}
}

func typeExpressionObject(value any) bool {
	object, ok := value.(map[string]any)
	if !ok {
		return false
	}
	return object["$ref"] != nil || object["$expression"] != nil
}

func withoutSemanticName(value any) any {
	object, ok := value.(map[string]any)
	if !ok {
		return value
	}
	copyObject := make(map[string]any, len(object)-1)
	for key, item := range object {
		if key != "name" {
			copyObject[key] = item
		}
	}
	return copyObject
}

func namedSemanticValues(value any) (map[string]any, bool) {
	if object, ok := value.(map[string]any); ok {
		name, named := object["name"].(string)
		if !named || name == "" {
			return nil, false
		}
		return map[string]any{name: object}, true
	}
	items, ok := value.([]any)
	if !ok {
		return nil, false
	}
	result := map[string]any{}
	for _, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		name, ok := object["name"].(string)
		if !ok || name == "" || result[name] != nil {
			return nil, false
		}
		result[name] = object
	}
	return result, true
}

func addedSemanticDifferences(path string, value any) []valueDifference {
	if named, ok := namedSemanticValues(value); ok {
		result := make([]valueDifference, 0, len(named))
		for _, name := range sortedMapKeys(named) {
			result = append(result, valueDifference{operation: "add", path: path + "/" + escapeJSONPointer(name), target: named[name]})
		}
		return result
	}
	return []valueDifference{{operation: "add", path: path, target: value}}
}

func removedSemanticDifferences(path string, value any) []valueDifference {
	if named, ok := namedSemanticValues(value); ok {
		result := make([]valueDifference, 0, len(named))
		for _, name := range sortedMapKeys(named) {
			result = append(result, valueDifference{operation: "remove", path: path + "/" + escapeJSONPointer(name), base: named[name]})
		}
		return result
	}
	return []valueDifference{{operation: "remove", path: path, base: value}}
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func classifyChange(ctx comparisonContext, operation string, before, after *Resource, path string, base, target any) SemanticChange {
	resource := after
	if resource == nil {
		resource = before
	}
	change := SemanticChange{
		Operation:       operation,
		Address:         resource.Address,
		ExpectedKind:    resource.Kind,
		Path:            path,
		Base:            base,
		Target:          target,
		Classifications: map[string]Classification{},
		Evidence:        []any{},
	}
	if before != nil {
		change.BaseSchemaRevision = resourceSchemaRevision(before.Kind)
	}
	if after != nil {
		change.TargetSchemaRevision = resourceSchemaRevision(after.Kind)
	}
	for _, dimension := range ctx.dimensions {
		change.Classifications[dimension] = classifyDimension(ctx, dimension, operation, before, after, path, base, target)
	}
	change.Evidence = append(change.Evidence, compatibilityEvidence(before, after, path, base, target)...)
	change.Evidence = compactEvidence(change.Evidence)
	change.AffectedArtifacts = affectedArtifacts(before, after, path)
	change.ChangeID = stableChangeID(change)
	return change
}

func compatibilityEvidence(before, after *Resource, path string, base, target any) []any {
	resource := after
	if resource == nil {
		resource = before
	}
	if resource == nil {
		return nil
	}
	kind := canonicalResourceKind(resource.Kind)
	field := pathLeaf(path)
	switch {
	case kind == "binding" && isMCPBinding(*resource) && strings.HasPrefix(path, "/spec/mcp/"):
		if isMCPEffectField(field) {
			return []any{map[string]any{"kind": "mcp_effect", "field": field, "base": base, "target": target}}
		}
	case kind == "mcp-server" && strings.HasSuffix(path, "/approval"):
		return []any{map[string]any{"kind": "mcp_approval", "field": "approval", "base": base, "target": target}}
	case kind == "mcp-connection" && isMCPConnectionImplementationPath(path):
		return []any{map[string]any{"kind": "mcp_connection_identity", "field": field, "base": base, "target": target}}
	case kind == "assistant" && isAssistantImplementationPath(path):
		return []any{map[string]any{"kind": "assistant_implementation", "field": field, "base": base, "target": target}}
	}
	return nil
}

func compactEvidence(values []any) []any {
	result := values[:0]
	for _, value := range values {
		if value != nil {
			result = append(result, value)
		}
	}
	return result
}

func pathLeaf(path string) string {
	path = strings.TrimSuffix(path, "/")
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[index+1:]
	}
	return path
}

func classifyDimension(ctx comparisonContext, dimension, operation string, before, after *Resource, path string, base, target any) Classification {
	resource := after
	if resource == nil {
		resource = before
	}
	if resource == nil || !dimensionApplicable(ctx, dimension, *resource) {
		return notApplicable()
	}
	if dimension == "security" {
		if classification, handled := classifyMCPMetadataChange(dimension, operation, *resource, path, base, target); handled {
			return classification
		}
		return classifySecurityChange(operation, path, base, target)
	}
	if operation == "rename" {
		switch dimension {
		case "source", "generated_client":
			return classified(CompatibilityBreaking, "SCN_COMPAT_RENAME_SYMBOL_CHANGED")
		case "request_wire", "response_wire", "internal_call":
			return classified(CompatibilityCompatible, "SCN_COMPAT_RENAME_WIRE_IDENTITY_PRESERVED")
		default:
			return classified(CompatibilityUnknown, "SCN_COMPAT_RENAME_EXTERNAL_IDENTITY_REQUIRES_EVIDENCE")
		}
	}
	if operation == "add" && path == "" {
		return classifyResourceAddition(dimension, *resource)
	}
	if operation == "remove" && path == "" || path == "/kind" {
		return classified(CompatibilityBreaking, "SCN_COMPAT_RESOURCE_REMOVED_OR_REPLACED")
	}
	if classification, handled := classifyMCPMetadataChange(dimension, operation, *resource, path, base, target); handled {
		return classification
	}
	switch resource.Kind {
	case "scenery.record":
		return classifyRecordChange(dimension, operation, before, after, path, base, target)
	case "scenery.enum", "scenery.union":
		return classifyVariantChange(dimension, operation, before, after, path, base, target)
	case "scenery.operation":
		return classifyOperationChange(dimension, operation, path, base, target)
	case "scenery.binding":
		if isMCPBinding(*resource) {
			return classifyMCPBindingChange(dimension, operation, *resource, path, base, target)
		}
		return classifyBindingChange(dimension, operation, *resource, path, base, target)
	case "scenery.execution":
		return classifyExecutionChange(dimension, path, base, target)
	case "scenery.schedule":
		if dimension == "runtime" || dimension == "deployment" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_SCHEDULE_STATE_MIGRATION_REQUIRED")
		}
	case "scenery.event", "scenery.event-emission":
		if dimension == "runtime" || dimension == "request_wire" || dimension == "response_wire" || dimension == "internal_call" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_EVENT_CONTRACT_MIGRATION_REQUIRED")
		}
	case "scenery.entity", "scenery.data-source", "scenery.provider":
		if dimension == "storage" {
			return classified(CompatibilityUnknown, "SCN_COMPAT_STORAGE_PROVIDER_RULES_UNAVAILABLE")
		}
		if dimension == "deployment" || dimension == "runtime" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_STORAGE_MIGRATION_REQUIRED")
		}
	case "scenery.deployment":
		if dimension == "deployment" {
			if strings.Contains(path, "/replicas") || strings.Contains(path, "/placement") {
				return classified(CompatibilityCompatible, "SCN_COMPAT_DEPLOYMENT_SCALE_CHANGED")
			}
			return classified(CompatibilityUnknown, "SCN_COMPAT_DEPLOYMENT_PROVIDER_RULES_UNAVAILABLE")
		}
	case "scenery.service":
		if strings.HasPrefix(path, "/spec/implementation") || strings.HasPrefix(path, "/spec/lifecycle") {
			if dimension == "runtime" || dimension == "deployment" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_IMPLEMENTATION_BINDING_CHANGED")
			}
			return notApplicable()
		}
	}
	if classification, handled := classifyMCPResourceChange(dimension, operation, *resource, path, base, target); handled {
		return classification
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_UNKNOWN")
}

func classifyResourceAddition(dimension string, resource Resource) Classification {
	if isMCPBinding(resource) {
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
	}
	switch canonicalResourceKind(resource.Kind) {
	case "mcp-server":
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
	case "mcp-connection":
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CONNECTION_READINESS_CHANGED")
	case "assistant":
		return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
	}
	switch resource.Kind {
	case "scenery.operation", "scenery.binding", "scenery.record", "scenery.enum", "scenery.union":
		return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
	case "scenery.entity", "scenery.data-source", "scenery.provider":
		if dimension == "storage" {
			return classified(CompatibilityUnknown, "SCN_COMPAT_STORAGE_PROVIDER_RULES_UNAVAILABLE")
		}
	}
	return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
}

func canonicalResourceKind(kind string) string {
	return strings.TrimPrefix(kind, "scenery.")
}

func isMCPBinding(resource Resource) bool {
	if canonicalResourceKind(resource.Kind) != "binding" {
		return false
	}
	return stringValue(resource.Spec["protocol"]) == "mcp"
}

func isMCPEffectField(field string) bool {
	return oneOf(field, "read_only", "destructive", "idempotent", "open_world", "allow_sensitive_output")
}

func isAssistantImplementationPath(path string) bool {
	return path == "/spec/implementation" || strings.HasPrefix(path, "/spec/implementation/") && oneOf(pathLeaf(path), "adapter", "source", "package", "package_lock")
}

func isMCPConnectionImplementationPath(path string) bool {
	return oneOf(path, "/spec/transport", "/spec/url", "/spec/connect_timeout", "/spec/call_timeout", "/spec/auth") ||
		strings.HasPrefix(path, "/spec/auth/")
}

func classifyMCPMetadataChange(dimension, operation string, resource Resource, path string, base, target any) (Classification, bool) {
	kind := canonicalResourceKind(resource.Kind)
	if kind == "binding" && isMCPBinding(resource) {
		if path == "/spec/mcp" {
			if operation == "remove" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED"), true
			}
			return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED"), true
		}
		if strings.HasPrefix(path, "/spec/mcp/") {
			field := pathLeaf(path)
			switch {
			case isNamedMCPToolPath(path) && oneOf(operation, "add", "remove"):
				return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_TOOL_NAME_CHANGED"), true
			case field == "name":
				return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_TOOL_NAME_CHANGED"), true
			case isMCPEffectField(field):
				if oneOf(dimension, "runtime", "security", "generated_client") {
					return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_EFFECT_METADATA_CHANGED"), true
				}
				return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_EFFECT_METADATA_CHANGED"), true
			}
		}
	}
	if kind == "mcp-server" && strings.HasSuffix(path, "/approval") {
		if oneOf(dimension, "runtime", "security", "generated_client") {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_APPROVAL_POLICY_CHANGED"), true
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_APPROVAL_POLICY_CHANGED"), true
	}
	if kind == "mcp-connection" && isMCPConnectionImplementationPath(path) {
		if dimension == "security" && strings.HasPrefix(path, "/spec/auth") {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED"), true
		}
		if dimension == "security" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED"), true
		}
		if oneOf(dimension, "runtime", "deployment") {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_MCP_CONNECTION_READINESS_CHANGED"), true
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED"), true
	}
	if kind == "assistant" {
		if isAssistantImplementationPath(path) {
			if oneOf(dimension, "runtime", "deployment") {
				return classified(CompatibilityMigrationRequired, "SCN_COMPAT_ASSISTANT_IMPLEMENTATION_REBUILD_REQUIRED"), true
			}
			return classified(CompatibilityCompatible, "SCN_COMPAT_ASSISTANT_IMPLEMENTATION_REBUILD_REQUIRED"), true
		}
		if path == "/spec/surface" || strings.HasPrefix(path, "/spec/surface/") || path == "/spec/mcp_server" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_ASSISTANT_PUBLIC_SURFACE_CHANGED"), true
		}
	}
	_ = operation
	_ = base
	_ = target
	return Classification{}, false
}

func classifyMCPBindingChange(dimension, operation string, resource Resource, path string, base, target any) Classification {
	if path == "/spec/mcp" {
		if operation == "remove" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
	}
	if isNamedMCPToolPath(path) && oneOf(operation, "add", "remove") {
		return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_TOOL_NAME_CHANGED")
	}
	if strings.HasPrefix(path, "/spec/mcp/") {
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_EFFECT_METADATA_CHANGED")
	}
	if path == "/spec/operation" || path == "/spec/execution" || path == "/spec/delivery" {
		return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_BINDING_CHANGED")
	}
	return classifyBindingChange(dimension, operation, resource, path, base, target)
}

func isNamedMCPToolPath(path string) bool {
	parts := strings.Split(strings.TrimPrefix(path, "/spec/"), "/")
	return len(parts) == 2 && parts[0] == "mcp" && parts[1] != ""
}

func classifyMCPResourceChange(dimension, operation string, resource Resource, path string, base, target any) (Classification, bool) {
	switch canonicalResourceKind(resource.Kind) {
	case "mcp-server":
		return classifyMCPServerChange(dimension, operation, path, base, target), true
	case "mcp-connection":
		return classifyMCPConnectionChange(dimension, operation, path, base, target), true
	case "assistant":
		return classifyAssistantChange(dimension, operation, path, base, target), true
	}
	return Classification{}, false
}

func classifyMCPServerChange(dimension, operation, path string, base, target any) Classification {
	if path == "/spec/capability" {
		if operation == "remove" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
	}
	if strings.HasPrefix(path, "/spec/capability/") {
		parts := strings.Split(strings.TrimPrefix(path, "/spec/"), "/")
		if len(parts) == 2 {
			if operation == "add" {
				return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
			}
			if operation == "remove" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED")
			}
		}
		if pathLeaf(path) == "name" || pathLeaf(path) == "binding" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_BINDING_CHANGED")
		}
		if pathLeaf(path) == "approval" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_APPROVAL_POLICY_CHANGED")
		}
	}
	if path == "/spec/connection" {
		if operation == "remove" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
	}
	if strings.HasPrefix(path, "/spec/connection/") {
		if operation == "add" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CAPABILITY_ADDED")
		}
		if operation == "remove" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CAPABILITY_REMOVED")
		}
		return classified(CompatibilityMigrationRequired, "SCN_COMPAT_MCP_CONNECTION_READINESS_CHANGED")
	}
	if oneOf(path, "/spec/max_input_bytes", "/spec/max_result_bytes") {
		comparison, ok := compareNumbers(base, target)
		if !ok {
			return classified(CompatibilityUnknown, "SCN_COMPAT_MCP_LIMIT_CHANGED")
		}
		if comparison < 0 {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_LIMIT_CHANGED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_LIMIT_CHANGED")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_MCP_SERVER_CHANGE_UNKNOWN")
}

func classifyMCPConnectionChange(dimension, operation, path string, base, target any) Classification {
	if strings.HasPrefix(path, "/spec/tools/") {
		if oneOf(dimension, "runtime", "generated_client", "response_wire", "deployment") {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_FILTER_CHANGED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_FILTER_CHANGED")
	}
	if isMCPConnectionImplementationPath(path) {
		if dimension == "security" && strings.HasPrefix(path, "/spec/auth") {
			return classified(CompatibilityBreaking, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED")
		}
		if dimension == "security" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED")
		}
		if oneOf(dimension, "runtime", "deployment") {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_MCP_CONNECTION_READINESS_CHANGED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_MCP_CONNECTION_IMPLEMENTATION_CHANGED")
	}
	if operation == "add" {
		return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_MCP_CONNECTION_CHANGE_UNKNOWN")
}

func classifyAssistantChange(dimension, operation, path string, base, target any) Classification {
	if isAssistantImplementationPath(path) {
		if oneOf(dimension, "runtime", "deployment") {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_ASSISTANT_IMPLEMENTATION_REBUILD_REQUIRED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_ASSISTANT_IMPLEMENTATION_REBUILD_REQUIRED")
	}
	if path == "/spec/surface" || strings.HasPrefix(path, "/spec/surface/") || path == "/spec/mcp_server" {
		return classified(CompatibilityBreaking, "SCN_COMPAT_ASSISTANT_PUBLIC_SURFACE_CHANGED")
	}
	if operation == "add" {
		return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_ASSISTANT_CHANGE_UNKNOWN")
}
