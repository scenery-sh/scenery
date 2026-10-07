package feature

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOverviewPreservesPartialLandingAndIndependentOverlap(t *testing.T) {
	t.Parallel()
	overview := Overview{Features: []Row{
		{Record: Record{Name: "foundation", Stage: "ready"}, Landing: "partially_landed", Outstanding: []string{"shared.go"}},
		{Record: Record{Name: "consumer", Stage: "ready", Dependencies: []string{"foundation"}}, Landing: "not_landed", Outstanding: []string{"shared.go", "ui.ts"}},
		{Record: Record{Name: "independent", Stage: "ready"}, Landing: "not_landed", Outstanding: []string{"other.go"}},
	}}
	finishOverview(&overview)
	if overview.Features[0].Status != "partially_landed" || overview.Features[1].Status != "blocked" || overview.Features[2].Status != "ready" {
		t.Fatalf("wrong states: %+v", overview.Features)
	}
	if len(overview.Features[0].Overlap) != 1 || !reflect.DeepEqual(overview.Features[0].Overlap[0].Paths, []string{"shared.go"}) || len(overview.Features[2].Overlap) != 0 {
		t.Fatalf("wrong overlap: %+v", overview.Features)
	}
	if !strings.Contains(strings.Join(overview.Features[1].Blockers, ","), "foundation") {
		t.Fatal("missing dependency blocker")
	}
}

func TestDependencyGraphRejectsUnknownAndCycles(t *testing.T) {
	t.Parallel()
	records := []Record{{Name: "one", Dependencies: []string{"two"}}, {Name: "two"}}
	if err := validateDependencies(Record{Name: "two", Dependencies: []string{"one"}}, records); err == nil {
		t.Fatal("cycle accepted")
	}
	if err := validateDependencies(Record{Name: "three", Dependencies: []string{"absent"}}, records); err == nil {
		t.Fatal("unknown dependency accepted")
	}
	if err := validateDependencies(Record{Name: "three", Dependencies: []string{"one"}}, records); err != nil {
		t.Fatal(err)
	}
}

func TestReceiptsRequireExactInputsEnvironmentCommandAndArchive(t *testing.T) {
	t.Parallel()
	archive := t.TempDir()
	check := Check{ID: "unit", Command: "go", Args: []string{"test", "./..."}, Reuse: true}
	receipt := CheckReceipt{ID: "unit", Revision: "inputs", Environment: "tools", Command: []string{"go", "test", "./..."}, CWD: "repo", Outcome: "passed", Archive: archive}
	if err := writeJSON(filepath.Join(archive, "receipt.json"), receipt, true); err != nil {
		t.Fatal(err)
	}
	if got := reusableReceipt([]CheckReceipt{receipt}, check, "inputs", "tools", "repo", receipt.Command); got == nil || !got.Reused {
		t.Fatal("applicable receipt was not reused")
	}
	for _, change := range []struct {
		revision, environment, cwd string
		command                    []string
	}{
		{"changed", "tools", "repo", receipt.Command}, {"inputs", "changed", "repo", receipt.Command}, {"inputs", "tools", "other", receipt.Command}, {"inputs", "tools", "repo", []string{"go", "test", "./other"}},
	} {
		if reusableReceipt([]CheckReceipt{receipt}, check, change.revision, change.environment, change.cwd, change.command) != nil {
			t.Fatalf("unrelated receipt reused: %+v", change)
		}
	}
	check.Expensive = true
	if reusableReceipt([]CheckReceipt{receipt}, check, "inputs", "tools", "repo", receipt.Command) != nil {
		t.Fatal("external probe reused")
	}
	check.Expensive = false
	if err := os.Remove(filepath.Join(archive, "receipt.json")); err != nil {
		t.Fatal(err)
	}
	if reusableReceipt([]CheckReceipt{receipt}, check, "inputs", "tools", "repo", receipt.Command) != nil {
		t.Fatal("missing archive reused")
	}
}

func TestCandidateApprovalBindsHistoryPolicyAndCheckpoint(t *testing.T) {
	t.Parallel()
	base := Candidate{Base: "base", Tip: "tip", Tree: "tree", Checkpoints: []Checkpoint{{Feature: "one", Commit: "checkpoint"}}, Checks: []Check{{ID: "check", Command: "go"}}}
	before := candidateRevision(base)
	changed := base
	changed.Tip = "new history"
	if candidateRevision(changed) == before {
		t.Fatal("new history retained approval")
	}
	changed = base
	changed.Checkpoints = []Checkpoint{{Feature: "one", Commit: "later checkpoint"}}
	if candidateRevision(changed) == before {
		t.Fatal("new checkpoint retained approval")
	}
	changed = base
	changed.Checks = []Check{{ID: "check", Command: "other"}}
	if candidateRevision(changed) == before {
		t.Fatal("new policy retained approval")
	}
	changed = base
	changed.Status = "validated"
	changed.Receipts = []CheckReceipt{{ID: "check", Outcome: "passed"}}
	if candidateRevision(changed) != before {
		t.Fatal("execution evidence changed the approved source revision")
	}
}

func TestPolicyUnionRetainsBaseRequirementsAndSelectsBoundaries(t *testing.T) {
	t.Parallel()
	base := Policy{Landing: []Check{{ID: "unit", Command: "go"}, {ID: "git", Command: "probe", WhenPaths: []string{"internal/feature/"}, Expensive: true}}}
	candidate := Policy{Landing: []Check{{ID: "unit", Command: "go"}, {ID: "new", Command: "lint"}}}
	all := checkUnion(base, candidate)
	if len(all) != 3 || len(selectChecks(all, []string{"docs/guide.md"})) != 2 || len(selectChecks(all, []string{"internal/feature/ledger.go"})) != 3 {
		t.Fatalf("lost required check or wrong boundary selection: %+v", all)
	}
}

func TestStrictRecordsAndOwnedCandidatePaths(t *testing.T) {
	t.Parallel()
	var record Record
	for _, value := range []string{`{"unexpected":true}`, `{} {}`, `{} ]`} {
		if decodeJSON([]byte(value), &record) == nil {
			t.Fatalf("accepted invalid record %s", value)
		}
	}
	m := Manager{State: t.TempDir()}
	for _, value := range []string{"../outside", "/absolute", "UPPER", ""} {
		if _, err := m.recordPath(value); err == nil {
			t.Fatalf("accepted unsafe record %q", value)
		}
		if _, err := m.candidatePath(value); err == nil {
			t.Fatalf("accepted unsafe candidate %q", value)
		}
	}
	if !reflect.DeepEqual(pathsFromGit(" leading.go\x00line\nname.go\x00"), []string{" leading.go", "line\nname.go"}) {
		t.Fatal("Git paths changed bytes")
	}
}
