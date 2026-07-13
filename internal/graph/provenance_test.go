package graph

import "testing"

func TestFieldProvenanceTreeRebaseAndLookup(t *testing.T) {
	resource := Resource{Address: "house/record/item", Spec: map[string]any{
		"field": []any{map[string]any{"name": "id", "type": map[string]any{"$ref": "std.uuid"}}},
	}}
	field := FieldProvenance{Kind: "authored", SourceAddress: resource.Address}
	SetFieldProvenance(&resource.Origin, "/spec", resource.Spec, field)
	if got := NearestFieldProvenance(resource.Origin, "/spec/field/0/name"); got.SourceAddress != resource.Address {
		t.Fatalf("nearest provenance = %#v", got)
	}
	if _, exists := resource.Origin.FieldProvenance["/spec/field/0/type/$ref"]; exists {
		t.Fatal("reference internals must remain scalar provenance leaves")
	}
	RebaseFieldProvenance(&resource.Origin, "/spec/field", "/spec/member")
	if _, exists := resource.Origin.FieldProvenance["/spec/member/0/name"]; !exists {
		t.Fatalf("rebased provenance = %#v", resource.Origin.FieldProvenance)
	}
}

func TestExpansionProvenanceUsesGeneratorIdentity(t *testing.T) {
	generator := Resource{Address: "house/crud/items", Origin: Origin{Kind: "authored"}}
	resource := Resource{Address: "house/operation/create_item", Spec: map[string]any{"input": map[string]any{"$ref": "record.item"}}}
	MarkExpansionFieldProvenance(&resource, generator)
	field := resource.Origin.FieldProvenance["/spec/input"]
	if field.Kind != "expansion" || field.ProvidedBy != generator.Address {
		t.Fatalf("expansion provenance = %#v", field)
	}
}
