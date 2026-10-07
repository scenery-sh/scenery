package telemetryreport

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/devtelemetry"
)

func TestRotatedSessionPreservesBuildCorrelationAndBlockLifetime(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	dir := filepath.Join(home, "agent", "dev")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	var log strings.Builder
	event := func(kind string, offset time.Duration, data any) {
		encoded, err := json.Marshal(map[string]any{"data": map[string]any{"type": kind, "time": at.Add(offset), "app": map[string]string{"root": "/app", "name": "app"}, "data": data}})
		if err != nil {
			t.Fatal(err)
		}
		log.Write(encoded)
		log.WriteByte('\n')
	}
	event("build.blocked", 0, map[string]any{"reason": "seed_changed", "cause": "restore applied seed", "since": at, "prevented_builds": 0})
	event("build.blocked", time.Minute, map[string]any{"reason": "seed_changed", "since": at, "prevented_builds": 4})
	event("build.unblocked", 2*time.Minute, map[string]any{"reason": "seed_changed", "since": at})
	event("build.step", 3*time.Minute, map[string]any{"operation_id": "superseded", "name": "build.request", "outcome": "superseded", "ok": false, "reason": "source_rebuild"})
	event("build.step", 4*time.Minute, map[string]any{"operation_id": "failed", "name": "build.request", "ok": false, "reason": "source_rebuild"})
	// Rotation splits an event; the reader must concatenate bytes before parsing.
	boundary := log.Len() + 31
	event("build.error", 4*time.Minute, map[string]any{"operation_id": "failed", "error": "specific failure"})
	response := devtelemetry.FirstResponse{Observation: devtelemetry.Observation{OperationID: "success", ObservedAt: at.Add(5 * time.Minute)}, At: at.Add(5*time.Minute + 1500*time.Millisecond), Generation: 2, BuildInputDigest: "digest", Status: 401}
	encoded, _ := json.Marshal(response)
	event("process.output", 6*time.Minute, map[string]string{"source": "host", "output": devtelemetry.FirstResponsePrefix + string(encoded) + "\n"})
	response.Initial, response.Generation, response.Status = true, 1, 404
	encoded, _ = json.Marshal(response)
	event("process.output", 7*time.Minute, map[string]string{"source": "host", "output": devtelemetry.FirstResponsePrefix + string(encoded) + "\n"})
	all := log.String()
	for name, data := range map[string]string{"session.log.1": all[:boundary], "session.log": all[boundary:]} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	report, err := Build(Options{AgentHome: home, Until: at.Add(10 * time.Minute), LiveStateChecked: true, ActiveBlocks: []BuildBlock{{AppRoot: "/other", Reason: "migration_pending", Since: at.Format(time.RFC3339), PreventedBuilds: 7}}})
	if err != nil {
		t.Fatal(err)
	}
	b := report.Builds
	if b.Sessions != 1 || b.Superseded != 1 || b.Rebuilds.Count != 1 || b.Rebuilds.FailureCount != 1 || b.Failures[0].Name != "specific failure" || report.Sources.SupervisorRotated != 1 {
		t.Fatalf("builds = %+v", b)
	}
	if len(b.Blocks) != 1 || b.Blocks[0].DurationMS != 120000 || b.Blocks[0].EndedAt == "" || b.PreventedBuilds != 4 || b.ActiveBlocks[0].DurationMS != 600000 {
		t.Fatalf("blocks = %+v / %+v", b.Blocks, b.ActiveBlocks)
	}
	if b.FirstResponse.PercentileSampleCount != 1 || *b.FirstResponse.P50MS != 1500 {
		t.Fatalf("first response = %+v", b.FirstResponse)
	}
	if b.HeaderObservationCount != 2 || len(b.HeaderObservations) != 2 || !b.HeaderObservations[1].Initial || b.HeaderObservations[0].Status != 401 {
		t.Fatalf("header events lost initial/status evidence: %+v", b.HeaderObservations)
	}
	if len(report.Sources.Supervisor) != 1 || report.Sources.Supervisor[0].Segments != 2 || report.Sources.Supervisor[0].First == "" || report.Sources.Supervisor[0].Last == "" {
		t.Fatalf("retained source coverage = %+v", report.Sources.Supervisor)
	}
}
