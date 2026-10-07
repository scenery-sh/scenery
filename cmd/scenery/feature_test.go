package main

import (
	"bytes"
	"path/filepath"
	"testing"

	"scenery.sh/internal/feature"
)

func TestFeatureGrammarSeparatesPreviewAndApprovedPublication(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"land", "one", "two", "-o", "json"},
		{"land", "--candidate", "candidate", "--yes", "--expect-revision", "revision", "-o", "json"},
		{"check", "one"},
		{"check", "--candidate", "candidate", "--expect-revision", "revision"},
		{"list", "--watch", "-o", "jsonl"},
		{"create", "one", "--purpose", "Feature purpose", "--depends-on", "base"},
		{"set", "one", "--no-dependencies"},
	} {
		if _, err := parseFeatureArgs(args); err != nil {
			t.Fatalf("valid invocation %v: %v", args, err)
		}
	}
	for _, args := range [][]string{
		{"land", "--candidate", "candidate"},
		{"land", "--candidate", "candidate", "--yes"},
		{"land", "one", "two", "--commit", "checkpoint"},
		{"list", "--yes", "--expect-revision", "revision"},
		{"list", "-o", "jsonl"},
		{"list", "--watch", "-o", "json"},
		{"check", "--candidate", "candidate"},
		{"create", "one"},
		{"register", "one", "--purpose", "Purpose"},
		{"set", "one", "--depends-on", "base", "--no-dependencies"},
	} {
		if _, err := parseFeatureArgs(args); err == nil {
			t.Fatalf("invalid invocation accepted: %v", args)
		}
	}
}

func TestFeatureOverviewUsesCheckedMachineSchema(t *testing.T) {
	t.Parallel()
	response := featureResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.feature"), OK: true, Action: "list", Overview: &feature.Overview{RepoRoot: "/repo", Main: "main", Features: []feature.Row{}, Candidates: []feature.Candidate{}}}
	var output bytes.Buffer
	if err := writeFeatureResponse(&output, response, "json"); err != nil {
		t.Fatal(err)
	}
	var decoded featureResponse
	if err := decodeCLIJSON(output.Bytes(), &decoded); err != nil {
		t.Fatal(err)
	}
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.feature.schema.json"), decoded); len(diagnostics) != 0 {
		t.Fatalf("feature schema: %+v", diagnostics)
	}
	if decoded.Overview == nil || decoded.Kind != "scenery.feature" {
		t.Fatalf("missing feature overview: %+v", decoded)
	}
}
