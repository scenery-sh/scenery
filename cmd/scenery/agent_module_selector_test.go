package main

import (
	"testing"

	"scenery.sh/internal/compiler"

	"scenery.sh/internal/graph"
)

func TestAgentModuleSelectors(t *testing.T) {
	designs := agentModule{Name: "designs", Directory: "solar/designs"}
	modules := []agentModule{{Name: "health", Directory: "health"}, designs}
	g := agentGraph{Result: &compiler.Result{Manifest: &graph.Manifest{}}}
	for _, target := range []string{"designs", "solar/designs"} {
		selected, err := g.contextModule(target, modules)
		if err != nil || selected.Name != designs.Name {
			t.Fatalf("context %q selected %q: %v", target, selected.Name, err)
		}
	}
	selected, err := selectTestModules(modules, []string{"designs", "solar/designs"})
	if err != nil || len(selected) != 1 || selected[0].Name != designs.Name {
		t.Fatalf("test selected %v: %v", selected, err)
	}
}
