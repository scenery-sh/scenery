package rustbuild

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
)

func TestDevelopmentInputsSelectTransitiveCargoPackages(t *testing.T) {
	root := t.TempDir()
	manifest := func(dir string) string { return filepath.Join(root, dir, "Cargo.toml") }
	var graph cargoGraph
	err := json.Unmarshal(fmt.Appendf(nil, `{
		"packages": [
			{"id":"first","manifest_path":%q}, {"id":"second","manifest_path":%q},
			{"id":"helper","manifest_path":%q}, {"id":"leaf","manifest_path":%q}
		],
		"resolve":{"nodes":[
			{"id":"first","deps":[{"pkg":"helper","dep_kinds":[{"kind":"build"}]}]},
			{"id":"helper","deps":[{"pkg":"leaf","dep_kinds":[{"kind":null}]}]},
			{"id":"second","deps":[{"pkg":"leaf","dep_kinds":[{"kind":"dev"}]}]}
		]}
	}`, manifest("first"), manifest("first/second"), manifest("helper"), manifest("leaf")), &graph)
	if err != nil {
		t.Fatal(err)
	}
	inputs := &DevelopmentInputs{packages: make(map[Service][]string), owners: make(map[Service][]string)}
	first := Service{Workspace: ".", ModuleDirectory: "first"}
	second := Service{Workspace: ".", ModuleDirectory: filepath.Join("first", "second")}
	for _, service := range []Service{first, second} {
		if err := inputs.addService(root, service, graph); err != nil {
			t.Fatal(err)
		}
	}
	for _, trial := range []struct {
		path          string
		first, second bool
	}{
		{"first/src/lib.rs", true, false},
		{"first/second/src/lib.rs", false, true},
		{"helper/build.rs", true, false},
		{"leaf/src/value.txt", true, false},
		{"Cargo.lock", true, true},
		{".cargo/config.toml", true, true},
	} {
		path := filepath.FromSlash(trial.path)
		if a, b := inputs.Consumes(first, path), inputs.Consumes(second, path); a != trial.first || b != trial.second {
			t.Errorf("%s ownership = %t, %t, want %t, %t", trial.path, a, b, trial.first, trial.second)
		}
	}
}
