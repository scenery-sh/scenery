package main

import (
	"context"
	"errors"
	"reflect"
	"testing"
)

func TestReplaceAppGenerationRestoresWithoutOverlappingWriters(t *testing.T) {
	oldPlan, newPlan := &appStartPlan{}, &appStartPlan{}
	previous := &runningApp{launch: oldPlan, pid: "old"}
	live := 1
	var events []string
	stop := func(app *runningApp) error {
		if app != previous || live != 1 {
			t.Fatal("replacement did not stop the owned previous generation")
		}
		live--
		events = append(events, "stop-old")
		return nil
	}
	start := func(_ context.Context, plan *appStartPlan) (*runningApp, error) {
		if live != 0 {
			t.Fatal("two write-producing generations would overlap")
		}
		if plan == newPlan {
			events = append(events, "candidate-failed-and-stopped")
			return nil, errors.New("application initialization failed")
		}
		if plan != oldPlan {
			t.Fatal("rollback did not use the exact retained launch")
		}
		live++
		events = append(events, "restore-old")
		return &runningApp{launch: plan, pid: "restored"}, nil
	}
	current, recovered, err := replaceAppGeneration(context.Background(), previous, newPlan, stop, start)
	if err == nil || !recovered || current == nil || current.pid != "restored" || live != 1 {
		t.Fatalf("current=%v recovered=%t err=%v live=%d", current, recovered, err, live)
	}
	if want := []string{"stop-old", "candidate-failed-and-stopped", "restore-old"}; !reflect.DeepEqual(events, want) {
		t.Fatalf("events=%v want=%v", events, want)
	}
}

func TestReplaceAppGenerationRefusesUnsafeRecovery(t *testing.T) {
	for _, scenario := range []string{"stop-unconfirmed", "candidate-shutdown-unconfirmed", "context-cancelled", "rollback-failed"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			oldPlan, candidate := &appStartPlan{}, &appStartPlan{}
			previous := &runningApp{launch: oldPlan}
			calls := 0
			stop := func(*runningApp) error {
				if scenario == "stop-unconfirmed" {
					return errors.New("stop failed")
				}
				return nil
			}
			start := func(_ context.Context, plan *appStartPlan) (*runningApp, error) {
				calls++
				if plan == oldPlan {
					return nil, errors.New("rollback failed")
				}
				if scenario == "context-cancelled" {
					cancel()
				}
				if scenario == "candidate-shutdown-unconfirmed" {
					return &runningApp{launch: plan}, errors.New("shutdown unconfirmed")
				}
				return nil, errors.New("candidate failed")
			}
			current, recovered, err := replaceAppGeneration(ctx, previous, candidate, stop, start)
			wantCalls := map[string]int{"stop-unconfirmed": 0, "candidate-shutdown-unconfirmed": 1, "context-cancelled": 1, "rollback-failed": 2}[scenario]
			if err == nil || recovered || calls != wantCalls {
				t.Fatalf("calls=%d recovered=%t err=%v", calls, recovered, err)
			}
			if scenario == "candidate-shutdown-unconfirmed" && (current == nil || current.launch != candidate) {
				t.Fatal("unconfirmed candidate was abandoned instead of retaining ownership")
			}
		})
	}
}
