package vnext

import (
	"fmt"
	"sort"
	"strings"
)

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

type RenameReceipt struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Digest string `json:"digest,omitempty"`
}

type CompareOptions struct {
	View       string          `json:"view,omitempty"`
	Dimensions []string        `json:"dimensions,omitempty"`
	Scope      string          `json:"scope,omitempty"`
	Renames    []RenameReceipt `json:"renames,omitempty"`
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

type VersionRecommendation struct {
	Level     string `json:"level"`
	Migration bool   `json:"migration"`
}

type SemanticDiff struct {
	APIVersion            string                `json:"api_version"`
	Profile               string                `json:"profile"`
	CatalogDigest         string                `json:"catalog_digest"`
	BaseRevision          string                `json:"base_revision,omitempty"`
	TargetRevision        string                `json:"target_revision,omitempty"`
	View                  string                `json:"view"`
	Scope                 string                `json:"scope,omitempty"`
	Dimensions            []string              `json:"dimensions"`
	Changes               []SemanticChange      `json:"changes"`
	Summary               DiffSummary           `json:"summary"`
	RequiredMigrations    []any                 `json:"required_migrations"`
	GeneratedConsequences []string              `json:"generated_consequences"`
	RiskRecords           []any                 `json:"risk_records"`
	VersionRecommendation VersionRecommendation `json:"version_recommendation"`
	Digest                string                `json:"comparison_digest"`
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
		APIVersion:            "scenery.semantic-diff/v1",
		Profile:               "scenery.compatibility-core/v1",
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
	}
	ctx := comparisonContext{base: resourcesByAddress(base), target: resourcesByAddress(target), dimensions: dimensions}
	ctx.typePositions = compatibilityTypePositions(ctx.base, ctx.target)
	consumedBase, consumedTarget := map[string]bool{}, map[string]bool{}
	renames := append([]RenameReceipt(nil), options.Renames...)
	sort.Slice(renames, func(i, j int) bool {
		if renames[i].From != renames[j].From {
			return renames[i].From < renames[j].From
		}
		return renames[i].To < renames[j].To
	})
	for _, receipt := range renames {
		before, beforeOK := ctx.base[receipt.From]
		after, afterOK := ctx.target[receipt.To]
		if !beforeOK || !afterOK || before.Kind != after.Kind || consumedBase[receipt.From] || consumedTarget[receipt.To] {
			continue
		}
		consumedBase[receipt.From], consumedTarget[receipt.To] = true, true
		change := classifyChange(ctx, "rename", &before, &after, "/address", receipt.From, receipt.To)
		change.Evidence = append(change.Evidence, map[string]any{"kind": "rename_receipt", "from": receipt.From, "to": receipt.To, "digest": receipt.Digest})
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
	diff.VersionRecommendation = recommendSemanticVersion(diff.Changes)
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
	change.AffectedArtifacts = affectedArtifacts(before, after, path)
	change.ChangeID = stableChangeID(change)
	return change
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
	switch resource.Kind {
	case "scenery.record/v1":
		return classifyRecordChange(dimension, operation, before, after, path, base, target)
	case "scenery.enum/v1", "scenery.union/v1":
		return classifyVariantChange(dimension, operation, before, after, path, base, target)
	case "scenery.operation/v1":
		return classifyOperationChange(dimension, operation, path, base, target)
	case "scenery.binding/v1":
		return classifyBindingChange(dimension, operation, *resource, path, base, target)
	case "scenery.execution/v1":
		return classifyExecutionChange(dimension, path, base, target)
	case "scenery.schedule/v1":
		if dimension == "runtime" || dimension == "deployment" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_SCHEDULE_STATE_MIGRATION_REQUIRED")
		}
	case "scenery.event/v1", "scenery.event-emission/v1":
		if dimension == "runtime" || dimension == "request_wire" || dimension == "response_wire" || dimension == "internal_call" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_EVENT_CONTRACT_MIGRATION_REQUIRED")
		}
	case "scenery.entity/v1", "scenery.data-source/v1", "scenery.provider/v1":
		if dimension == "storage" {
			return classified(CompatibilityUnknown, "SCN_COMPAT_STORAGE_PROVIDER_RULES_UNAVAILABLE")
		}
		if dimension == "deployment" || dimension == "runtime" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_STORAGE_MIGRATION_REQUIRED")
		}
	case "scenery.deployment/v1":
		if dimension == "deployment" {
			if strings.Contains(path, "/replicas") || strings.Contains(path, "/placement") {
				return classified(CompatibilityCompatible, "SCN_COMPAT_DEPLOYMENT_SCALE_CHANGED")
			}
			return classified(CompatibilityUnknown, "SCN_COMPAT_DEPLOYMENT_PROVIDER_RULES_UNAVAILABLE")
		}
	case "scenery.service/v1":
		if strings.HasPrefix(path, "/spec/implementation") || strings.HasPrefix(path, "/spec/lifecycle") {
			if dimension == "runtime" || dimension == "deployment" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_IMPLEMENTATION_BINDING_CHANGED")
			}
			return notApplicable()
		}
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_UNKNOWN")
}

