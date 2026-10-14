package clientprojection

import (
	"sort"
)

func NamedChildren(spec map[string]any, name string) []map[string]any {
	children := OrderedChildren(spec, name)
	sort.Slice(children, func(i, j int) bool {
		a, _ := children[i]["name"].(string)
		b, _ := children[j]["name"].(string)
		return a < b
	})
	return children
}

func OrderedChildren(spec map[string]any, name string) []map[string]any {
	value, ok := spec[name]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if child, ok := item.(map[string]any); ok {
				out = append(out, child)
			}
		}
		return out
	}
	return nil
}
