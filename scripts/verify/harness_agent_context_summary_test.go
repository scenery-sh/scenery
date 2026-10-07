package main

import (
	"strings"
	"testing"

	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/repoinfo"
)

func TestCompactContextSeparatesCoverageRemainingAndConditionalProof(t *testing.T) {
	t.Parallel()
	digest := "sha256:" + strings.Repeat("a", 64)
	run := &harnessreport.ValidationRun{ID: "run", InputRevision: digest, FinalInputRevision: digest, InputsStable: true, ArchivePath: ".scenery/harness/runs/run"}
	resp := harnessSelfResponse{Run: run, OK: true, Mode: harnessSelfModeDefault, Repo: harnessSelfRepo{Root: t.TempDir()}, Steps: []harnessStep{
		{Name: "go tests", OK: true, Command: []string{"go", "test", "-json", "./..."}},
		{Name: "go vet", OK: true, Command: []string{"go", "vet", "./..."}},
		{Name: "logs", OK: true, Command: []string{"scenery", "logs", "--limit", "500", "-o", "jsonl"}},
	}}
	contextPack := harnessAgentContext{Run: run, ChangedAreaRecommendedCommands: []string{"go test ./...", "go test ./runtime", "go test ./../other", "go test -race ./runtime", "go run ./cmd/scenery generate --target typescript_client.public_api", "scenery logs --limit 500 -o jsonl", repoinfo.ValidationFullCommand, repoinfo.ValidationQuickCommand}}
	summary := buildHarnessAgentContextSummary(resp, contextPack)
	if !summary.RepoChecksPassed || summary.Run != run {
		t.Fatalf("identity/status = %+v", summary)
	}
	want := map[string]string{"go test ./...": "covered", "go test ./runtime": "covered", "go test ./../other": "remaining", "go test -race ./runtime": "remaining", "go run ./cmd/scenery generate --target typescript_client.public_api": "remaining", "scenery logs --limit 500 -o jsonl": "conditional", repoinfo.ValidationFullCommand: "covered", repoinfo.ValidationQuickCommand: "covered", "golangci-lint run ./...": "remaining"}
	for _, check := range summary.Checks {
		if check.Status != want[check.Command] {
			t.Fatalf("check = %+v, want %s", check, want[check.Command])
		}
		if check.Status == "covered" && check.EvidenceStep == "" {
			t.Fatal("coverage without evidence")
		}
		if check.Status == "conditional" && check.Condition == "" {
			t.Fatal("conditional proof without its condition")
		}
		if check.Status == "remaining" && !strings.Contains(check.Condition, "may have passed separately") {
			t.Fatal("missing receipt confused with an unexecuted check")
		}
		delete(want, check.Command)
	}
	if len(want) > 0 {
		t.Fatalf("missing checks: %v", want)
	}
	if diagnostics := validateHarnessJSONSchemaFile("../../docs/schemas/scenery.agent_context.summary.schema.json", summary); len(diagnostics) > 0 {
		t.Fatalf("schema: %v", diagnostics)
	}
	resp.Steps[1].OK = false
	summary = buildHarnessAgentContextSummary(resp, contextPack)
	for _, check := range summary.Checks {
		if check.Command == repoinfo.ValidationFullCommand && check.Status == "covered" {
			t.Fatal("full verifier covered without successful vet evidence")
		}
	}
	resp.Steps[1].OK = true
	resp.Mode = harnessSelfModeProbe
	resp.Steps = append(resp.Steps, harnessStep{Name: "verification isolation", OK: true, Command: strings.Fields(repoinfo.ValidationFullCommand)})
	summary = buildHarnessAgentContextSummary(resp, contextPack)
	for _, check := range summary.Checks {
		if check.Command == repoinfo.ValidationFullCommand && check.Status == "covered" {
			t.Fatal("probe reproduction command claimed full verification")
		}
	}
	resp.Mode = harnessSelfModeDefault
	resp.Steps[0].Command = harnessSelfGoTestCommandWithCacheMode(true)
	summary = buildHarnessAgentContextSummary(resp, contextPack)
	for _, check := range summary.Checks {
		if (check.Command == "go test ./..." || check.Command == repoinfo.ValidationFullCommand) && check.Status != "covered" {
			t.Fatalf("complete fresh suite was not covered: %+v", check)
		}
	}
	run.InputsStable = false
	summary = buildHarnessAgentContextSummary(resp, contextPack)
	if summary.RepoChecksPassed {
		t.Fatal("unstable inputs treated as proof")
	}
	for _, check := range summary.Checks {
		if check.Status == "covered" {
			t.Fatalf("unstable coverage: %+v", check)
		}
	}
	run.InputsStable = true
	resp.Steps[0].OK = false
	resp.OK = false
	summary = buildHarnessAgentContextSummary(resp, contextPack)
	for _, check := range summary.Checks {
		if check.Status == "covered" {
			t.Fatalf("failed suite/verifier coverage: %+v", check)
		}
	}
}
