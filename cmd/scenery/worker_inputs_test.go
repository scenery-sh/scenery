package main

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The Rust Host scans the same tree and must select the same files, or its
// source digest differs from the CLI's and it refuses the CLI's plan. Its
// `dev_inputs` tests run the cases of this fixture too.
func TestWorkerInputScopeMatchesTheHostFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "worker-input-scope.json"))
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name      string            `json:"name"`
		Generated []string          `json:"generated"`
		Files     map[string]string `json:"files"`
		Inputs    []string          `json:"inputs"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		t.Fatal(err)
	}
	if len(cases) == 0 {
		t.Fatal("fixture has no cases")
	}
	for _, test := range cases {
		root := t.TempDir()
		for name, content := range test.Files {
			path := filepath.Join(root, filepath.FromSlash(name))
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		scope := &workerInputScope{root: root, generated: test.Generated}
		inputs, err := scope.capture()
		if err != nil {
			t.Fatalf("%s: %v", test.Name, err)
		}
		if got := slices.Sorted(maps.Keys(inputs)); !slices.Equal(got, test.Inputs) {
			t.Errorf("%s: inputs = %q, want %q", test.Name, got, test.Inputs)
		}
	}
}
