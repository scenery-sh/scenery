package main

import (
	bytes "bytes"
	strings "strings"
	testing "testing"
)

func TestHarnessTextPreservesWarningAndSelectedProofScope(t *testing.T) {
	t.Parallel()
	resp := harnessSelfResponse{
		OK: true, Mode: harnessSelfModeQuick,
		Steps: []harnessStep{{Name: "postgres service probe", OK: true, Summary: postgresProbeSkip("Docker unavailable"), Diagnostics: []checkDiagnostic{postgresProbeSkipDiagnostic("Docker unavailable")}}},
	}
	summary := buildHarnessSelfSummary(resp)
	if summary.Status != "pass_with_warnings" || summary.Steps[0].Status != "warning" {
		t.Fatalf("skipped proof became green: %+v", summary)
	}
	var out bytes.Buffer
	if err := writeHarnessSelfText(&out, resp); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"pass_with_warnings", "mode=quick", "warning postgres service probe", "Docker", "skipped", "Release-only probes were not run"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("summary missing %q: %s", want, out.String())
		}
	}
}