func classifyResourceAddition(dimension string, resource Resource) Classification {
	switch resource.Kind {
	case "scenery.operation/v1", "scenery.binding/v1", "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1":
		return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
	case "scenery.entity/v1", "scenery.data-source/v1", "scenery.provider/v1":
		if dimension == "storage" {
			return classified(CompatibilityUnknown, "SCN_COMPAT_STORAGE_PROVIDER_RULES_UNAVAILABLE")
		}
	}
	return classified(CompatibilityCompatible, "SCN_COMPAT_ADDITION")
}

func classifyRecordChange(dimension, operation string, before, after *Resource, path string, base, target any) Classification {
	if child, exact := namedChildAtPath(path, "field"); exact && child != "" {
		if operation == "add" {
			field, _ := target.(map[string]any)
			optional := isOptionalType(field["type"])
			switch dimension {
			case "request_wire":
				if optional {
					return classified(CompatibilityCompatible, "SCN_COMPAT_OPTIONAL_INPUT_FIELD_ADDED")
				}
				return classified(CompatibilityBreaking, "SCN_COMPAT_REQUIRED_INPUT_FIELD_ADDED")
			case "response_wire":
				if before != nil && before.Spec["unknown_fields"] == "preserve" {
					return classified(CompatibilityCompatible, "SCN_COMPAT_PRESERVING_RESPONSE_FIELD_ADDED")
				}
				return classified(CompatibilityBreaking, "SCN_COMPAT_CLOSED_RESPONSE_FIELD_ADDED")
			case "source", "generated_client":
				if optional {
					return classified(CompatibilityCompatible, "SCN_COMPAT_OPTIONAL_FIELD_API_ADDED")
				}
				return classified(CompatibilityBreaking, "SCN_COMPAT_REQUIRED_FIELD_API_ADDED")
			}
		}
		if operation == "remove" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_RECORD_FIELD_REMOVED")
		}
	}
	if strings.HasPrefix(path, "/spec/field/") && strings.Contains(path, "/type") {
		return classifyTypeTransition(dimension, fmt.Sprint(base), fmt.Sprint(target))
	}
	if strings.HasPrefix(path, "/spec/field/") && strings.HasSuffix(path, "/wire_name") {
		return classified(CompatibilityBreaking, "SCN_COMPAT_WIRE_NAME_CHANGED")
	}
	if constraintName(path) != "" {
		return classifyConstraintChange(dimension, path, base, target)
	}
	if strings.HasSuffix(path, "/default") {
		return classified(CompatibilityBreaking, "SCN_COMPAT_DEFAULT_CHANGED")
	}
	if path == "/spec/unknown_fields" {
		if target == "preserve" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_RECORD_OPENED")
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_RECORD_CLOSED")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_RECORD_CHANGE_UNKNOWN")
}

