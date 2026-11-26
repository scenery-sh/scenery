package main

import (
	json "encoding/json"
	os "os"
	filepath "path/filepath"
	sort "sort"
	strings "strings"
	testing "testing"

	envpolicy "scenery.sh/internal/envpolicy"
	scn "scenery.sh/internal/scn"
)

func writeTestAppFile(t *testing.T, root, rel, contents string) {
	t.Helper()
	contents = normalizeTestAppConfig(rel, contents)
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll(%q): %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("WriteFile(%q): %v", path, err)
	}
	if rel == ".scenery.json" {
		contractPath := filepath.Join(root, testAppFilename)
		if _, err := os.Stat(contractPath); os.IsNotExist(err) {
			contract := "application \"test\" {}\n"
			if err := os.WriteFile(contractPath, []byte(contract), 0o644); err != nil {
				t.Fatalf("WriteFile(%q): %v", contractPath, err)
			}
		}
	}
}

func normalizeTestAppConfig(rel, contents string) string {
	if rel == ".scenery.json" {
		var cfg map[string]any
		if json.Unmarshal([]byte(contents), &cfg) == nil {
			if _, ok := cfg["envs"]; !ok {
				cfg["envs"] = map[string]any{"local": map[string]any{"default": true}}
				if encoded, err := json.Marshal(cfg); err == nil {
					contents = string(encoded)
				}
			}
		}
	}
	return contents
}

func writeHarnessSelfRepo(t *testing.T, schema string, requestedSchemas ...string) string {
	t.Helper()
	root := t.TempDir()
	writeTestAppFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.27.0\n")
	writeTestAppFile(t, root, "README.md", "# Scenery\n")
	writeTestAppFile(t, root, "AGENTS.md", "See [architecture](ARCHITECTURE.md).\n")
	writeTestAppFile(t, root, "ARCHITECTURE.md", "# Architecture\n")
	writeTestAppFile(t, root, "SKILL.md", "# Scenery Skill\n")
	writeTestAppFile(t, root, "CONTRIBUTING.md", "# Contributing\n")
	writeTestAppFile(t, root, "docs/spec/SPEC.md", "# Spec\n")
	writeTestAppFile(t, root, "docs/environment.registry.json", `{"kind":"`+envpolicy.Kind+`","schema_revision":"`+envpolicy.SchemaRevision+`","variables":[{"name":"SCENERY_TEST_","match":"prefix","scope":"test_only","direction":"test_input","category":"tests","stability":"test_only","secret":false,"allowed_in":["docs","tests"],"owner":"scenery runtime","rationale":"Test-only controls.","preferred_surface":"tests"}]}`)
	schemaNames := append([]string{}, requestedSchemas...)
	sort.Strings(schemaNames)
	for i, name := range schemaNames {
		name = strings.TrimSpace(name)
		if name == "" || filepath.Base(name) != name || !strings.HasSuffix(name, ".schema.json") {
			t.Fatalf("invalid harness fixture schema name %q", name)
		}
		if i > 0 && name == strings.TrimSpace(schemaNames[i-1]) {
			continue
		}
		if _, err := os.Stat(filepath.Join(repoRootForTest(t), "docs", "schemas", name)); err != nil {
			t.Fatalf("unknown harness fixture schema %q: %v", name, err)
		}
		writeTestAppFile(t, root, filepath.Join("docs", "schemas", name), schema)
	}
	return root
}

func repoRootForTest(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(root)
}

func diagnosticMessages(diagnostics []checkDiagnostic) string {
	messages := make([]string, 0, len(diagnostics))
	for _, diag := range diagnostics {
		messages = append(messages, diag.Message)
	}
	return strings.Join(messages, "\n")
}

const testAppFilename = scn.AppFilename
