package contractagent

import (
	"reflect"
	"slices"
	"testing"
)

func TestSelectedResourcesPreservesOrderDuplicatesAndEmptyResults(t *testing.T) {
	first := Resource{Address: "app/record/a", Name: "first"}
	last := Resource{Address: "app/record/a", Name: "last"}
	b := Resource{Address: "app/record/b", Name: "b"}
	manifest := &Manifest{Resources: []Resource{first, b, last}}
	for _, test := range []struct {
		name      string
		manifest  *Manifest
		addresses []string
		want      []Resource
		failure   string
	}{
		{name: "nil", want: []Resource{}},
		{name: "empty", manifest: manifest, want: []Resource{}},
		{name: "single last duplicate", manifest: manifest, addresses: []string{first.Address}, want: []Resource{last}},
		{name: "ordered repeated requests", manifest: manifest, addresses: []string{b.Address, first.Address, first.Address}, want: []Resource{last, last, b}},
		{name: "all", manifest: manifest, addresses: []string{b.Address, first.Address}, want: []Resource{last, b}},
		{name: "nil missing", addresses: []string{"missing"}, failure: `resource "missing" not found`},
		{name: "single missing", manifest: manifest, addresses: []string{"missing"}, failure: `resource "missing" not found`},
		{name: "sorted missing", manifest: manifest, addresses: []string{"z", b.Address, "c"}, failure: `resource "c" not found`},
		{name: "empty address", manifest: manifest, addresses: []string{""}, failure: `resource "" not found`},
	} {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.addresses)
			got, err := selectedResources(test.manifest, test.addresses)
			if test.failure != "" {
				if err == nil || err.Error() != test.failure || got != nil {
					t.Fatalf("selection = %#v, %v; want nil, %s", got, err, test.failure)
				}
			} else if err != nil || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("selection = %#v, %v; want %#v", got, err, test.want)
			}
			if !reflect.DeepEqual(test.addresses, before) {
				t.Fatal("selection reordered caller addresses")
			}
		})
	}
	selected, err := selectedResources(manifest, []string{first.Address})
	if err != nil {
		t.Fatal(err)
	}
	selected[0].Name = "changed"
	if manifest.Resources[2].Name != "last" {
		t.Fatal("selected resource storage aliases the input slice")
	}
}
