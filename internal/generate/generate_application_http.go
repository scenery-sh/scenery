package generate

import (
	"fmt"
	"sort"
	"strings"
)

func renderServiceOperationAdapters(b *strings.Builder, operations, bindings []Resource) error {
	for _, operation := range operations {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		if method == "" {
			return fmt.Errorf("operation %s has no handler method", operation.Address)
		}
		operationName := goName(operation.Name)
		if operationUsesHTTPStream(operation, bindings) {
			fmt.Fprintf(b, "func (adapter *serviceAdapter) %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, scenery.ByteStream, error) {\n", method, operationName, operationName)
			b.WriteString("\tif adapter == nil || adapter.native == nil { return nil, scenery.ByteStream{}, fmt.Errorf(\"service is not initialized\") }\n\tnative := adapter.native\n")
			fmt.Fprintf(b, "\treturn native.%s(ctx, input)\n}\n\n", method)
		} else {
			fmt.Fprintf(b, "func (adapter *serviceAdapter) %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, error) {\n", method, operationName, operationName)
			b.WriteString("\tif adapter == nil || adapter.native == nil { return nil, fmt.Errorf(\"service is not initialized\") }\n\tnative := adapter.native\n")
			fmt.Fprintf(b, "\treturn native.%s(ctx, input)\n}\n\n", method)
		}
	}
	return nil
}

func operationUsesHTTPStream(operation Resource, bindings []Resource) bool {
	for _, binding := range bindings {
		if stringValue(binding.Spec["protocol"]) == "http" && stringValue(binding.Spec["delivery"]) == "stream" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == operation.Address {
			return true
		}
	}
	return false
}

func defaultHTTPMediaType(codec string) string {
	switch codec {
	case "problem_json":
		return "application/problem+json"
	case "text":
		return "text/plain"
	case "bytes":
		return "application/octet-stream"
	case "form":
		return "application/x-www-form-urlencoded"
	case "multipart":
		return "multipart/form-data"
	default:
		return "application/json"
	}
}

func literalStringListFromValue(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func goStringStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "map[string]string{}"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, fmt.Sprintf("%q: %q", key, values[key]))
	}
	return "map[string]string{" + strings.Join(entries, ", ") + "}"
}
