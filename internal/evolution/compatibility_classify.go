package evolution

import (
	"fmt"
	"maps"
	"strings"

	"scenery.sh/internal/graph"
)

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
	if after != nil && after.Kind == "scenery.union" || after == nil && before != nil && before.Kind == "scenery.union" {
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
		if strings.Contains(path, "/codec_profile") || strings.Contains(path, "/content_type") || strings.Contains(path, "/path_parameter/") || strings.Contains(path, "/path_tail/") || strings.Contains(path, "/query_parameter/") || strings.Contains(path, "/header/") || strings.Contains(path, "/cookie/") || strings.Contains(path, "/body/") {
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
	if path == "/spec/http/path" && (strings.Contains(fmt.Sprint(base), "...}") || strings.Contains(fmt.Sprint(target), "...}")) {
		return Classification{Applicable: true, Result: CompatibilityUnknown, Relation: SecurityUnknown, Rule: "SCN_COMPAT_SECURITY_UNKNOWN"}
	}
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

func ClassifySecurityChange(operation, path string, base, target any) Classification {
	return classifySecurityChange(operation, path, base, target)
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
	maps.Copy(resources, base)
	maps.Copy(resources, target)
	positions := map[string]typePosition{}
	visited := map[string]bool{}
	var mark func(module string, value any, input bool)
	mark = func(module string, value any, input bool) {
		for _, name := range referencedTypeNames(value) {
			parts := strings.Split(name, ".")
			if len(parts) != 2 || !oneOf(parts[0], "record", "enum", "union") {
				continue
			}
			address := graph.ResourceAddress(module, parts[0], parts[1])
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
			case "scenery.record":
				for _, field := range namedChildren(resource.Spec, "field") {
					mark(resource.Module, field["type"], input)
				}
			case "scenery.union":
				for _, variant := range namedChildren(resource.Spec, "variant") {
					mark(resource.Module, variant["type"], input)
				}
			}
		}
	}
	for _, resource := range resources {
		if resource.Kind != "scenery.operation" {
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
	case "scenery.record", "scenery.enum", "scenery.union":
		if dimension == "request_wire" {
			return ctx.typePositions[resource.Address].input
		}
		if dimension == "response_wire" {
			return ctx.typePositions[resource.Address].output
		}
		return oneOf(dimension, "source", "generated_client")
	case "scenery.operation":
		return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "internal_call", "runtime", "deployment")
	case "scenery.binding":
		switch protocol {
		case "http":
			return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "runtime", "security", "deployment")
		case "mcp":
			return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "internal_call", "runtime", "security", "deployment")
		case "internal":
			return oneOf(dimension, "source", "generated_client", "internal_call", "runtime", "security", "deployment")
		case "cli":
			return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "runtime", "security")
		case "event":
			return oneOf(dimension, "request_wire", "response_wire", "internal_call", "runtime", "security", "deployment")
		}
	case "scenery.mcp-server":
		return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "internal_call", "runtime", "security", "deployment")
	case "scenery.mcp-connection":
		return oneOf(dimension, "source", "runtime", "security", "deployment")
	case "scenery.assistant":
		return oneOf(dimension, "source", "request_wire", "response_wire", "generated_client", "runtime", "security", "deployment")
	case "scenery.http-gateway", "scenery.authentication", "scenery.authorization", "scenery.pipeline", "scenery.secret", "scenery.secret-store":
		return oneOf(dimension, "source", "request_wire", "runtime", "security", "deployment")
	case "scenery.execution", "scenery.execution-engine", "scenery.go-target", "scenery.go-toolchain", "scenery.go-module":
		return oneOf(dimension, "runtime", "deployment")
	case "scenery.schedule", "scenery.event", "scenery.event-emission":
		return oneOf(dimension, "request_wire", "response_wire", "internal_call", "runtime", "deployment")
	case "scenery.entity", "scenery.data-source", "scenery.view", "scenery.crud", "scenery.fixture", "scenery.provider":
		return oneOf(dimension, "source", "runtime", "storage", "deployment")
	case "scenery.deployment":
		return dimension == "deployment"
	case "scenery.service", "scenery.module":
		return oneOf(dimension, "source", "generated_client", "runtime", "deployment")
	}
	return true
}
