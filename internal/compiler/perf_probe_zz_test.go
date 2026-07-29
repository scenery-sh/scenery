package compiler

import (
	"os"
	"testing"
	"time"

	graphmodel "scenery.sh/internal/graph"
)

// Throwaway probe: measures graph.Graph / ContextAt cost on a real manifest.
func TestPerfProbeContextAt(t *testing.T) {
	root := os.Getenv("PERF_PROBE_ROOT")
	if root == "" {
		t.Skip("PERF_PROBE_ROOT not set")
	}
	result, err := Compile(root)
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	manifest, err := result.ManifestForView("effective")
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	t.Logf("resources: %d", len(manifest.Resources))

	start := time.Now()
	edges := graphmodel.ResourceEdges(manifest.Resources)
	t.Logf("ResourceEdges once: %v (edges: %d)", time.Since(start), len(edges))

	// warm run again to reduce first-run noise
	start = time.Now()
	graphmodel.ResourceEdges(manifest.Resources)
	t.Logf("ResourceEdges warm: %v", time.Since(start))

	focus := manifest.Resources[0].Address
	start = time.Now()
	_, err = graphmodel.Graph(manifest, focus, graphmodel.GraphOptions{Direction: "both", Depth: 3, MaxResources: 1000})
	if err != nil {
		t.Fatalf("graph: %v", err)
	}
	t.Logf("Graph depth=3 once: %v", time.Since(start))

	// Multi-focus ContextAt: pick up to 10 spread-out focus addresses.
	var focuses []string
	step := len(manifest.Resources) / 10
	if step == 0 {
		step = 1
	}
	for i := 0; i < len(manifest.Resources) && len(focuses) < 10; i += step {
		focuses = append(focuses, manifest.Resources[i].Address)
	}
	opts := graphmodel.ContextOptions{Focus: focuses, Include: []string{"dependencies", "dependents"}, Depth: 3, MaxResources: 1000, MaxBytes: 2_000_000}
	start = time.Now()
	bundle, err := graphmodel.Context(manifest, opts)
	if err != nil {
		t.Fatalf("context: %v", err)
	}
	t.Logf("ContextAt %d focuses depth=3: %v (resources returned: %d, truncated: %v)", len(focuses), time.Since(start), len(bundle.Resources), bundle.Truncated)

	// Single-focus depth=1 baseline (default depth path)
	opts1 := graphmodel.ContextOptions{Focus: focuses[:1], MaxResources: 100, MaxBytes: 200000}
	start = time.Now()
	if _, err = graphmodel.Context(manifest, opts1); err != nil {
		t.Fatalf("context1: %v", err)
	}
	t.Logf("ContextAt 1 focus depth=0(default): %v", time.Since(start))
}
