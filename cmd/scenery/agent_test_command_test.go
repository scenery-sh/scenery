package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

func TestRunBatchedGoModuleTestsIsolatesFailures(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.test/batched\n\ngo 1.23\n",
		"alpha/alpha_test.go": `package alpha
import "testing"
func TestAlpha(t *testing.T) {}
`,
		"beta/beta_test.go": `package beta
import "testing"
func TestBeta(t *testing.T) { t.Fatal("beta boom") }
`,
		"gamma/gamma_test.go": `package gamma
import "testing"
func TestGamma(t *testing.T) {}
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := agentGraph{Result: &compiler.Result{Root: root}}
	modules := []agentModule{
		{Name: "alpha", Directory: "alpha", Runtime: "go"},
		{Name: "beta", Directory: "beta", Runtime: "go"},
		{Name: "gamma", Directory: "gamma", Runtime: "go"},
	}
	results := make([]moduleTestResult, len(modules))
	batched := make([]bool, len(modules))
	runBatchedGoModuleTests(context.Background(), g, modules, envpolicy.Environ(), results, batched)

	for i := range modules {
		if !batched[i] {
			t.Fatalf("module %s was not batched", modules[i].Name)
		}
	}
	if !results[0].OK {
		t.Fatalf("alpha should pass, got output: %s", results[0].Output)
	}
	if results[1].OK || !strings.Contains(results[1].Output, "beta boom") {
		t.Fatalf("beta should fail with 'beta boom', got ok=%v output=%q", results[1].OK, results[1].Output)
	}
	if !results[2].OK {
		t.Fatalf("gamma should pass, got output: %s", results[2].Output)
	}
}