func classifyTypeTransition(dimension, base, target string) Classification {
	if base == target {
		return classified(CompatibilityCompatible, "SCN_COMPAT_TYPE_UNCHANGED")
	}
	if dimension == "source" || dimension == "generated_client" {
		return classified(CompatibilityBreaking, "SCN_COMPAT_GENERATED_TYPE_CHANGED")
	}
	baseOptional, targetOptional := wrappedType(base, "optional"), wrappedType(target, "optional")
	if baseOptional != targetOptional {
		if dimension == "request_wire" {
			if !baseOptional && targetOptional {
				return classified(CompatibilityCompatible, "SCN_COMPAT_INPUT_REQUIRED_TO_OPTIONAL")
			}
			return classified(CompatibilityBreaking, "SCN_COMPAT_INPUT_OPTIONAL_TO_REQUIRED")
		}
		if dimension == "response_wire" {
			if baseOptional && !targetOptional {
				return classified(CompatibilityCompatible, "SCN_COMPAT_OUTPUT_OPTIONAL_TO_REQUIRED")
			}
			return classified(CompatibilityBreaking, "SCN_COMPAT_OUTPUT_REQUIRED_TO_OPTIONAL")
		}
	}
	baseNullable, targetNullable := hasTypeWrapper(base, "nullable"), hasTypeWrapper(target, "nullable")
	if baseNullable != targetNullable {
		if dimension == "request_wire" && !baseNullable && targetNullable {
			return classified(CompatibilityCompatible, "SCN_COMPAT_INPUT_NULLABILITY_LOOSENED")
		}
		if dimension == "response_wire" && !baseNullable && targetNullable {
			return classified(CompatibilityBreaking, "SCN_COMPAT_OUTPUT_NULLABILITY_LOOSENED")
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_NULLABILITY_NARROWED")
	}
	baseScalar, targetScalar := innermostType(base), innermostType(target)
	if numericWidening(baseScalar, targetScalar) {
		if dimension == "request_wire" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_NUMERIC_INPUT_WIDENED")
		}
		if dimension == "response_wire" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_NUMERIC_OUTPUT_WIDENED")
		}
	}
	if numericWidening(targetScalar, baseScalar) {
		if dimension == "request_wire" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_NUMERIC_INPUT_NARROWED")
		}
		if dimension == "response_wire" {
			return classified(CompatibilityCompatible, "SCN_COMPAT_NUMERIC_OUTPUT_NARROWED")
		}
	}
	return classified(CompatibilityBreaking, "SCN_COMPAT_TYPE_OR_WIRE_REPRESENTATION_CHANGED")
}

