package compiler

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestResourceViewClonePreservesJSONShapeAndOwnership(t *testing.T) {
	span := &Range{SourceID: "source", Start: Position{Line: 2}, End: Position{Line: 3}}
	resources := []Resource{{Address: "app/service/test", Spec: map[string]any{
		"nested": map[string]any{"items": []any{map[string]any{"value": "original"}}},
		"int":    3, "number": json.Number("1.25"), "strings": []string{"a"},
		"nil_map": map[string]any(nil), "empty_map": map[string]any{},
		"nil_slice": []any(nil), "empty_slice": []any{},
	}, Origin: Origin{
		Patches: []string{"patch"}, ModuleChain: []string{"module"}, DeclarationRange: span,
		AttributeRanges:  map[string]Range{"field": *span},
		ExpansionLineage: []ExpansionStep{{SourceRange: span}},
		FieldProvenance:  map[string]FieldProvenance{"field": {DeclaredAt: span, Transformations: []string{"default"}}},
	}}, {Spec: map[string]any{}, Origin: Origin{Patches: []string{}, AttributeRanges: map[string]Range{}, FieldProvenance: map[string]FieldProvenance{}}}}
	data, err := json.Marshal(resources)
	if err != nil {
		t.Fatal(err)
	}
	var expected []Resource
	if err := json.Unmarshal(data, &expected); err != nil {
		t.Fatal(err)
	}
	cloned := cloneResourceView(resources)
	if !reflect.DeepEqual(cloned, expected) {
		t.Fatal("direct copy differs from graph JSON normalization")
	}
	cloned[0].Spec["nested"].(map[string]any)["items"].([]any)[0].(map[string]any)["value"] = "changed"
	cloned[0].Origin.Patches[0] = "changed"
	cloned[0].Origin.ModuleChain[0] = "changed"
	cloned[0].Origin.DeclarationRange.SourceID = "changed"
	cloned[0].Origin.AttributeRanges["field"] = Range{}
	cloned[0].Origin.ExpansionLineage[0].SourceRange.SourceID = "changed"
	field := cloned[0].Origin.FieldProvenance["field"]
	field.DeclaredAt.SourceID = "changed"
	field.Transformations[0] = "changed"
	cloned[0].Origin.FieldProvenance["new"] = FieldProvenance{}
	after, err := json.Marshal(resources)
	if err != nil || string(after) != string(data) {
		t.Fatalf("copy shares mutable graph state: %v", err)
	}
	if cloneResourceView(nil) != nil {
		t.Fatal("nil view became non-nil")
	}
}
