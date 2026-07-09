package main

import (
	"strings"
	"testing"
)

func TestRunHarnessArchitectureStepValidAndInvalidFixtures(t *testing.T) {
	t.Parallel()

	t.Run("valid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeArchitectureSupportFiles(t, root)
		writeTestAppFile(t, root, "internal/example/example.go", "package example\n\nimport \"fmt\"\n\nfunc Format(v string) string { return fmt.Sprintf(\"%s\", v) }\n")
		writeTestAppFile(t, root, "apps/consolenext/node_modules/pkg/README.md", "model context "+"protocol\n")

		step := runHarnessArchitectureStep(root)
		if !step.OK {
			t.Fatalf("architecture step failed: %+v", step)
		}
		if got, _ := step.Summary["source_files"].(int); got == 0 {
			t.Fatalf("source_files = %v, want > 0", step.Summary["source_files"])
		}
	})

	t.Run("invalid", func(t *testing.T) {
		t.Parallel()

		root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
		writeArchitectureSupportFiles(t, root)
		writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.26.3\n\nrequire github.com/example/newdep v1.0.0\n")
		writeTestAppFile(t, root, "internal/bad/bad.go", "package bad\n\nimport _ \"github.com/spf13/cobra\"\n")
		writeTestAppFile(t, root, "internal/app/bad.go", "package app\n\nimport _ \"scenery.sh/internal/postgresdb\"\n")
		writeTestAppFile(t, root, "internal/model/bad.go", "package model\n\nimport _ \"golang.org/x/tools/go/packages\"\n")
		writeTestAppFile(t, root, "runtime/bad.go", "package runtime\n\nimport _ \"scenery.sh/internal/devdash\"\n")

		step := runHarnessArchitectureStep(root)
		if step.OK {
			t.Fatalf("architecture step ok = true, want false")
		}
		joined := diagnosticMessages(step.Diagnostics)
		for _, want := range []string{
			"direct Go dependency is not in the architecture allowlist",
			"forbidden import github.com/spf13/cobra",
			"internal/app imports the PostgreSQL driver layer",
			"internal/model imports the parser package loader",
		} {
			if !strings.Contains(joined, want) {
				t.Fatalf("missing %q diagnostic: %+v", want, step.Diagnostics)
			}
		}
	})
}

func writeArchitectureSupportFiles(t *testing.T, root string) {
	t.Helper()
	writeTestAppFile(t, root, ".gitignore", "/oracle/\n/coverage/\n.scenery/\n.DS_Store\nnode_modules/\n")
	writeTestAppFile(t, root, ".gitattributes", "cmd/scenery/devdash_static/** -diff linguist-generated=true linguist-vendored=true\ncmd/scenery/dashboard_static/dist/** -diff linguist-generated=true\n")
}
