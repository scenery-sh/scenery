package main

import (
	"strings"
	"testing"
)

func TestInspectDocsExcerptsRejectNonUTF8Source(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, "README.md", "# Readme\n"+string([]byte{0xff}))
	documents := []inspectDocsDocument{{}}
	documents[0].Path = "README.md"
	documents[0].Role = "direct"
	if err := attachInspectDocsExcerpts(root, documents); err == nil {
		t.Fatal("accepted non-UTF-8 source as an exact JSON excerpt")
	}
}

func TestInspectDocsMultiPathUnionAndExcerptBounds(t *testing.T) {
	t.Parallel()
	root := writeHarnessSelfRepo(t, `{"type":"object"}`)
	writeTestAppFile(t, root, "README.md", "# Readme\n\n## Runtime\n"+strings.Repeat("runtime details\n", 2000))
	appendInspectDocsTestDocuments(t, root, inspectDocsTestDocument("README.md", "Readme", "active", []string{"runtime"}))
	opts, err := parseInspectArgs([]string{"docs", "--for-path", "runtime/server", "--for-path", "README.md", "--for-path", "./README.md", "--include-text"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := buildInspectDocsResponseForOptions(root, opts.Docs)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(payload.Query.ForPaths, ","); got != "README.md,runtime/server" {
		t.Fatalf("normalized paths = %s", got)
	}
	if !stringSliceContains(payload.VerificationCommands, harnessValidationFullCommand) || stringSliceContains(payload.VerificationCommands, harnessValidationQuickCommand) {
		t.Fatalf("verification union = %v", payload.VerificationCommands)
	}
	seen := map[string]bool{}
	total := 0
	truncated := false
	for _, doc := range payload.Documents {
		if seen[doc.Path] {
			t.Fatalf("duplicate document %s", doc.Path)
		}
		seen[doc.Path] = true
		for _, section := range doc.Sections {
			if section.Excerpt == nil {
				t.Fatalf("missing excerpt for %s", section.Anchor)
			}
			excerpt := section.Excerpt
			total += len(excerpt.Text)
			if len(excerpt.Text) > maxInspectDocsExcerptBytes || excerpt.EndLine > section.EndLine || !strings.HasPrefix(excerpt.ContentRevision, "sha256:") {
				t.Fatalf("invalid excerpt: %+v", excerpt)
			}
			truncated = truncated || excerpt.Truncated
		}
	}
	if total > maxInspectDocsTextBytes || !truncated {
		t.Fatalf("text=%d truncated=%v", total, truncated)
	}
	if _, err := buildInspectDocsResponseForOptions(root, inspectDocsOptions{IncludeText: true}); err == nil {
		t.Fatal("accepted text without paths")
	}
	if _, err := buildInspectDocsQuery(root, inspectDocsOptions{ForPaths: []string{"../outside"}}); err == nil {
		t.Fatal("accepted outside path")
	}
}

func TestInspectDocsTypeScriptRuntimeSelectsNormativeScalarSections(t *testing.T) {
	t.Parallel()
	root := writeHarnessSelfRepo(t, `{"type":"object"}`)
	writeTestAppFile(t, root, "docs/spec/typescript-client.md", "# TypeScript Client\n\n## 7. Scalar type mapping\nint64 is bigint.\n\n## 8. Composite type mapping\nExact collection types.\n\n## 9. Records and unknown fields\nReject unknown fields.\n\n## 11. Tagged unions\nPreserve unknown open tags.\n")
	writeTestAppFile(t, root, "docs/agent-guide.md", "# Guide\n\n## TypeScript Client Integration\nGenerate the client.\n")
	writeTestAppFile(t, root, "docs/local-contract.md", "# Local Contract\n\n## App Config\nruntime TypeScript generation\n\n## Harness\nruntime TypeScript generation\n")
	writeTestAppFile(t, root, "docs/schemas/scenery.typescript-client-generated.schema.json", `{"type":"object"}`)
	appendInspectDocsTestDocuments(t, root,
		inspectDocsTestDocument("docs/spec/typescript-client.md", "TypeScript Client", "reference", []string{"typescript"}),
		inspectDocsTestDocument("docs/agent-guide.md", "Guide", "active", []string{"agents"}),
		inspectDocsTestDocument("docs/schemas/scenery.typescript-client-generated.schema.json", "Generated client", "active", []string{"schema"}),
	)
	query := inspectDocsOptions{ForPaths: []string{"internal/generate/generate_typescript_runtime_json"}, IncludeText: true}
	payload, err := buildInspectDocsResponseForOptions(root, query)
	if err != nil {
		t.Fatal(err)
	}
	if inspectDocsHasDocument(payload.Documents, "docs/local-contract.md") {
		t.Fatal("unrelated app config/harness contract selected")
	}
	for _, doc := range payload.Documents {
		if doc.Path == "docs/spec/typescript-client.md" {
			if len(doc.Sections) != 4 || doc.Sections[0].Anchor != "7-scalar-type-mapping" || !strings.Contains(doc.Sections[0].Excerpt.Text, "int64 is bigint") {
				t.Fatalf("sections = %+v", doc.Sections)
			}
		}
	}
	if diagnostics := validateHarnessJSONSchemaFile("../../docs/schemas/scenery.inspect.docs.schema.json", payload); len(diagnostics) > 0 {
		t.Fatalf("schema: %v", diagnostics)
	}
}
