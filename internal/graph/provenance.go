package graph

import (
	"fmt"
	"sort"
	"strings"
)

// SetFieldProvenanceTree applies provenance to every non-scalar descendant.
func SetFieldProvenanceTree(origin *Origin, value any, path string, field FieldProvenance) {
	if origin == nil {
		return
	}
	if origin.FieldProvenance == nil {
		origin.FieldProvenance = map[string]FieldProvenance{}
	}
	var walk func(any, string)
	walk = func(current any, currentPath string) {
		switch typed := current.(type) {
		case map[string]any:
			if typed["$ref"] != nil || typed["$scalar"] != nil || typed["$expression"] != nil {
				return
			}
			keys := make([]string, 0, len(typed))
			for key := range typed {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				childPath := ProvenanceChildPath(currentPath, key)
				origin.FieldProvenance[childPath] = field
				walk(typed[key], childPath)
			}
		case []any:
			for index, item := range typed {
				childPath := ProvenanceChildPath(currentPath, fmt.Sprintf("%d", index))
				origin.FieldProvenance[childPath] = field
				walk(item, childPath)
			}
		}
	}
	walk(value, path)
}

func SetFieldProvenance(origin *Origin, path string, value any, field FieldProvenance) {
	if origin == nil {
		return
	}
	if origin.FieldProvenance == nil {
		origin.FieldProvenance = map[string]FieldProvenance{}
	}
	origin.FieldProvenance[path] = field
	SetFieldProvenanceTree(origin, value, path, field)
}

func MarkExpansionFieldProvenance(resource *Resource, generator Resource) {
	if resource == nil {
		return
	}
	field := FieldProvenance{
		Kind: "expansion", DeclaredAt: generator.Origin.DeclarationRange, ProvidedBy: generator.Address,
		SourceAddress: generator.Address, Transformations: []string{"declarative_expansion"},
	}
	SetFieldProvenanceTree(&resource.Origin, resource.Spec, "/spec", field)
}

func ProvenanceChildPath(parent, name string) string {
	return parent + "/" + escapeJSONPointer(name)
}

func RebaseFieldProvenance(origin *Origin, from, to string) {
	if origin == nil || from == to || len(origin.FieldProvenance) == 0 {
		return
	}
	updates := map[string]FieldProvenance{}
	for path, field := range origin.FieldProvenance {
		if path == from || strings.HasPrefix(path, from+"/") {
			updates[to+strings.TrimPrefix(path, from)] = field
			delete(origin.FieldProvenance, path)
		}
	}
	for path, field := range updates {
		origin.FieldProvenance[path] = field
	}
}

func EnsureFieldProvenance(resource *Resource, path, stage string) {
	if _, ok := resource.Origin.FieldProvenance[path]; ok {
		return
	}
	field := NearestFieldProvenance(resource.Origin, path)
	if field.Kind == "" {
		field = FieldProvenance{Kind: "derived", ProvidedBy: resource.Address, SourceAddress: resource.Address, Transformations: []string{"compiler_" + stage}}
	}
	if resource.Origin.FieldProvenance == nil {
		resource.Origin.FieldProvenance = map[string]FieldProvenance{}
	}
	resource.Origin.FieldProvenance[path] = field
}

func NearestFieldProvenance(origin Origin, path string) FieldProvenance {
	for candidate := path; candidate != ""; {
		if field, ok := origin.FieldProvenance[candidate]; ok {
			return field
		}
		index := strings.LastIndex(candidate, "/")
		if index <= 0 {
			break
		}
		candidate = candidate[:index]
	}
	return FieldProvenance{}
}

func AppendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
