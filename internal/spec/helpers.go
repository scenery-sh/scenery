package spec

import (
	"sort"
	"strings"
)

func kindForBlock(blockType string) string {
	return "scenery." + strings.ReplaceAll(blockType, "_", "-")
}

func cloneSemanticValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = cloneSemanticValue(item)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = cloneSemanticValue(item)
		}
		return result
	default:
		return typed
	}
}

func cloneMapValue(value any) map[string]any {
	result := map[string]any{}
	if source, ok := value.(map[string]any); ok {
		for key, item := range source {
			result[key] = cloneSemanticValue(item)
		}
	}
	return result
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func oneOf[T comparable](value T, candidates ...T) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}
