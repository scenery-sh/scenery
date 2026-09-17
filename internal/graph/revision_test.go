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

// A service's contract revision follows the contract of its module instance and
// of the resources its adapter covers, and no other contract change.
func TestServiceContractRevisionFollowsOnlyTheServiceContract(t *testing.T) {
	manifest := func(house, garden, page string) *Manifest {
		return &Manifest{Application: ApplicationIdentity{Name: "estate"}, Resources: []Resource{
			{Address: "app/module/house", Kind: "scenery.module", Name: "house", Module: "app", Spec: map[string]any{"exports": house}},
			{Address: "house/record/item", Kind: "scenery.record", Name: "item", Module: "house", Spec: map[string]any{"unknown_fields": house}},
			{Address: "house/service/house", Kind: "scenery.service", Name: "house", Module: "house", Spec: map[string]any{"runtime": "go"}},
			{Address: "garden/record/plant", Kind: "scenery.record", Name: "plant", Module: "garden", Spec: map[string]any{"unknown_fields": garden}},
			{Address: "garden/service/garden", Kind: "scenery.service", Name: "garden", Module: "garden", Spec: map[string]any{"runtime": "go"}},
			{Address: "app/record/shared", Kind: "scenery.record", Name: "shared", Module: "app", Spec: map[string]any{"unknown_fields": page}},
		}}
	}
	house := func(value *Manifest) string {
		return ServiceContractRevision(value, value.Resources[2], []string{"house/service/house", "app/record/shared"})
	}
	garden := func(value *Manifest) string {
		return ServiceContractRevision(value, value.Resources[4], []string{"garden/service/garden"})
	}
	base := manifest("reject", "reject", "reject")
	if !IsCanonicalSHA256Digest(house(base)) || house(base) == garden(base) {
		t.Fatalf("service contract revisions = %q %q", house(base), garden(base))
	}
	gardenEdit := manifest("reject", "ignore", "reject")
	if house(gardenEdit) != house(base) || garden(gardenEdit) == garden(base) {
		t.Fatal("a garden contract edit did not change exactly the garden revision")
	}
	if houseEdit := manifest("ignore", "reject", "reject"); house(houseEdit) == house(base) || garden(houseEdit) != garden(base) {
		t.Fatal("a house contract edit did not change exactly the house revision")
	}
	moduleEdit := manifest("reject", "reject", "reject")
	moduleEdit.Resources[0].Spec["exports"] = "changed"
	if house(moduleEdit) == house(base) || garden(moduleEdit) != garden(base) {
		t.Fatal("an edit of the service's module instance did not change exactly the house revision")
	}
	if coveredEdit := manifest("reject", "reject", "ignore"); house(coveredEdit) == house(base) || garden(coveredEdit) != garden(base) {
		t.Fatal("an edit of a covered resource outside the module did not change exactly the covering service's revision")
	}
	reordered := manifest("reject", "reject", "reject")
	reordered.Resources[1], reordered.Resources[5] = reordered.Resources[5], reordered.Resources[1]
	if ServiceContractRevision(reordered, reordered.Resources[2], []string{"house/service/house", "app/record/shared"}) != house(base) {
		t.Fatal("service contract revision depends on resource order")
	}
}
