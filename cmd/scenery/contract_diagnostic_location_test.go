package main

import (
	"bytes"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/scn"
)

func TestWriteContractResultPrintsDiagnosticLocations(t *testing.T) {
	t.Parallel()

	sourceID := scn.SourceID("services/scenery.package.scn")
	result := &compiler.Result{
		ContractStatus: "invalid",
		PartialGraph: &graph.PartialGraph{
			SourceMap: map[string]graph.SourceRecord{
				sourceID: {URI: "services/scenery.package.scn"},
			},
		},
		Diagnostics: []graph.Diagnostic{
			{
				Code:     "SCN3008",
				Severity: "error",
				Message:  "module supplies unknown input authorization",
				Range: &scn.Range{
					SourceID: sourceID,
					Start:    scn.Position{Line: 11, Column: 2},
					End:      scn.Position{Line: 11, Column: 15},
				},
			},
			{
				Code:     "SCN9001",
				Severity: "error",
				Message:  "internal failure",
			},
		},
	}
	var buf bytes.Buffer
	err := writeContractResult(&buf, "human", false, result, nil)
	if err == nil {
		t.Fatal("invalid result must return an error")
	}
	out := buf.String()
	if !strings.Contains(out, "services/scenery.package.scn:12:3: SCN3008: module supplies unknown input authorization") {
		t.Fatalf("missing one-based location prefix:\n%s", out)
	}
	if !strings.Contains(out, "SCN9001: internal failure") || strings.Contains(out, ": SCN9001: internal failure") {
		t.Fatalf("rangeless diagnostic format changed:\n%s", out)
	}
}

func TestContractDiagnosticLocationFallsBackToLoadedSources(t *testing.T) {
	t.Parallel()

	sourceID := scn.SourceID("scenery.scn")
	result := &compiler.Result{
		Sources: []*scn.Source{{ID: sourceID, Relative: "scenery.scn"}},
	}
	diag := graph.Diagnostic{
		Range: &scn.Range{SourceID: sourceID, Start: scn.Position{Line: 0, Column: 0}},
	}
	if got := contractDiagnosticLocation(result, diag); got != "scenery.scn:1:1" {
		t.Fatalf("location = %q, want scenery.scn:1:1", got)
	}
	if got := contractDiagnosticLocation(result, graph.Diagnostic{}); got != "" {
		t.Fatalf("rangeless location = %q, want empty", got)
	}
	diag.Range.SourceID = "src_unknown"
	if got := contractDiagnosticLocation(result, diag); got != "" {
		t.Fatalf("unknown source location = %q, want empty", got)
	}
}
