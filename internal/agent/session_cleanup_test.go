package agent

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func verifyRecordedStartTime(owner Owner) error {
	if owner.StartedAt != "recorded" {
		return errors.New("owner process start time changed")
	}
	return nil
}

func TestSessionCleanupSelectsOnlyVerifiedRecordedProcesses(t *testing.T) {
	t.Parallel()

	session := Session{
		SessionID: "review-a",
		AppRoot:   "/app",
		OwnerPID:  41001,
		Owner:     Owner{PID: 41001, StartedAt: "recorded"},
		Processes: map[string]Process{
			"api":             {PID: 41003, Owner: Owner{PID: 41003, StartedAt: "recorded"}},
			"frontend-web":    {PID: 41002, Owner: Owner{PID: 41002, StartedAt: "recorded"}},
			"duplicate-api":   {PID: 41003, Owner: Owner{PID: 41003, StartedAt: "recorded"}},
			"unrecorded":      {PID: 41004},
			"mismatched":      {PID: 41005, Owner: Owner{PID: 41099, StartedAt: "recorded"}},
			"reused-pid":      {PID: 41006, Owner: Owner{PID: 41006, StartedAt: "another process"}},
			"current-owner":   {PID: 42000, Owner: Owner{PID: 42000, StartedAt: "recorded"}},
			"already-stopped": {PID: 41007, Owner: Owner{PID: 41007, StartedAt: "recorded"}},
		},
	}
	var owners, children []int
	record := func(target *[]int) recordedProcessStop {
		return func(_ context.Context, owner Owner) error {
			*target = append(*target, owner.PID)
			return nil
		}
	}
	seen := map[int]bool{41007: true}
	if err := stopRecordedSessionProcesses(context.Background(), session, map[int]bool{42000: true}, seen, verifyRecordedStartTime, record(&owners), record(&children)); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(owners, []int{41001}) || !slices.Equal(children, []int{41002, 41003}) {
		t.Fatalf("stopped owner/children = %v/%v, want [41001]/[41002 41003]", owners, children)
	}
	for _, pid := range []int{41004, 41005, 41006, 42000} {
		if seen[pid] {
			t.Fatalf("unowned or kept pid %d was selected: %v", pid, seen)
		}
	}
}

func TestSessionCleanupNeverSignalsAnUnverifiedOwner(t *testing.T) {
	t.Parallel()

	for name, session := range map[string]Session{
		"no record":      {OwnerPID: 41001},
		"other pid":      {OwnerPID: 41001, Owner: Owner{PID: 41002, StartedAt: "recorded"}},
		"identity moved": {OwnerPID: 41001, Owner: Owner{PID: 41001, StartedAt: "another process"}},
	} {
		if owner, ok := recordedSessionOwner(session, verifyRecordedStartTime); ok {
			t.Fatalf("%s: selected owner %+v", name, owner)
		}
	}
	if owner, ok := recordedSessionOwner(Session{Owner: Owner{PID: 41001, StartedAt: "recorded"}}, verifyRecordedStartTime); !ok || owner.PID != 41001 {
		t.Fatalf("recorded owner = %+v, %v", owner, ok)
	}
}
