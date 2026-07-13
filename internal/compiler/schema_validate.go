package compiler

import (
	"fmt"

	"scenery.sh/internal/spec"
)

func validateAuthoredBlockSchemas(sources []*Source, packageScope bool) []Diagnostic {
	var diagnostics []Diagnostic
	for _, source := range sources {
		for _, block := range source.Blocks {
			schema, ok := authoredStructuralSchemas[block.Type]
			allowedStructural := block.Type == "module" || !packageScope && (block.Type == "workspace" || block.Type == "application") || packageScope && (block.Type == "package" || block.Type == "input" || block.Type == "export")
			if !ok || !allowedStructural {
				schema, ok = authoredResourceSourceSchema(block.Type)
			}
			if ok {
				diagnostics = append(diagnostics, validateAuthoredBlock(block, schema)...)
			}
		}
	}
	return diagnostics
}

func validateAuthoredBlock(block *Block, schema *authoredBlockSchema) []Diagnostic {
	var diagnostics []Diagnostic
	add := func(code, message string, target *Block) {
		rng := target.Range
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message + " (schema " + schema.Revision + ")", Range: &rng})
	}
	if len(block.Labels) != schema.Labels {
		add("SCN1016", fmt.Sprintf("%s requires exactly %d labels; found %d", block.Type, schema.Labels, len(block.Labels)), block)
	}
	for _, label := range block.Labels {
		if !validAuthoredLabel(schema, label) {
			add("SCN1013", fmt.Sprintf("%s label %q violates %s policy", block.Type, label, schema.LabelPolicy), block)
		}
	}
	for name := range block.Attributes {
		if _, expectsBlock := schema.Children[name]; expectsBlock {
			add("SCN1017", "field "+name+" must be authored as a block", block)
		} else if field, allowed := schema.Attributes[name]; allowed {
			if field.UnsupportedDraft != "" {
				add("SCN7009", "unsupported_draft: attribute "+name+" requires unresolved capability "+field.UnsupportedDraft, block)
			}
			if value, literal := literalString(block, name); literal && !authoredEnumAllows(field, value) {
				add("SCN1020", "attribute "+name+" in "+block.Type+" is outside its declared enum", block)
			}
		} else if !schema.AllowUnknownAttributes {
			add("SCN1017", "unknown attribute "+name+" in "+block.Type, block)
		} else if schema.DynamicAttribute != nil && !validSemanticName(name) {
			add("SCN1017", "dynamic attribute "+name+" in "+block.Type+" must be lower_snake_case", block)
		}
	}
	counts := map[string]int{}
	labels := map[string]map[string]bool{}
	for _, child := range block.Blocks {
		rule, ok := schema.Children[child.Type]
		if !ok {
			add("SCN1018", "unknown nested block "+child.Type+" in "+block.Type, child)
			continue
		}
		counts[child.Type]++
		if !rule.Repeatable && counts[child.Type] > 1 {
			add("SCN1014", "duplicate singleton block "+child.Type+" in "+block.Type, child)
		}
		if rule.Repeatable && rule.Schema.Labels > 0 && len(child.Labels) == rule.Schema.Labels {
			key := fmt.Sprint(child.Labels)
			if labels[child.Type] == nil {
				labels[child.Type] = map[string]bool{}
			}
			if labels[child.Type][key] {
				add("SCN1014", "duplicate "+child.Type+" labels in "+block.Type, child)
			}
			labels[child.Type][key] = true
		}
		diagnostics = append(diagnostics, validateAuthoredBlock(child, rule.Schema)...)
	}
	for name := range schema.Required {
		_, attribute := block.Attributes[name]
		if !attribute && counts[name] == 0 {
			add("SCN1015", "missing required field "+name+" in "+block.Type, block)
		}
	}
	for _, requirement := range authoredConditionalRequirements[schema.Revision] {
		value, literal := literalString(block, requirement.Field)
		if !literal || !semanticEqual(value, requirement.Equals) {
			continue
		}
		for _, name := range requirement.Required {
			_, attribute := block.Attributes[name]
			if !attribute && counts[name] == 0 {
				add("SCN1015", "missing required field "+name+" when "+requirement.Field+" is "+stringValue(requirement.Equals), block)
			}
		}
	}
	return diagnostics
}

func validateResourceSchemas(resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		for _, violation := range spec.ValidateResource(resource.Kind, resource.Spec) {
			diagnostics = append(diagnostics, Diagnostic{Code: violation.Code, Severity: "error", Message: violation.Message, Address: resource.Address})
		}
	}
	return diagnostics
}
