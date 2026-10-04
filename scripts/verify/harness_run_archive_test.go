package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/harnessreport"
)

func TestRunArchivesPreserveEvidenceAcrossLaterPublication(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	var first []byte
	for _, id := range []string{"first", "second"} {
		revision := "sha256:" + strings.Repeat("a", 64)
		if id == "second" {
			revision = "sha256:" + strings.Repeat("b", 64)
		}
		run := &harnessreport.ValidationRun{ID: id, InputRevision: revision, FinalInputRevision: revision, InputsStable: true, ArchivePath: ".scenery/harness/runs/" + id}
		resp := harnessSelfResponse{PayloadIdentity: newCLIPayloadIdentity("scenery.harness.self"), Run: run, OK: true, Repo: harnessSelfRepo{Root: root}, Steps: []harnessStep{}, Artifacts: []harnessArtifact{}}
		contextPack := harnessAgentContext{Run: run}
		if err := publishHarnessRun(root, resp, contextPack); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"self.json", "summary.json", "agent-context.json", "agent-context-summary.json"} {
			data, err := os.ReadFile(filepath.Join(root, run.ArchivePath, name))
			if err != nil {
				t.Fatal(err)
			}
			var value struct {
				Run harnessreport.ValidationRun `json:"run"`
			}
			if err := json.Unmarshal(data, &value); err != nil {
				t.Fatal(err)
			}
			if value.Run != *run {
				t.Fatalf("%s changed run identity: %+v", name, value.Run)
			}
			if id == "first" && name == "self.json" {
				first = data
			}
		}
		if err := publishHarnessRun(root, resp, contextPack); err == nil {
			t.Fatal("published archive was replaced")
		}
	}
	data, err := os.ReadFile(filepath.Join(root, ".scenery/harness/runs/first/self.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != string(first) {
		t.Fatal("first evidence changed")
	}
	latest, err := os.ReadFile(filepath.Join(root, ".scenery/harness/self-latest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var value harnessSelfResponse
	if err := json.Unmarshal(latest, &value); err != nil {
		t.Fatal(err)
	}
	if value.Run.ID != "second" {
		t.Fatalf("latest = %+v", value.Run)
	}
}

func TestRunPublicationRejectsMismatchedContext(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	run := &harnessreport.ValidationRun{ID: "run", ArchivePath: ".scenery/harness/runs/run"}
	if err := publishHarnessRun(root, harnessSelfResponse{Run: run}, harnessAgentContext{}); err == nil {
		t.Fatal("accepted unrelated context")
	}
	if _, err := os.Stat(filepath.Join(root, run.ArchivePath)); !os.IsNotExist(err) {
		t.Fatal("published invalid archive")
	}
}

func TestInputRevisionBindsContentsModesDeletionAndInventory(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeTestAppFile(t, root, "source.go", "original")
	digest := func(paths ...string) string {
		t.Helper()
		value, err := hashHarnessInputs(root, paths)
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	original := digest("source.go", "missing")
	if original != digest("missing", "source.go", "source.go") {
		t.Fatal("inventory order changed digest")
	}
	writeTestAppFile(t, root, "source.go", "modified")
	if original == digest("source.go", "missing") {
		t.Fatal("content change hidden")
	}
	writeTestAppFile(t, root, "source.go", "original")
	if original != digest("source.go", "missing") {
		t.Fatal("unchanged bytes not reusable")
	}
	if err := os.Chmod(filepath.Join(root, "source.go"), 0o755); err != nil {
		t.Fatal(err)
	}
	if original == digest("source.go", "missing") {
		t.Fatal("mode change hidden")
	}
	if err := os.Remove(filepath.Join(root, "source.go")); err != nil {
		t.Fatal(err)
	}
	if original == digest("source.go", "missing") {
		t.Fatal("deletion hidden")
	}
	if digest("missing") == digest("another") {
		t.Fatal("inventory change hidden")
	}
	if _, err := hashHarnessInputs(root, []string{"../escape"}); err == nil {
		t.Fatal("accepted outside path")
	}
}
