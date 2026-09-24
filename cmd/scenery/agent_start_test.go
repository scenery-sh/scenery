package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/doctor"
)

// A supervised agent whose start cannot succeed records one incident and
// stays up idle instead of exiting into its supervisor's restart; an
// unsupervised start still reports the failure, and doctor names the block.
func TestSupervisedAgentStartFailureBlocksInsteadOfRestarting(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".scenery")
	paths := localagent.PathsForHome(home)
	commandAgentPathsOverride = &paths
	t.Cleanup(func() { commandAgentPathsOverride = nil })
	stateErr := &localagent.StartFailure{Class: localagent.StartClassState, Err: errors.New("read agent session registry: invalid current artifact identity")}

	if err := containAgentStartFailure(context.Background(), agentOptions{}, stateErr); !errors.Is(err, stateErr) {
		t.Fatalf("unsupervised start = %v, want the failure", err)
	}
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	// The idle wait ends only with the process; a cancelled context stands in
	// for the supervisor's stop signal.
	if err := containAgentStartFailure(stopped, agentOptions{Supervised: true}, stateErr); err != nil {
		t.Fatalf("supervised blocked start = %v, want a successful idle exit", err)
	}
	incident, err := localagent.LoadStartIncident(paths)
	if err != nil || incident.State != "blocked" || incident.Attempts != 2 || incident.Class != localagent.StartClassState {
		t.Fatalf("incident = %+v, %v", incident, err)
	}

	check := doctorAgentStartCheck(doctor.ProbeDeps{AgentHome: func() (string, error) { return home, nil }})
	if check.Status != doctor.StatusError || check.Severity != doctor.SeverityRequired || !strings.Contains(check.Message, "BLOCKED after 2 failed start(s)") || !strings.Contains(check.SuggestedAction, "scenery system agent restart") {
		t.Fatalf("doctor check = %+v", check)
	}
	if err := localagent.ClearStartIncident(paths); err != nil {
		t.Fatal(err)
	}
	if check := doctorAgentStartCheck(doctor.ProbeDeps{AgentHome: func() (string, error) { return home, nil }}); check.Status != doctor.StatusOK {
		t.Fatalf("doctor check after a successful start = %+v", check)
	}
}

func TestParseAgentArgsAcceptsSupervised(t *testing.T) {
	t.Parallel()

	opts, err := parseAgentArgs([]string{"--socket", "/tmp/a.sock", "--router-http", "--supervised"})
	if err != nil || !opts.Supervised {
		t.Fatalf("opts = %+v, %v", opts, err)
	}
}
