package compiler

import (
	"errors"
	"strings"
	"testing"
)

func TestGoBuildTargetSelectionNamesAvailableTargets(t *testing.T) {
	result := &Result{Manifest: &Manifest{Resources: []Resource{
		{Kind: "scenery.go-target", Name: "development", Address: "app/go_target/development", Spec: map[string]any{"role": "development"}},
	}}}
	for _, name := range []string{"", "missing"} {
		_, err := ResolveGoBuildTarget(result, name, "build")
		selection, ok := errors.AsType[*GoTargetSelectionError](err)
		if !ok || selection.Name != name || len(selection.Available) != 1 || !strings.Contains(err.Error(), "--target development") {
			t.Fatalf("selection %q: %v", name, err)
		}
	}
}
