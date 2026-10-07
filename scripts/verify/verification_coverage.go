package main

import (
	"os"
	"path/filepath"
	"time"

	"scenery.sh/internal/harnessreport"
)

const harnessVerificationKind = "scenery.harness.verification"

// Applicable proof binds the final working-input revision, immutable artifact,
// selected lane and source stability. Historical green runs alone do not count.
func buildVerificationCoverage(root string, current harnessSelfResponse) *harnessreport.VerificationCoverage {
	result := &harnessreport.VerificationCoverage{PayloadIdentity: newCLIPayloadIdentity(harnessVerificationKind), Provenance: current.Provenance, GeneratedAt: current.GeneratedAt, Complete: true, Lanes: []harnessreport.VerificationLane{}}
	if current.Run == nil {
		result.Complete = false
		return result
	}
	result.RunID, result.InputRevision = current.Run.ID, current.Run.FinalInputRevision
	context := measurementContext(root, "", "", "", "", 1, 1)
	result.CI = context.CI
	required := []struct{ id, name string }{
		{"go", "go tests"}, {"client", "Scenery TypeScript client conformance"}, {"table", "Scenery table behavior guards"}, {"identity", "Scenery runtime identity checks"},
		{"client-typecheck", "Scenery TypeScript client typecheck"}, {"catalog-typecheck", "Scenery UI catalog typecheck"},
		{"assistant-helper", harnessAssistantHelperProbeName}, {"native-contract", harnessNativeContractApplicationProbeName},
	}
	histories := []harnessSelfResponse{current}
	entries, _ := os.ReadDir(filepath.Join(root, ".scenery", "harness", "runs"))
	for i := len(entries) - 1; i >= 0 && len(histories) < 101; i-- {
		if !entries[i].IsDir() {
			continue
		}
		prior, err := readHarnessJSON[harnessSelfResponse](filepath.Join(root, ".scenery", "harness", "runs", entries[i].Name(), "self.json"))
		if err != nil || prior.Kind != current.Kind || prior.SchemaRevision != current.SchemaRevision || prior.Run == nil || !prior.Run.InputsStable || prior.Run.FinalInputRevision != result.InputRevision {
			continue
		}
		histories = append(histories, prior)
	}
	now, _ := time.Parse(time.RFC3339Nano, current.GeneratedAt)
	for _, lane := range required {
		entry := selectVerificationLane(current, histories, lane.id, lane.name, now)
		if entry.Outcome != "passed" {
			result.Complete = false
		}
		result.Lanes = append(result.Lanes, entry)
	}
	return result
}

// The newest applicable outcome wins, including a current blocked prerequisite.
// A prior success must not hide a newer failure or incomplete event stream.
func selectVerificationLane(current harnessSelfResponse, histories []harnessSelfResponse, id, name string, now time.Time) harnessreport.VerificationLane {
	entry := harnessreport.VerificationLane{ID: id, Required: true, Outcome: "not_selected", Reason: "no complete applicable proof for these inputs", Command: []string{}, Boundary: "step subprocess wall time; accumulated parallel work is separate"}
	for _, step := range current.Steps {
		if harnessSkippedLane(step.Summary["skipped_lanes"], name) {
			entry.Selected, entry.Outcome, entry.Reason = true, "blocked", step.Error
			return entry
		}
	}
	for _, history := range histories {
		for _, step := range history.Steps {
			if step.Name != name {
				continue
			}
			entry.Selected = history.Run.ID == current.Run.ID
			entry.Outcome, entry.Reason = "passed", ""
			entry.RunID = history.Run.ID
			entry.Artifact = filepath.ToSlash(filepath.Join(history.Run.ArchivePath, "self.json"))
			observed, _ := time.Parse(time.RFC3339Nano, history.GeneratedAt)
			entry.AgeSeconds = max(0, now.Sub(observed).Seconds())
			entry.WallMS, entry.Command = step.DurationMS, append([]string{}, step.Command...)
			if step.Evidence != nil {
				entry.WallMS, entry.Command = step.Evidence.DurationMS, append([]string{}, step.Evidence.Command...)
			}
			switch {
			case !history.Run.InputsStable:
				entry.Outcome, entry.Reason = "incomplete", "inputs changed during execution"
			case history.Run.ArchivePath == "":
				entry.Outcome, entry.Reason, entry.Artifact = "incomplete", "proof was not retained in an immutable archive", ""
			case !step.OK:
				entry.Outcome, entry.Reason = "failed", firstNonEmpty(step.Error, "selected lane failed")
			case id == "go" && (history.TestTiming == nil || !history.TestTiming.Discovery.Complete):
				entry.Outcome, entry.Reason = "incomplete", "Go case discovery is incomplete"
			}
			return entry
		}
	}
	return entry
}

func harnessSkippedLane(value any, name string) bool {
	switch names := value.(type) {
	case []string:
		for _, candidate := range names {
			if candidate == name {
				return true
			}
		}
	case []any:
		for _, candidate := range names {
			if candidate == name {
				return true
			}
		}
	}
	return false
}