func classifyConstraintChange(dimension, path string, base, target any) Classification {
	if strings.HasSuffix(path, "/pattern") {
		return classified(CompatibilityUnknown, "SCN_COMPAT_PATTERN_RELATION_UNKNOWN")
	}
	if base == nil || target == nil {
		tightened := base == nil
		if dimension == "request_wire" {
			if tightened {
				return classified(CompatibilityBreaking, "SCN_COMPAT_INPUT_CONSTRAINT_TIGHTENED")
			}
			return classified(CompatibilityCompatible, "SCN_COMPAT_INPUT_CONSTRAINT_LOOSENED")
		}
		if dimension == "response_wire" {
			if tightened {
				return classified(CompatibilityCompatible, "SCN_COMPAT_OUTPUT_GUARANTEE_TIGHTENED")
			}
			return classified(CompatibilityBreaking, "SCN_COMPAT_OUTPUT_GUARANTEE_LOOSENED")
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_CONSTRAINT_API_CHANGED")
	}
	comparison, ok := compareNumbers(base, target)
	if !ok {
		return classified(CompatibilityUnknown, "SCN_COMPAT_CONSTRAINT_RELATION_UNKNOWN")
	}
	name := constraintName(path)
	tightened := (strings.HasPrefix(name, "min") && comparison < 0) || (strings.HasPrefix(name, "max") && comparison > 0)
	if dimension == "request_wire" {
		if tightened {
			return classified(CompatibilityBreaking, "SCN_COMPAT_INPUT_CONSTRAINT_TIGHTENED")
		}
		return classified(CompatibilityCompatible, "SCN_COMPAT_INPUT_CONSTRAINT_LOOSENED")
	}
	if dimension == "response_wire" {
		if tightened {
			return classified(CompatibilityCompatible, "SCN_COMPAT_OUTPUT_GUARANTEE_TIGHTENED")
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_OUTPUT_GUARANTEE_LOOSENED")
	}
	return classified(CompatibilityBreaking, "SCN_COMPAT_CONSTRAINT_API_CHANGED")
}

func classifyVariantChange(dimension, operation string, before, after *Resource, path string, baseValue, targetValue any) Classification {
	childKind := "value"
	if after != nil && after.Kind == "scenery.union/v1" || after == nil && before != nil && before.Kind == "scenery.union/v1" {
		childKind = "variant"
	}
	if _, exact := namedChildAtPath(path, childKind); exact {
		if operation == "add" {
			switch dimension {
			case "request_wire":
				return classified(CompatibilityCompatible, "SCN_COMPAT_INPUT_VARIANT_ADDED")
			case "response_wire":
				if before != nil && before.Spec["open"] == true {
					return classified(CompatibilityCompatible, "SCN_COMPAT_OPEN_OUTPUT_VARIANT_ADDED")
				}
				return classified(CompatibilityBreaking, "SCN_COMPAT_CLOSED_OUTPUT_VARIANT_ADDED")
			case "generated_client":
				if before != nil && before.Spec["open"] == true {
					return classified(CompatibilityCompatible, "SCN_COMPAT_OPEN_GENERATED_VARIANT_ADDED")
				}
				return classified(CompatibilityBreaking, "SCN_COMPAT_EXHAUSTIVE_VARIANT_ADDED")
			case "source":
				return classified(CompatibilityCompatible, "SCN_COMPAT_VARIANT_ADDED")
			}
		}
		if operation == "remove" {
			switch dimension {
			case "request_wire", "source", "generated_client":
				return classified(CompatibilityBreaking, "SCN_COMPAT_VARIANT_REMOVED")
			case "response_wire":
				return classified(CompatibilityCompatible, "SCN_COMPAT_OUTPUT_VARIANT_REMOVED")
			}
		}
	}
	if path == "/spec/open" {
		baseOpen, _ := baseValue.(bool)
		targetOpen, _ := targetValue.(bool)
		if !baseOpen && targetOpen {
			switch dimension {
			case "request_wire", "response_wire", "runtime", "internal_call":
				return classified(CompatibilityCompatible, "SCN_COMPAT_OPENNESS_WIRE_SET_UNCHANGED")
			default:
				return classified(CompatibilityBreaking, "SCN_COMPAT_OPENNESS_GENERATED_REPRESENTATION_CHANGED")
			}
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_OPENNESS_CHANGED")
	}
	if strings.HasSuffix(path, "/wire_value") || strings.Contains(path, "/tag") {
		return classified(CompatibilityBreaking, "SCN_COMPAT_VARIANT_WIRE_IDENTITY_CHANGED")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_VARIANT_CHANGE_UNKNOWN")
}

func classifyOperationChange(dimension, operation, path string, base, target any) Classification {
	if _, exact := namedChildAtPath(path, "result"); exact || func() bool { _, ok := namedChildAtPath(path, "error"); return ok }() {
		if operation == "add" {
			if dimension == "response_wire" || dimension == "generated_client" || dimension == "internal_call" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_OPERATION_OUTCOME_ADDED")
			}
			return notApplicable()
		}
		if operation == "remove" {
			if dimension == "response_wire" {
				return classified(CompatibilityCompatible, "SCN_COMPAT_OPERATION_OUTCOME_REMOVED_FROM_WIRE")
			}
			if dimension == "generated_client" || dimension == "source" || dimension == "runtime" || dimension == "internal_call" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_OPERATION_OUTCOME_REMOVED")
			}
		}
	}
	if strings.HasPrefix(path, "/spec/input") {
		return classifyTypeTransition(dimension, fmt.Sprint(base), fmt.Sprint(target))
	}
	if strings.HasPrefix(path, "/spec/handler") {
		if dimension == "runtime" || dimension == "deployment" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_HANDLER_BINDING_CHANGED")
		}
		return notApplicable()
	}
	if strings.HasPrefix(path, "/spec/idempotency") {
		if dimension == "runtime" || dimension == "generated_client" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_IDEMPOTENCY_CHANGED")
		}
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_OPERATION_CHANGE_UNKNOWN")
}

func classifyBindingChange(dimension, operation string, resource Resource, path string, base, target any) Classification {
	protocol := stringValue(resource.Spec["protocol"])
	if protocol == "" && resource.Spec["http"] != nil {
		protocol = "http"
	}
	if protocol == "http" {
		if path == "/spec/http/guarantee" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_HTTP_GUARANTEE_MIGRATION_REQUIRED")
		}
		if path == "/spec/http/path" || path == "/spec/http/method" || path == "/spec/gateway" || path == "/spec/gateway/$ref" {
			if dimension == "source" || dimension == "request_wire" || dimension == "generated_client" || dimension == "runtime" || dimension == "deployment" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_ROUTE_IDENTITY_CHANGED")
			}
			return notApplicable()
		}
		if strings.Contains(path, "/response/") {
			if operation == "add" && strings.Contains(path, "/media") {
				return classified(CompatibilityUnknown, "SCN_COMPAT_HTTP_NEGOTIATION_ADDITION_UNKNOWN")
			}
			if dimension == "response_wire" || dimension == "generated_client" || dimension == "runtime" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_HTTP_RESPONSE_CHANGED")
			}
			if dimension == "source" {
				return classified(CompatibilityCompatible, "SCN_COMPAT_HTTP_TRANSPORT_RESPONSE_DECLARATION_CHANGED")
			}
			return notApplicable()
		}
		if strings.Contains(path, "/codec_profile") || strings.Contains(path, "/content_type") || strings.Contains(path, "/path_parameter/") || strings.Contains(path, "/query_parameter/") || strings.Contains(path, "/header/") || strings.Contains(path, "/cookie/") || strings.Contains(path, "/body/") {
			if dimension == "request_wire" || dimension == "response_wire" || dimension == "generated_client" || dimension == "runtime" {
				return classified(CompatibilityBreaking, "SCN_COMPAT_HTTP_MAPPING_OR_CODEC_CHANGED")
			}
		}
		if strings.Contains(path, "limit") || strings.Contains(path, "max_") {
			if base == nil || target == nil {
				return classified(CompatibilityBreaking, "SCN_COMPAT_HTTP_LIMIT_BOUNDARY_CHANGED")
			}
			comparison, ok := compareNumbers(base, target)
			if !ok {
				return classified(CompatibilityUnknown, "SCN_COMPAT_HTTP_LIMIT_RELATION_UNKNOWN")
			}
			if dimension == "request_wire" && comparison > 0 {
				return classified(CompatibilityBreaking, "SCN_COMPAT_HTTP_REQUEST_LIMIT_TIGHTENED")
			}
			return classified(CompatibilityCompatible, "SCN_COMPAT_HTTP_LIMIT_RAISED")
		}
		if strings.Contains(path, "/timeouts") {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_HTTP_TIMEOUT_MIGRATION_REQUIRED")
		}
	}
	if protocol == "internal" {
		if dimension == "internal_call" || dimension == "generated_client" || dimension == "runtime" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_INTERNAL_BINDING_CHANGED")
		}
	}
	if protocol == "cli" && (strings.Contains(path, "/command") || strings.Contains(path, "/argument/") || strings.Contains(path, "/flag/")) {
		if dimension == "source" || dimension == "request_wire" || dimension == "generated_client" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_CLI_IDENTITY_CHANGED")
		}
	}
	if protocol == "event" {
		if dimension == "runtime" || dimension == "request_wire" || dimension == "internal_call" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_EVENT_BINDING_MIGRATION_REQUIRED")
		}
	}
	if strings.HasPrefix(path, "/spec/delivery") || strings.HasPrefix(path, "/spec/execution") {
		if dimension == "runtime" || dimension == "response_wire" || dimension == "generated_client" || dimension == "internal_call" {
			return classified(CompatibilityBreaking, "SCN_COMPAT_DELIVERY_OR_EXECUTION_CHANGED")
		}
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_BINDING_CHANGE_UNKNOWN")
}

