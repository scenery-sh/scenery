package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestOpenAPIExportStdoutJSONAndYAML(t *testing.T) {
	t.Parallel()
	repo := repoRootForTest(t)
	houseRoot := filepath.Join(repo, "internal", "compiler", "testdata", "house")

	var jsonOut bytes.Buffer
	if err := runOpenAPIExport(&jsonOut, []string{"--app-root", houseRoot}); err != nil {
		t.Fatalf("runOpenAPIExport JSON: %v", err)
	}
	if strings.Contains(strings.ToLower(jsonOut.String()), "scenery") {
		t.Fatalf("JSON stdout leaked 'scenery':\n%s", jsonOut.String())
	}
	var parsedJSON map[string]any
	if err := json.Unmarshal(jsonOut.Bytes(), &parsedJSON); err != nil {
		t.Fatalf("invalid OpenAPI JSON: %v", err)
	}
	if parsedJSON["openapi"] != "3.1.1" {
		t.Fatalf("openapi = %v, want 3.1.1", parsedJSON["openapi"])
	}

	var yamlOut bytes.Buffer
	if err := runOpenAPIExport(&yamlOut, []string{"--format", "yaml", "--app-root", houseRoot}); err != nil {
		t.Fatalf("runOpenAPIExport YAML: %v", err)
	}
	if strings.Contains(strings.ToLower(yamlOut.String()), "scenery") {
		t.Fatalf("YAML stdout leaked 'scenery':\n%s", yamlOut.String())
	}
	var parsedYAML map[string]any
	if err := yaml.Unmarshal(yamlOut.Bytes(), &parsedYAML); err != nil {
		t.Fatalf("invalid OpenAPI YAML: %v", err)
	}
	if parsedYAML["openapi"] != "3.1.1" {
		t.Fatalf("yaml openapi = %v, want 3.1.1", parsedYAML["openapi"])
	}
}

func TestOpenAPIExportMachineJSONMatchesSchema(t *testing.T) {
	t.Parallel()
	repo := repoRootForTest(t)
	nativeRoot := filepath.Join(repo, "internal", "compiler", "testdata", "native")

	var out bytes.Buffer
	if err := runOpenAPIExport(&out, []string{"--app-root", nativeRoot, "-o", "json"}); err != nil {
		t.Fatalf("runOpenAPIExport -o json: %v", err)
	}
	envelope, err := machine.Decode[graph.Diagnostic](out.Bytes(), currentMachineSpecRevision())
	if err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !envelope.OK {
		t.Fatalf("expected ok envelope, got %+v", envelope)
	}
	payload, ok := envelope.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected object payload, got %T", envelope.Data)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs", "schemas", "scenery.openapi.export.schema.json"), payload); len(diagnostics) != 0 {
		t.Fatalf("schema validation failed: %v", diagnostics)
	}
	gateways := payload["gateways"].([]any)
	if len(gateways) != 1 {
		t.Fatalf("got %d gateways, want 1", len(gateways))
	}
	first := gateways[0].(map[string]any)
	if first["document"] == nil {
		t.Fatalf("expected inline document when --output is omitted")
	}
}

func TestGenerateOpenAPIWriteAndCheckDrift(t *testing.T) {
	t.Parallel()
	repo := repoRootForTest(t)
	houseRoot := filepath.Join(repo, "internal", "compiler", "testdata", "house")
	outFile := filepath.Join(t.TempDir(), "docs", "openapi.yaml")

	// --check fails with exit 1 / SCN6204 before the file exists and does not create it.
	var checkOut bytes.Buffer
	err := runGenerateOpenAPI(&checkOut, []string{"--app-root", houseRoot, "--format", "yaml", "--output", outFile, "--check", "-o", "json"})
	if cliExitCode(err) != 1 {
		t.Fatalf("initial --check exit = %d, err = %v", cliExitCode(err), err)
	}
	if _, statErr := os.Stat(outFile); !os.IsNotExist(statErr) {
		t.Fatalf("--check must not write %s", outFile)
	}
	envelope, decodeErr := machine.Decode[graph.Diagnostic](checkOut.Bytes(), currentMachineSpecRevision())
	if decodeErr != nil {
		t.Fatalf("decode check envelope: %v", decodeErr)
	}
	if envelope.OK || len(envelope.Diagnostics) == 0 || envelope.Diagnostics[0].Code != "SCN6204" {
		t.Fatalf("expected SCN6204 stale diagnostic, got %+v", envelope)
	}

	// Write the OpenAPI file.
	var writeOut bytes.Buffer
	if err := runGenerateOpenAPI(&writeOut, []string{"--app-root", houseRoot, "--format", "yaml", "--output", outFile}); err != nil {
		t.Fatalf("write openapi: %v", err)
	}
	written, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read written openapi: %v", err)
	}
	if strings.Contains(strings.ToLower(string(written)), "scenery") {
		t.Fatalf("written OpenAPI file leaked 'scenery'")
	}

	// --check now passes cleanly.
	var freshOut bytes.Buffer
	if err := runGenerateOpenAPI(&freshOut, []string{"--app-root", houseRoot, "--format", "yaml", "--output", outFile, "--check", "-o", "json"}); err != nil {
		t.Fatalf("fresh --check failed: %v", err)
	}
	freshEnv, err := machine.Decode[graph.Diagnostic](freshOut.Bytes(), currentMachineSpecRevision())
	if err != nil || !freshEnv.OK {
		t.Fatalf("fresh --check envelope = %+v, err = %v", freshEnv, err)
	}

	// Mutating the file triggers drift detection again.
	if err := os.WriteFile(outFile, append(written, []byte("# stale\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := runGenerateOpenAPI(&bytes.Buffer{}, []string{"--app-root", houseRoot, "--format", "yaml", "--output", outFile, "--check"}); cliExitCode(err) != 1 {
		t.Fatalf("mutated --check exit = %d, err = %v", cliExitCode(err), err)
	}
}

func TestOpenAPIInvalidRequestsReturnExitCode2(t *testing.T) {
	t.Parallel()
	repo := repoRootForTest(t)
	houseRoot := filepath.Join(repo, "internal", "compiler", "testdata", "house")

	for _, tc := range []struct {
		name string
		fn   func() error
	}{
		{"bad-format", func() error {
			return runOpenAPIExport(&bytes.Buffer{}, []string{"--format", "toml", "--app-root", houseRoot})
		}},
		{"unknown-gateway", func() error {
			return runOpenAPIExport(&bytes.Buffer{}, []string{"--gateway", "nope", "--app-root", houseRoot})
		}},
		{"unexpected-arg", func() error { return runOpenAPIExport(&bytes.Buffer{}, []string{"extra"}) }},
		{"generate-missing-output", func() error { return runGenerateOpenAPI(&bytes.Buffer{}, []string{"--app-root", houseRoot}) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := tc.fn()
			if err == nil || cliExitCode(err) != 2 {
				t.Fatalf("err = %v, exit = %d; want exit 2", err, cliExitCode(err))
			}
		})
	}
}
