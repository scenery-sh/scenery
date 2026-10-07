package main

import (
	"encoding/json"
	"reflect"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
	"testing"

	"scenery.sh/internal/harnessreport"
)

func TestCombinedSelectionAndEvidence(t *testing.T) {
	t.Parallel()
	if selectedMode([]harnessreport.ChangedFile{{Path: "docs/agent-guide.md"}}, false) != "quick" || selectedMode([]harnessreport.ChangedFile{{Path: "internal/feature/model.go"}}, false) != "default" {
		t.Fatal("combined candidate classification lost")
	}
	if selectedMode([]harnessreport.ChangedFile{{Path: "internal/feature/model.go"}}, true) != "quick" {
		t.Fatal("development must retain focused feedback")
	}
	summary := contextSummary{Checks: []contextCheck{{Command: "go test ./...", Status: "covered"}}}
	commands, err := remainingCommands(summary, []string{"go test ./...", "go vet ./..."})
	if err != nil || !reflect.DeepEqual(commands, []string{"go vet ./...", "golangci-lint run ./..."}) {
		t.Fatalf("evidence union: %v %v", commands, err)
	}
	summary.Checks = append(summary.Checks, contextCheck{Command: "scenery logs -o jsonl", Status: "conditional"})
	if _, err := remainingCommands(summary, []string{"scenery logs -o jsonl"}); err == nil {
		t.Fatal("owner scenario silently waived")
	}
	args, err := commandArgs("go run ./cmd/scenery generate -o json")
	if err != nil || args[0] != ".scenery/harness/bin/scenery" {
		t.Fatal("shared installed CLI selected")
	}
	if _, err := commandArgs("echo x; touch y"); err == nil {
		t.Fatal("shell syntax accepted")
	}
}

func TestVerifierResultUsesCurrentEnvelopeAndSummary(t *testing.T) {
	t.Parallel()
	report := harnessreport.SelfSummaryResponse{PayloadIdentity: machine.NewPayloadIdentity("scenery.harness.self.summary"), OK: true, Run: &harnessreport.ValidationRun{ID: "run", ArchivePath: ".scenery/harness/runs/run"}}
	envelope := machine.NewEnvelope[graph.Diagnostic](string(spec.CurrentRevision()), machine.Producer{Version: "dev", Toolchain: machine.Toolchain{GoVersion: "go1.27.0"}}, true, report, nil)
	encoded, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeVerifierSummary(encoded)
	if err != nil || decoded.Run == nil || decoded.Run.ID != "run" {
		t.Fatalf("current verifier result: %+v %v", decoded, err)
	}
	report.OK = false
	envelope = machine.NewEnvelope[graph.Diagnostic](string(spec.CurrentRevision()), machine.Producer{Version: "dev", Toolchain: machine.Toolchain{GoVersion: "go1.27.0"}}, false, report, nil)
	encoded, err = json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err = decodeVerifierSummary(encoded)
	if err != nil || decoded.OK || decoded.Run == nil || decoded.Run.ArchivePath != ".scenery/harness/runs/run" {
		t.Fatalf("failed verifier lost its immutable evidence: %+v %v", decoded, err)
	}
	envelope.SpecRevision = "sha256:wrong"
	encoded, _ = json.Marshal(envelope)
	if _, err := decodeVerifierSummary(encoded); err == nil {
		t.Fatal("mismatched producer specification accepted")
	}
}