func classifyExecutionChange(dimension, path string, base, target any) Classification {
	if strings.Contains(path, "/revision") || strings.Contains(path, "/lease") || strings.Contains(path, "/retry") || strings.Contains(path, "/retention") || strings.Contains(path, "/deduplication") || strings.Contains(path, "/external_name") {
		if dimension == "runtime" || dimension == "deployment" {
			return classified(CompatibilityMigrationRequired, "SCN_COMPAT_DURABLE_MIGRATION_REQUIRED")
		}
	}
	if strings.Contains(path, "/timeout") || strings.Contains(path, "/concurrency") || strings.Contains(path, "/attempts") {
		comparison, ok := compareNumbers(base, target)
		if ok && comparison <= 0 {
			return classified(CompatibilityCompatible, "SCN_COMPAT_RUNTIME_CAPACITY_LOOSENED")
		}
		return classified(CompatibilityBreaking, "SCN_COMPAT_RUNTIME_CAPACITY_TIGHTENED")
	}
	if strings.Contains(path, "/mode") || strings.Contains(path, "/engine") {
		return classified(CompatibilityBreaking, "SCN_COMPAT_EXECUTION_MODE_CHANGED")
	}
	return classified(CompatibilityUnknown, "SCN_COMPAT_EXECUTION_CHANGE_UNKNOWN")
}

