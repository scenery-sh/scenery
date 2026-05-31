package main

import (
	"strings"
	"testing"
)

func TestRunHarnessArchitectureStepSuccess(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeArchitectureSupportFiles(t, root)
	writeTestAppFile(t, root, "internal/example/example.go", "package example\n\nimport \"fmt\"\n\nfunc Format(v string) string { return fmt.Sprintf(\"%s\", v) }\n")

	step := runHarnessArchitectureStep(root)
	if !step.OK {
		t.Fatalf("architecture step failed: %+v", step)
	}
	if got, _ := step.Summary["source_files"].(int); got == 0 {
		t.Fatalf("source_files = %v, want > 0", step.Summary["source_files"])
	}
}

func TestRunHarnessArchitectureStepReportsViolations(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeArchitectureSupportFiles(t, root)
	writeTestAppFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n\ngo 1.26.3\n\nrequire github.com/example/newdep v1.0.0\n")
	writeTestAppFile(t, root, "internal/bad/bad.go", "package bad\n\nimport _ \"github.com/spf13/cobra\"\n")

	step := runHarnessArchitectureStep(root)
	if step.OK {
		t.Fatalf("architecture step ok = true, want false")
	}
	var messages []string
	for _, diag := range step.Diagnostics {
		messages = append(messages, diag.Message)
	}
	joined := strings.Join(messages, "\n")
	if !strings.Contains(joined, "direct Go dependency is not in the architecture allowlist") {
		t.Fatalf("missing dependency diagnostic: %+v", step.Diagnostics)
	}
	if !strings.Contains(joined, "forbidden import github.com/spf13/cobra") {
		t.Fatalf("missing forbidden import diagnostic: %+v", step.Diagnostics)
	}
}

func TestRunHarnessArchitectureStepReportsLayerViolation(t *testing.T) {
	t.Parallel()

	root := writeHarnessSelfRepo(t, `{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object"}`)
	writeArchitectureSupportFiles(t, root)
	writeTestAppFile(t, root, "runtime/bad.go", "package runtime\n\nimport _ \"github.com/pbrazdil/onlava/internal/devdash\"\n")

	step := runHarnessArchitectureStep(root)
	if step.OK {
		t.Fatalf("architecture step ok = true, want false")
	}
	var messages []string
	for _, diag := range step.Diagnostics {
		messages = append(messages, diag.Message)
	}
	if !strings.Contains(strings.Join(messages, "\n"), "package layer violation") {
		t.Fatalf("missing layer diagnostic: %+v", step.Diagnostics)
	}
}

func writeArchitectureSupportFiles(t *testing.T, root string) {
	t.Helper()
	writeTestAppFile(t, root, ".gitignore", "/oracle/\n/coverage/\n.onlava/\n.DS_Store\nnode_modules/\n")
	writeTestAppFile(t, root, ".gitattributes", "cmd/onlava/devdash_static/** -diff linguist-generated=true linguist-vendored=true\nui/public/assets/** -diff linguist-generated=true linguist-vendored=true\nui/dist/** -diff linguist-generated=true\n")
}
