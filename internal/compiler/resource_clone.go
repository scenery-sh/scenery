package compiler

import (
	"encoding/json"
	"maps"
	"slices"

	"scenery.sh/internal/scn"
)

// Graph views own every mutable field. Copy their structured values directly
// instead of serializing provenance and reparsing it for each compiler phase.
func cloneResourceView(resources []Resource) []Resource {
	cloned := slices.Clone(resources)
	for i := range cloned {
		cloned[i].Spec, _ = cloneResourceValue(resources[i].Spec).(map[string]any)
		origin := resources[i].Origin
		origin.Patches = cloneNonemptyStrings(origin.Patches)
		origin.ModuleChain = cloneNonemptyStrings(origin.ModuleChain)
		origin.DeclarationRange = cloneResourceRange(origin.DeclarationRange)
		origin.AttributeRanges = nil
		if len(resources[i].Origin.AttributeRanges) > 0 {
			origin.AttributeRanges = maps.Clone(resources[i].Origin.AttributeRanges)
		}
		origin.ExpansionLineage = nil
		if len(resources[i].Origin.ExpansionLineage) > 0 {
			origin.ExpansionLineage = slices.Clone(resources[i].Origin.ExpansionLineage)
			for j := range origin.ExpansionLineage {
				origin.ExpansionLineage[j].SourceRange = cloneResourceRange(origin.ExpansionLineage[j].SourceRange)
			}
		}
		origin.FieldProvenance = nil
		if len(resources[i].Origin.FieldProvenance) > 0 {
			origin.FieldProvenance = maps.Clone(resources[i].Origin.FieldProvenance)
			for key, field := range origin.FieldProvenance {
				field.DeclaredAt = cloneResourceRange(field.DeclaredAt)
				field.Transformations = cloneNonemptyStrings(field.Transformations)
				origin.FieldProvenance[key] = field
			}
		}
		cloned[i].Origin = origin
	}
	return cloned
}

func cloneResourceRange(value *scn.Range) *scn.Range {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneNonemptyStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return slices.Clone(values)
}

func cloneResourceValue(value any) any {
	switch value := value.(type) {
	case nil, string, bool, float64:
		return value
	case map[string]any:
		if value == nil {
			return nil
		}
		cloned := make(map[string]any, len(value))
		for key, item := range value {
			cloned[key] = cloneResourceValue(item)
		}
		return cloned
	case []any:
		if value == nil {
			return nil
		}
		cloned := make([]any, len(value))
		for i, item := range value {
			cloned[i] = cloneResourceValue(item)
		}
		return cloned
	default:
		// Synthetic inputs can contain typed slices or numbers. Preserve the
		// graph view's existing JSON normalization for those uncommon values.
		data, _ := json.Marshal(value)
		var cloned any
		_ = json.Unmarshal(data, &cloned)
		return cloned
	}
}