func classifySecurityChange(operation, path string, base, target any) Classification {
	if !securityRelevantPath(path) && operation != "remove" {
		return Classification{Applicable: true, Result: CompatibilityCompatible, Relation: SecurityEqual, Rule: "SCN_COMPAT_SECURITY_UNCHANGED"}
	}
	relation := securityRelation(path, base, target)
	switch relation {
	case SecurityEqual:
		return Classification{Applicable: true, Result: CompatibilityCompatible, Relation: relation, Rule: "SCN_COMPAT_SECURITY_UNCHANGED"}
	case SecurityStronger:
		return Classification{Applicable: true, Result: CompatibilityBreaking, Relation: relation, Rule: "SCN_COMPAT_SECURITY_STRENGTHENED_CALLER_BREAKING"}
	case SecurityWeaker:
		return Classification{Applicable: true, Result: CompatibilityBreaking, Relation: relation, Rule: "SCN_COMPAT_SECURITY_WEAKENED"}
	case SecurityIncomparable:
		return Classification{Applicable: true, Result: CompatibilityBreaking, Relation: relation, Rule: "SCN_COMPAT_SECURITY_INCOMPARABLE"}
	default:
		return Classification{Applicable: true, Result: CompatibilityUnknown, Relation: SecurityUnknown, Rule: "SCN_COMPAT_SECURITY_UNKNOWN"}
	}
}

func securityRelation(path string, base, target any) string {
	baseText, targetText := fmt.Sprint(base), fmt.Sprint(target)
	if baseText == targetText {
		return SecurityEqual
	}
	if strings.Contains(path, "/exposure") {
		rank := map[string]int{"local": 0, "application": 1, "private_network": 2, "internet": 3}
		before, beforeOK := rank[baseText]
		after, afterOK := rank[targetText]
		if beforeOK && afterOK {
			if after > before {
				return SecurityWeaker
			}
			return SecurityStronger
		}
	}
	if strings.Contains(path, "/authentication") {
		return rankedSecurityRelation(authenticationStrength(baseText), authenticationStrength(targetText))
	}
	if strings.Contains(path, "/authorization") {
		return rankedSecurityRelation(authorizationStrength(baseText), authorizationStrength(targetText))
	}
	if strings.Contains(path, "/principal") || strings.Contains(path, "/tenant") || strings.Contains(path, "/credential") {
		return SecurityIncomparable
	}
	if strings.Contains(path, "/sensitive") && target == false {
		return SecurityWeaker
	}
	return SecurityUnknown
}

func authenticationStrength(value string) int {
	if strings.Contains(value, "std.authentication.none") {
		return 0
	}
	return 1
}

func authorizationStrength(value string) int {
	switch {
	case strings.Contains(value, "std.authorization.none"):
		return 2
	case strings.Contains(value, "std.authorization.public"):
		return 0
	default:
		return 1
	}
}

func rankedSecurityRelation(base, target int) string {
	switch {
	case base == target:
		return SecurityIncomparable
	case target > base:
		return SecurityStronger
	default:
		return SecurityWeaker
	}
}

