package compiler

import (
	"strconv"
	"strings"
)

func resourcePointerValue(resource Resource, pointer string) (any, bool) {
	parts := pointerParts(pointer)
	if len(parts) == 0 || parts[0] != "spec" {
		return nil, false
	}
	var current any = resource.Spec
	for _, part := range parts[1:] {
		next, ok := semanticPointerStep(current, part)
		if !ok {
			return nil, false
		}
		current = next
	}
	return current, true
}

func semanticPointerStep(current any, part string) (any, bool) {
	switch values := current.(type) {
	case map[string]any:
		if next, ok := values[part]; ok {
			return next, true
		}
		if stringValue(values["name"]) == part {
			return values, true
		}
	case []any:
		if index, err := strconv.Atoi(part); err == nil && index >= 0 && index < len(values) {
			return values[index], true
		}
		for _, raw := range values {
			child, _ := raw.(map[string]any)
			if stringValue(child["name"]) == part {
				return child, true
			}
		}
	}
	return nil, false
}

func pointerParts(pointer string) []string {
	if !strings.HasPrefix(pointer, "/") {
		return nil
	}
	raw := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	parts := make([]string, len(raw))
	for index, part := range raw {
		parts[index] = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
	}
	return parts
}
