package main

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestOnboardingArgumentErrorsAreActionableJSON(t *testing.T) {
	for _, test := range []struct {
		name string
		run  func() error
		want string
	}{
		{"materialize", func() error { return runContractGenerate(io.Discard, []string{"--target", "contracts", "-o", "json"}) }, "requires --materialize"},
		{"dry-run", func() error { return runContractGenerate(io.Discard, []string{"--dry-run", "-o", "json"}) }, "dry-run"},
		{"unknown-target", func() error { return runContractGenerate(io.Discard, []string{"--target", "imaginary"}) }, "unknown generation target"},
		{"check-merge", func() error { return runContractGenerate(io.Discard, []string{"--check", "--merge-editor-workspace"}) }, "cannot be combined"},
		{"qualified-schema", func() error { return runContractSchema(io.Discard, []string{"execution", "-o", "json"}) }, "scenery schema scenery.execution"},
		{"provider-usage", func() error { return runProviderLock(io.Discard, []string{"install"}) }, "provider lock"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := test.run()
			if err == nil || cliExitCode(err) != 2 {
				t.Fatalf("error = %v, exit = %d", err, cliExitCode(err))
			}
			var output strings.Builder
			_ = renderMachineError(&output, []string{"-o", "json"}, err)
			envelope, decodeErr := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if len(envelope.Diagnostics) != 1 {
				t.Fatalf("diagnostics = %+v", envelope.Diagnostics)
			}
			diagnostic := envelope.Diagnostics[0]
			if diagnostic.Code != "SCN8001" || diagnostic.ReportToken != "" || !strings.Contains(diagnostic.Message, test.want) {
				t.Fatalf("diagnostic = %+v; want %q", diagnostic, test.want)
			}
		})
	}
}

func TestProviderLockCheckJSONAndReportSchemas(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "app.scn"), []byte("provider \"db\" { source = \"registry.scenery.dev/core/postgres\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	err := runProviderLock(&output, []string{"lock", "--check", "--app-root", root, "-o", "json"})
	if cliExitCode(err) != 1 {
		t.Fatalf("exit = %d, err = %v", cliExitCode(err), err)
	}
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(output.String()), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || envelope.Data["changed"] != true || envelope.Data["checked"] != true {
		t.Fatalf("envelope = %+v", envelope)
	}
	if _, err := os.Stat(filepath.Join(root, "app.lock.scn")); !os.IsNotExist(err) {
		t.Fatalf("check wrote lock: %v", err)
	}
	repo := repoRootForTest(t)
	for name, payload := range map[string]any{
		"scenery.provider.lock.result.schema.json": envelope.Data,
		"scenery.generate.result.schema.json":      map[string]any{"target": "", "generation": generate.GenerateResult{}, "clients": []generate.ClientCoverage{{Target: "app/typescript_client/public_api", Message: "empty"}}, "editor_workspace": generate.EditorWorkspaceReport{Status: "skipped", Reason: "scenery_repository_fixture"}},
	} {
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs", "schemas", name), payload); len(diagnostics) != 0 {
			t.Fatalf("%s: %v", name, diagnostics)
		}
	}
}

func TestGenerationHelpDescribesImplementedModes(t *testing.T) {
	entry, ok := findHelpCommand([]string{"generate"})
	if !ok {
		t.Fatal("missing generate help")
	}
	for _, usage := range entry.Usage {
		if strings.Contains(usage, "--dry-run") && !strings.Contains(usage, "generate sqlc") {
			t.Fatalf("unsupported usage: %s", usage)
		}
	}
	for _, flag := range []string{"--materialize", "--prune-materialized-go", "--merge-editor-workspace", "--check"} {
		if !strings.Contains(strings.Join(entry.Flags, " "), flag) {
			t.Errorf("missing %s", flag)
		}
	}
}
