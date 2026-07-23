package scn

import (
	"regexp"

	"scenery.sh/internal/spec"
)

type formatterSchemaKey struct {
	parent    string
	blockType string
}

var formatterSchemas = buildFormatterSchemaIndex()

func buildFormatterSchemaIndex() map[formatterSchemaKey]*spec.SourceBlockSchema {
	index := map[formatterSchemaKey]*spec.SourceBlockSchema{}
	structural := spec.StructuralSourceSchemas()
	resources := spec.ResourceSourceSchemas()
	var visit func(string, *spec.SourceBlockSchema)
	visit = func(parent string, schema *spec.SourceBlockSchema) {
		for blockType, child := range schema.Children {
			key := formatterSchemaKey{parent: parent, blockType: blockType}
			if _, exists := index[key]; !exists {
				index[key] = child.Schema
			}
			visit(blockType, child.Schema)
		}
	}
	for blockType, schema := range resources {
		index[formatterSchemaKey{blockType: blockType}] = schema
		visit(blockType, schema)
	}
	for blockType, schema := range structural {
		index[formatterSchemaKey{blockType: blockType}] = schema
		visit(blockType, schema)
	}
	return index
}

func formatterSchemaForBlock(parent, blockType string) (*spec.SourceBlockSchema, bool) {
	schema, ok := formatterSchemas[formatterSchemaKey{parent: parent, blockType: blockType}]
	return schema, ok
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