func compatibilityTypePositions(base, target map[string]Resource) map[string]typePosition {
	resources := make(map[string]Resource, len(base)+len(target))
	for address, resource := range base {
		resources[address] = resource
	}
	for address, resource := range target {
		resources[address] = resource
	}
	positions := map[string]typePosition{}
	visited := map[string]bool{}
	var mark func(module string, value any, input bool)
	mark = func(module string, value any, input bool) {
		for _, name := range referencedTypeNames(value) {
			parts := strings.Split(name, ".")
			if len(parts) != 2 || !oneOf(parts[0], "record", "enum", "union") {
				continue
			}
			address := resourceAddress(module, parts[0], parts[1])
			resource, ok := resources[address]
			if !ok {
				continue
			}
			position := positions[address]
			if input {
				position.input = true
			} else {
				position.output = true
			}
			positions[address] = position
			visitKey := address + "/" + map[bool]string{true: "input", false: "output"}[input]
			if visited[visitKey] {
				continue
			}
			visited[visitKey] = true
			switch resource.Kind {
			case "scenery.record/v1":
				for _, field := range namedChildren(resource.Spec, "field") {
					mark(resource.Module, field["type"], input)
				}
			case "scenery.union/v1":
				for _, variant := range namedChildren(resource.Spec, "variant") {
					mark(resource.Module, variant["type"], input)
				}
			}
		}
	}
	for _, resource := range resources {
		if resource.Kind != "scenery.operation/v1" {
			continue
		}
		mark(resource.Module, resource.Spec["input"], true)
		for _, childKind := range []string{"result", "error"} {
			for _, child := range namedChildren(resource.Spec, childKind) {
				mark(resource.Module, child["type"], false)
			}
		}
	}
	return positions
}

func referencedTypeNames(value any) []string {
	if ref := refString(value); ref != "" {
		return []string{ref}
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return typeExpressionNames(raw)
		}
	}
	return nil
}

func dimensionApplicable(ctx comparisonContext, dimension string, resource Resource) bool {
	protocol := stringValue(resource.Spec["protocol"])
	if protocol == "" && resource.Spec["http"] != nil {
		protocol = "http"
	}
	switch resource.Kind {
	case "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1":
		if dimension == "request_wire" {
			return ctx.typePositions[resource.Address].input
		}
		if dimension == "response_wire" {
			return ctx.typePositions[resource.Address].output
		}
		return oneOf(dimension, "source", "generated_client")
	case "scenery.operation/v1":
		return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "internal_call", "runtime", "deployment")
	case "scenery.binding/v1":
		switch protocol {
		case "http":
			return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "runtime", "security", "deployment")
		case "internal":
			return oneOf(dimension, "source", "generated_client", "internal_call", "runtime", "security", "deployment")
		case "cli":
			return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "runtime", "security")
		case "event":
			return oneOf(dimension, "request_wire", "response_wire", "internal_call", "runtime", "security", "deployment")
		}
	case "scenery.http-gateway/v1", "scenery.authentication/v1", "scenery.authorization/v1", "scenery.pipeline/v1", "scenery.secret/v1", "scenery.secret-store/v1":
		return oneOf(dimension, "source", "request_wire", "runtime", "security", "deployment")
	case "scenery.execution/v1", "scenery.execution-engine/v1", "scenery.go-target/v1", "scenery.go-toolchain/v1", "scenery.go-module/v1":
		return oneOf(dimension, "runtime", "deployment")
	case "scenery.schedule/v1", "scenery.event/v1", "scenery.event-emission/v1":
		return oneOf(dimension, "request_wire", "response_wire", "internal_call", "runtime", "deployment")
	case "scenery.entity/v1", "scenery.data-source/v1", "scenery.view/v1", "scenery.crud/v1", "scenery.fixture/v1", "scenery.provider/v1":
		return oneOf(dimension, "source", "runtime", "storage", "deployment")
	case "scenery.deployment/v1":
		return dimension == "deployment"
	case "scenery.service/v1", "scenery.module/v1":
		return oneOf(dimension, "source", "generated_client", "runtime", "deployment")
	}
	return true
}
