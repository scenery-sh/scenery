package agent

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStartIncidentBlocksPersistentFailuresAndBoundsTransientOnes(t *testing.T) {
	t.Parallel()

	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	now := time.Date(2026, 9, 15, 17, 0, 0, 0, time.UTC)
	stateErr := startFailure(StartClassState, errors.New("read agent session registry: invalid current artifact identity"))
	// Each attempt is a new process: the persisted incident carries the count.
	for attempt := 1; attempt <= 3; attempt++ {
		decision, err := NewStartContainment(paths, "producer-a").Record(stateErr, now.Add(time.Duration(attempt)*time.Second))
		if err != nil {
			t.Fatal(err)
		}
		if !decision.Blocked || decision.Incident.Attempts != attempt || decision.Incident.State != "blocked" || !decision.Incident.FirstAt.Equal(now.Add(time.Second)) {
			t.Fatalf("attempt %d decision = %+v; a persistent failure blocks and repeats add to one incident", attempt, decision)
		}
	}
	loaded, err := LoadStartIncident(paths)
	if err != nil || loaded.Attempts != 3 || loaded.Class != StartClassState {
		t.Fatalf("loaded incident = %+v, %v", loaded, err)
	}

	// Another producer is a new incident.
	containment := NewStartContainment(paths, "producer-b")
	decision, err := containment.Record(stateErr, now.Add(time.Minute))
	if err != nil || decision.Incident.Attempts != 1 {
		t.Fatalf("new producer decision = %+v, %v", decision, err)
	}

	busy := startFailure(StartClassUnavailable, fmt.Errorf("listen tcp 127.0.0.1:9440: address already in use"))
	var delays []time.Duration
	for attempt := 1; ; attempt++ {
		decision, err := containment.Record(busy, now.Add(time.Duration(attempt)*time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if decision.Blocked {
			if attempt != startTransientAttemptLimit {
				t.Fatalf("transient failure blocked at attempt %d, want %d", attempt, startTransientAttemptLimit)
			}
			break
		}
		delays = append(delays, decision.Delay)
	}
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second, time.Minute}
	if fmt.Sprint(delays) != fmt.Sprint(want) {
		t.Fatalf("delays = %v, want %v", delays, want)
	}
	if err := ClearStartIncident(paths); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadStartIncident(paths); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("incident after a successful start: %v", err)
	}
}

// Containment must not depend on persisting its diagnostic record: when the
// incident cannot be written, the process's own count still grows, still
// backs off and still blocks at the limit.
func TestStartContainmentBlocksWhenTheIncidentCannotBePersisted(t *testing.T) {
	t.Parallel()

	paths := PathsForHome(filepath.Join(t.TempDir(), ".scenery"))
	// A non-empty directory where the incident belongs defeats both the read
	// and the atomic rename of every write.
	if err := os.MkdirAll(filepath.Join(paths.AgentStartIncidentPath, "occupied"), 0o700); err != nil {
		t.Fatal(err)
	}
	containment := NewStartContainment(paths, "producer")
	busy := startFailure(StartClassUnavailable, errors.New("listen tcp 127.0.0.1:9440: address already in use"))
	now := time.Date(2026, 9, 24, 12, 0, 0, 0, time.UTC)
	for attempt := 1; attempt <= startTransientAttemptLimit; attempt++ {
		decision, err := containment.Record(busy, now.Add(time.Duration(attempt)*time.Second))
		if err == nil {
			t.Fatalf("attempt %d persisted its incident into a directory", attempt)
		}
		if decision.Incident.Attempts != attempt || decision.Blocked != (attempt == startTransientAttemptLimit) {
			t.Fatalf("attempt %d decision = %+v; the in-process count must bound retries", attempt, decision)
		}
	}
}
