package main

import (
	"context"
	"errors"
	"os"
	"testing"
	"testing/synctest"

	"scenery.sh/internal/assistantruntime"
)

func TestAssistantFailedStartupRetainsUnconfirmedProcess(t *testing.T) {
	for _, failure := range []string{"client", "readiness"} {
		t.Run(failure, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				s, _, _ := assistantStageFixture(t)
				const address = "app/assistant/support"
				prepared := s.prepared[address]
				ownedRoot := prepared.ownedRoot
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				want := assistantruntime.ErrInvalidControlAddress
				if failure == "client" {
					prepared.controlURL = "invalid-control-address"
					s.prepared[address] = prepared
				} else {
					cancel()
					want = context.Canceled
				}
				done := make(chan struct{})
				process := &devManagedProcess{PID: 42, Done: done}
				starts, recordedPID := 0, 0
				s.config.ProcessFactory = func(context.Context, devProcessStartRequest) (*devManagedProcess, error) {
					starts++
					return process, nil
				}
				s.config.OnProcess = func(_ string, pid int) { recordedPID = pid }
				if err := s.startDefinition(ctx, prepared.definition); !errors.Is(err, want) {
					t.Fatalf("startup error = %v, want %v", err, want)
				}
				instance := s.instances[address]
				if instance == nil || instance.process != process || !instance.stopping || recordedPID != 42 {
					t.Fatalf("unconfirmed process was not retained: instance=%+v recordedPID=%d", instance, recordedPID)
				}
				if got := s.ProcessSnapshot()["assistant-support"].PID; got != 42 {
					t.Fatalf("process snapshot lost unconfirmed PID: %d", got)
				}
				if _, err := os.Stat(ownedRoot); err != nil {
					t.Fatalf("live helper files were removed: %v", err)
				}
				if err := s.startDefinition(context.Background(), prepared.definition); err == nil || starts != 1 || s.restarts[address] != 0 {
					t.Fatalf("unconfirmed shutdown allowed retry: err=%v starts=%d retries=%d", err, starts, s.restarts[address])
				}
				for range 2 {
					if err := s.Close(); err == nil || s.instances[address] != instance {
						t.Fatalf("Close discarded unconfirmed process: %v", err)
					}
				}
				close(done)
				if err := s.Close(); err != nil || len(s.instances) != 0 || recordedPID != 0 {
					t.Fatalf("confirmed exit did not release ownership: err=%v pid=%d", err, recordedPID)
				}
				if _, err := os.Stat(ownedRoot); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("stopped helper retained private files: %v", err)
				}
			})
		})
	}
}
