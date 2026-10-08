package telemetryreport

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentCommandPercentilesExcludeFailures(t *testing.T) {
	t.Parallel()
	base := time.Now().UTC().Add(-time.Minute)
	var transcript strings.Builder
	for index, run := range []struct {
		command string
		elapsed time.Duration
		failed  bool
	}{
		{"scenery check", 10 * time.Millisecond, true},
		{"scenery check", 20 * time.Millisecond, true},
		{"scenery check", time.Second, false},
		{"scenery up", 5 * time.Millisecond, true},
		{"scenery check | tail", 2 * time.Millisecond, false},
	} {
		at := base.Add(time.Duration(index) * time.Second)
		output := "done"
		if run.failed {
			output = "Exit code 1"
		}
		fmt.Fprintf(&transcript, `{"type":"assistant","timestamp":%q,"message":{"content":[{"type":"tool_use","id":"%d","name":"Bash","input":{"command":%q}}]}}`+"\n", at.Format(time.RFC3339Nano), index, run.command)
		fmt.Fprintf(&transcript, `{"type":"user","timestamp":%q,"message":{"content":[{"type":"tool_result","tool_use_id":"%d","is_error":%t,"content":%q}]}}`+"\n", at.Add(run.elapsed).Format(time.RFC3339Nano), index, run.failed, output)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "session.jsonl"), []byte(transcript.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	agents, err := readAgents(Options{ClaudeProjectsDir: root, CommandFamilies: testFamilies, Since: base.Add(-time.Hour), Until: base.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if agents.SceneryCommands != 5 || agents.SceneryAttributable != 4 || agents.SceneryAttributableFailed != 3 {
		t.Fatalf("outcome counts = %+v", agents)
	}
	for _, command := range agents.Commands {
		switch command.Command {
		case "check":
			if command.Count != 4 || command.Attributable != 3 || command.FailureCount != 2 || command.WallTimeMS != 1030 || ms(command.P50MS) != 1000 {
				t.Fatalf("mixed outcomes = %+v", command)
			}
		case "up":
			if command.FailureCount != 1 || command.WallTimeMS != 5 || command.P50MS != nil {
				t.Fatalf("only failures = %+v", command)
			}
		}
	}
}

func TestRebuildFailureFindingUsesRebuildCauses(t *testing.T) {
	t.Parallel()
	for _, failures := range []int{3, 6} {
		report := Report{Builds: Builds{
			Rebuilds:        Timing{Count: 20, FailureCount: failures},
			Failures:        []Count{{Name: "initial-only", Count: 7}, {Name: "rebuild-only", Count: failures}},
			InitialFailures: []Count{{Name: "initial-only", Count: 7}},
			RebuildFailures: []Count{{Name: "rebuild-only", Count: failures}},
		}}
		found := false
		for _, finding := range findings(report) {
			if finding.Code != "builds.failure_rate" {
				continue
			}
			found = true
			if !strings.Contains(finding.Message, "leading cause: rebuild-only") || strings.Contains(finding.Message, "initial-only") {
				t.Fatalf("cause crosses cohorts: %+v", finding)
			}
		}
		if !found {
			t.Fatalf("missing rebuild failure finding for %d failures", failures)
		}
	}
}
