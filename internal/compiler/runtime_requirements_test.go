package compiler

import (
	"testing"

	"scenery.sh/internal/scn"
)

func TestWorkerHostSelection(t *testing.T) {
	for _, tc := range []struct {
		name, runtime string
		workspace     map[string]scn.Expression
		want          bool
	}{
		{name: "Go by default", runtime: "go"},
		{name: "Go with explicit selection", runtime: "go", workspace: map[string]scn.Expression{"worker_host": {Value: true}}, want: true},
		{name: "Rust required by application", runtime: "rust", want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := &Result{
				Manifest: &Manifest{Resources: []Resource{{Kind: "scenery.service", Spec: map[string]any{"runtime": tc.runtime}}}},
				Sources:  []*Source{{Relative: scn.AppFilename, Blocks: []*Block{{Type: "workspace", Attributes: tc.workspace}}}},
			}
			if got := result.UsesWorkerBundle(); got != tc.want {
				t.Fatalf("UsesWorkerBundle() = %t, want %t", got, tc.want)
			}
		})
	}
}
