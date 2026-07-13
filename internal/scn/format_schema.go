package scn

import (
	"regexp"

	"scenery.sh/internal/spec"
)

func formatterSchemaForBlock(parent, blockType string) (*spec.SourceBlockSchema, bool) {
	structural := spec.StructuralSourceSchemas()
	if parent == "" {
		if schema, ok := structural[blockType]; ok {
			return schema, true
		}
		return spec.ResourceSourceSchema(blockType)
	}
	find := func(rootType string, root *spec.SourceBlockSchema) (*spec.SourceBlockSchema, bool) {
		var visit func(string, *spec.SourceBlockSchema) (*spec.SourceBlockSchema, bool)
		visit = func(currentType string, current *spec.SourceBlockSchema) (*spec.SourceBlockSchema, bool) {
			if currentType == parent {
				if child, ok := current.Children[blockType]; ok {
					return child.Schema, true
				}
			}
			for childType, child := range current.Children {
				if found, ok := visit(childType, child.Schema); ok {
					return found, true
				}
			}
			return nil, false
		}
		return visit(rootType, root)
	}
	for rootType := range spec.ResourceSourceChildren() {
		root, ok := spec.ResourceSourceSchema(rootType)
		if ok {
			if schema, found := find(rootType, root); found {
				return schema, true
			}
		}
	}
	for rootType, root := range structural {
		if schema, ok := find(rootType, root); ok {
			return schema, true
		}
	}
	return nil, false
}

func validFormatterLabel(schema *spec.SourceBlockSchema, label string) bool {
	if schema == nil || schema.Labels == 0 || label == "" {
		return schema != nil && schema.Labels == 0 && label == ""
	}
	if schema.LabelPattern == "" {
		return true
	}
	matched, err := regexp.MatchString(schema.LabelPattern, label)
	return err == nil && matched
}
