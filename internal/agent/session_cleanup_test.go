package agent

import (
	context "context"
	filepath "path/filepath"

	slices "slices"
	strings "strings"
	testing "testing"
)

func TestCleanupSupersededDevSessionsSelectsSameSessionInProcess(t *testing.T) {
	t.Parallel()

	current := Session{
		SessionID: "review-a",
		AppRoot:   "/app",
	}
	previous := Session{
		SessionID: "review-a",
		AppRoot:   "/app",
	}
	unrelated := Session{
		SessionID: "review-b",
		AppRoot:   "/app",
	}
	var stopped []string
	err := cleanupStaleDevSessionProcessesWithDependencies(context.Background(), current, []Session{previous, unrelated}, staleDevSessionCleanupDependencies{
		sameScope: sameAgentSession,
		stopRegistered: func(_ context.Context, _ Session, session Session, seen map[int]bool) error {
			stopped = append(stopped, session.SessionID)
			seen[41001] = true
			return nil
		},
		stopCommands: func(_ context.Context, _ Session, seen map[int]bool) error {
			if !seen[41001] {
				t.Fatalf("command cleanup seen = %v", seen)
			}
			return nil
		},
		stopEnvironment: func(_ context.Context, _ Session, seen map[int]bool) error {
			if !seen[41001] {
				t.Fatalf("environment cleanup seen = %v", seen)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []string{"review-a"}) {
		t.Fatalf("stopped sessions = %v", stopped)
	}
}

func TestStopDeletedSessionProcessesSelectsOwnerInProcess(t *testing.T) {
	t.Parallel()

	session := Session{
		SessionID: "review-a",
		AppRoot:   "/app",
		OwnerPID:  41001,
	}
	var ownerPIDs, childPIDs []int
	err := stopDeletedSessionProcessesWithDependencies(context.Background(), session, stopDeletedSessionProcessDependencies{
		shouldSignalOwner: func(got Session) bool { return got.SessionID == session.SessionID },
		stopOwner: func(_ context.Context, pid int) error {
			ownerPIDs = append(ownerPIDs, pid)
			return nil
		},
		processPIDs: func(Session) []int { return []int{41001, 41002, 41002} },
		stopChild: func(_ context.Context, pid int) error {
			childPIDs = append(childPIDs, pid)
			return nil
		},
		stopCommands: func(_ context.Context, _ Session, seen map[int]bool) error {
			if !seen[41001] || !seen[41002] {
				t.Fatalf("command cleanup seen = %v", seen)
			}
			return nil
		},
		stopEnvironment: func(_ context.Context, _ Session, seen map[int]bool) error {
			if !seen[41001] || !seen[41002] {
				t.Fatalf("environment cleanup seen = %v", seen)
			}
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(ownerPIDs, []int{41001}) || !slices.Equal(childPIDs, []int{41002}) {
		t.Fatalf("stopped owner/children = %v/%v", ownerPIDs, childPIDs)
	}
}

func TestStopDeletedSessionProcessesSelectsStateRootMatchedOrphanInProcess(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	stateRoot := filepath.Join(root, ".scenery", "sessions", "review-a")
	otherStateRoot := filepath.Join(root, ".scenery", "sessions", "review-b")
	session := Session{SessionID: "review-a", AppRoot: root, StateRoot: stateRoot}
	ps := strings.Join([]string{
		"41001 S " + filepath.Join(stateRoot, "run", "app", "scenery-app-review-a") + " 30",
		"41002 S " + filepath.Join(otherStateRoot, "run", "app", "scenery-app-review-b") + " 30",
		"41003 Z " + filepath.Join(stateRoot, "run", "app", "scenery-app-zombie") + " 30",
	}, "\n")
	var stopped []int
	seen := map[int]bool{}
	err := stopSessionCommandProcessesFromPS(context.Background(), session, seen, ps, func(_ context.Context, pid int) error {
		stopped = append(stopped, pid)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stopped, []int{41001}) {
		t.Fatalf("stopped PIDs = %v, want matched live orphan only", stopped)
	}
	if !seen[41001] || seen[41002] || seen[41003] {
		t.Fatalf("seen PIDs = %v", seen)
	}
}
