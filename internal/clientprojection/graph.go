package clientprojection

import (
	"fmt"
	"maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"scenery.sh/internal/graph"
)

func ResourcesByAddress(manifest *Manifest) map[string]Resource {
	result := map[string]Resource{}
	if manifest != nil {
		for _, resource := range manifest.Resources {
			result[resource.Address] = resource
		}
	}
	return result
}

func ResourceAddress(module, blockType, name string) string {
	return graph.ResourceAddress(module, blockType, name)
}

func DefaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func StringValues(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
			continue
		}
		if scalar, ok := item.(map[string]any); ok {
			if text, ok := scalar["value"].(string); ok {
				values = append(values, text)
			}
		}
	}
	return values
}

func CloneMapValue(value any) map[string]any {
	result := map[string]any{}
	if source, ok := value.(map[string]any); ok {
		maps.Copy(result, source)
	}
	return result
}

func IntegerValue(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), typed == float64(int(typed))
	case string:
		parsed, err := strconv.Atoi(typed)
		return parsed, err == nil
	case map[string]any:
		if typed["$scalar"] != "int" {
			return 0, false
		}
		parsed, err := strconv.Atoi(StringValue(typed["value"]))
		return parsed, err == nil
	default:
		return 0, false
	}
}

type OperationInputField struct {
	Name       string
	WireName   string
	Type       any
	Optional   bool
	HasDefault bool
	EnumValues []string
}
type OperationInputShape struct {
	Type   any
	Unit   bool
	Record *Resource
	Fields map[string]OperationInputField
}

func ResolveOperationInputShape(resources map[string]Resource, operation Resource) OperationInputShape {
	shape := OperationInputShape{Type: operation.Spec["input"], Fields: map[string]OperationInputField{}}
	reference := RefString(shape.Type)
	if reference == "std.type.unit" {
		shape.Unit = true
		return shape
	}
	record, ok := resources[ResolveResourceRef(operation, reference, "record")]
	if !ok {
		return shape
	}
	shape.Record = &record
	for _, field := range NamedChildren(record.Spec, "field") {
		name := StringValue(field["name"])
		shape.Fields[name] = OperationInputField{
			Name: name, WireName: WireName(field, name), Type: field["type"], Optional: IsOptionalType(field["type"]), HasDefault: field["default"] != nil,
			EnumValues: EnumWireValues(resources, operation.Module, field["type"]),
		}
	}
	return shape
}

func EnumWireValues(resources map[string]Resource, module string, value any) []string {
	expression := TypeExpression(value)
	for _, wrapper := range []string{"optional", "nullable", "list", "set"} {
		for strings.HasPrefix(expression, wrapper+"(") && strings.HasSuffix(expression, ")") {
			expression = strings.TrimSpace(expression[len(wrapper)+1 : len(expression)-1])
		}
	}
	if !strings.HasPrefix(expression, "enum.") {
		return nil
	}
	enum, ok := resources[graph.ResourceAddress(module, "enum", strings.TrimPrefix(expression, "enum."))]
	if !ok || enum.Spec["open"] == true {
		return nil
	}
	var values []string
	for _, value := range NamedChildren(enum.Spec, "value") {
		values = append(values, WireName(value, StringValue(value["name"])))
	}
	sort.Strings(values)
	return values
}

func ModuleInstancePath(resource Resource) string {
	if resource.Module == "app" || resource.Module == "" {
		return resource.Name
	}
	return resource.Module + "/" + resource.Name
}

func ResolveResourceRef(resource Resource, reference, kind string) string {
	if reference == "" || strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) != 2 {
		return reference
	}
	module := resource.Module
	switch kind {
	case "application", "workspace", "go_module", "go_toolchain", "go_target", "http_gateway", "authentication", "authorization", "workload_identity", "pipeline", "provider", "data_source", "execution_engine", "event_bus", "secret_store", "secret", "deployment", "mcp_connection", "mcp_server", "assistant", "typescript_client", "patch":
		module = "app"
	}
	return graph.ResourceAddress(module, parts[0], parts[1])
}

func RefString(value any) string {
	object, _ := value.(map[string]any)
	reference, _ := object["$ref"].(string)
	return reference
}

func StringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	scalar, _ := value.(map[string]any)
	switch scalar["$scalar"] {
	case "int":
		text, _ := scalar["value"].(string)
		return text
	case "decimal":
		coefficient, _ := scalar["coefficient"].(string)
		scale, err := strconv.Atoi(fmt.Sprint(scalar["scale"]))
		if err == nil {
			negative := strings.HasPrefix(coefficient, "-")
			digits := strings.TrimPrefix(coefficient, "-")
			for len(digits) <= scale {
				digits = "0" + digits
			}
			if scale > 0 {
				digits = digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
			}
			if negative {
				return "-" + digits
			}
			return digits
		}
	case "duration":
		nanoseconds, err := strconv.ParseInt(fmt.Sprint(scalar["nanoseconds"]), 10, 64)
		if err == nil {
			return time.Duration(nanoseconds).String()
		}
	case "size":
		return fmt.Sprint(scalar["bytes"])
	default:
		text, _ := scalar["value"].(string)
		return text
	}
	return ""
}

func TypeExpression(value any) string {
	if reference := RefString(value); reference != "" {
		return reference
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return strings.TrimSpace(raw)
		}
	}
	return fmt.Sprint(value)
}

func IsOptionalType(value any) bool {
	expression := strings.TrimSpace(TypeExpression(value))
	return strings.HasPrefix(expression, "optional(") && strings.HasSuffix(expression, ")")
}
