package graph

import "testing"

func TestContractRevisionIsOrderIndependentAndSpecBound(t *testing.T) {
	resources := []Resource{
		{Address: "house/record/item", Kind: "scenery.record", Name: "item", Module: "house", Spec: map[string]any{"unknown_fields": "reject"}},
		{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"method": "House"}}},
	}
	first, err := ContractRevision(resources, "house")
	if err != nil {
		t.Fatal(err)
	}
	resources[0], resources[1] = resources[1], resources[0]
	second, err := ContractRevision(resources, "house")
	if err != nil {
		t.Fatal(err)
	}
	if first != second || !IsCanonicalSHA256Digest(first) {
		t.Fatalf("contract revisions = %q %q", first, second)
	}
}

func TestCanonicalResourcesSortsWithoutMutatingInput(t *testing.T) {
	resources := []Resource{{Address: "b"}, {Address: "a"}}
	first, err := CanonicalResources(resources)
	if err != nil {
		t.Fatal(err)
	}
	second, err := CanonicalResources([]Resource{{Address: "a"}, {Address: "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) || resources[0].Address != "b" {
		t.Fatalf("canonical resources changed input or order: %s", first)
	}
}
