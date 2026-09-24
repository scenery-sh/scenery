package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/doctor"
)

func withAgentStartRetryWait(t *testing.T, wait func(context.Context, time.Duration) bool) {
	t.Helper()
	old := agentStartRetryWait
	t.Cleanup(func() { agentStartRetryWait = old })
	agentStartRetryWait = wait
}

// A supervised agent whose start cannot succeed records one incident and
// stays up idle instead of exiting into its supervisor's restart, and doctor
// names the block.
func TestSupervisedAgentStartFailureBlocksInsteadOfRestarting(t *testing.T) {
	home := filepath.Join(t.TempDir(), ".scenery")
	paths := localagent.PathsForHome(home)
	stateErr := &localagent.StartFailure{Class: localagent.StartClassState, Err: errors.New("read agent session registry: invalid current artifact identity")}
	failing := func(context.Context, agentOptions) (func() error, error) { return nil, stateErr }
	withAgentStartRetryWait(t, func(context.Context, time.Duration) bool {
		t.Fatal("a persistent failure must block, not retry")
		return false
	})

	// The idle wait ends only with the process; a cancelled context stands in
	// for the supervisor's stop signal. Each call is a new process.
	stopped, cancel := context.WithCancel(context.Background())
	cancel()
	for range 2 {
		var stderr bytes.Buffer
		if err := runSupervisedAgent(stopped, agentOptions{Supervised: true}, localagent.NewStartContainment(paths, "producer"), failing, &stderr); err != nil {
			t.Fatalf("supervised blocked start = %v, want a successful idle exit", err)
		}
		if !strings.Contains(stderr.String(), "start blocked after") {
			t.Fatalf("stderr = %q", stderr.String())
		}
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

// Failing to write the start incident must not disable containment: the
// supervised process retries in place with growing delays and blocks at the
// limit although no incident was ever persisted.
func TestSupervisedAgentStartStaysBoundedWhenTheIncidentCannotBeRecorded(t *testing.T) {
	paths := localagent.PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	if err := os.MkdirAll(filepath.Join(paths.AgentStartIncidentPath, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	busy := &localagent.StartFailure{Class: localagent.StartClassUnavailable, Err: errors.New("listen tcp 127.0.0.1:9440: address already in use")}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	starts := 0
	failing := func(context.Context, agentOptions) (func() error, error) {
		starts++
		if starts > 8 {
			t.Fatalf("start attempt %d: the retries were not bounded", starts)
		}
		return nil, busy
	}
	var delays []time.Duration
	withAgentStartRetryWait(t, func(_ context.Context, delay time.Duration) bool {
		delays = append(delays, delay)
		if len(delays) == 7 {
			// The eighth attempt must block; the stop signal only ends the
			// idle wait that follows.
			cancel()
		}
		return true
	})
	var stderr bytes.Buffer
	if err := runSupervisedAgent(ctx, agentOptions{Supervised: true}, localagent.NewStartContainment(paths, "producer"), failing, &stderr); err != nil {
		t.Fatalf("supervised start = %v, want a successful idle exit", err)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute}
	if starts != 8 || fmt.Sprint(delays) != fmt.Sprint(want) {
		t.Fatalf("starts = %d, delays = %v, want 8 and %v", starts, delays, want)
	}
	if !strings.Contains(stderr.String(), "start blocked after 8 attempt(s)") || !strings.Contains(stderr.String(), "could not record the start incident") {
		t.Fatalf("stderr = %q", stderr.String())
	}
}

// Publishing the agent state belongs to the start phase: a state write that
// fails after the listeners bound is a failed start, which leaves the start
// incident in place and releases what the attempt held, so the next attempt
// can succeed and only then ends the incident.
func TestAgentStartPublishesStateBeforeEndingTheIncident(t *testing.T) {
	paths := localagent.PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	commandAgentPathsOverride = &paths
	t.Cleanup(func() { commandAgentPathsOverride = nil })
	opts := agentOptions{RouterAddr: "127.0.0.1:0", RouterHTTP: true, Supervised: true}
	containment := localagent.NewStartContainment(paths, "producer")
	if _, err := containment.Record(errors.New("earlier failure"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(paths.StatePath, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := startAgentServer(ctx, opts); err == nil || !strings.Contains(err.Error(), "publish agent state") {
		t.Fatalf("start with an unwritable state = %v, want the publication failure", err)
	}
	if _, err := localagent.LoadStartIncident(paths); err != nil {
		t.Fatalf("a failed publication ended the start incident: %v", err)
	}

	if err := os.RemoveAll(paths.StatePath); err != nil {
		t.Fatal(err)
	}
	run, err := startAgentServer(ctx, opts)
	if err != nil {
		t.Fatalf("start after the state became writable = %v; the failed attempt kept a lock, socket or listener", err)
	}
	if _, err := localagent.LoadState(paths.StatePath); err != nil {
		t.Fatalf("started agent state = %v", err)
	}
	if _, err := localagent.LoadStartIncident(paths); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incident after a complete start = %v", err)
	}
	cancel()
	if err := run(); err != nil {
		t.Fatalf("run = %v", err)
	}
}

func TestParseAgentArgsAcceptsSupervised(t *testing.T) {
	t.Parallel()

	opts, err := parseAgentArgs([]string{"--socket", "/tmp/a.sock", "--router-http", "--supervised"})
	if err != nil || !opts.Supervised {
		t.Fatalf("opts = %+v, %v", opts, err)
	}
}
