package nativedurable

import (
	"context"
	"errors"
	"testing"
)

type memorySteps struct {
	result               []byte
	saved                bool
	states               []string
	failure              []byte
	loadError, saveError error
	context              context.Context
}

func (store *memorySteps) Load(ctx context.Context, jobID, key string) ([]byte, bool, error) {
	if ctx != store.context || jobID != "job" || key != "step" {
		panic("step persistence lost native context or identity")
	}
	return store.result, store.saved, store.loadError
}
func (store *memorySteps) Save(ctx context.Context, jobID, key, state string, result, failure []byte) error {
	if ctx != store.context || jobID != "job" || key != "step" {
		panic("step persistence lost native context or identity")
	}
	store.states = append(store.states, state)
	store.failure = failure
	if store.saveError != nil {
		return store.saveError
	}
	store.saved = state == "succeeded"
	store.result = result
	return nil
}

func TestStepReplayAndRetryPreserveNativeExecution(t *testing.T) {
	type contextKey struct{}
	pointer := &struct{ Value int }{42}
	store := &memorySteps{}
	ctx, cancel := context.WithCancel(WithStepStore(context.WithValue(context.Background(), contextKey{}, pointer), "job", store))
	defer cancel()
	store.context = ctx
	calls := 0
	failure := errors.New("implementation failed")
	run := func(got context.Context) ([]byte, error) {
		calls++
		if got != ctx || got.Value(contextKey{}) != pointer {
			t.Fatal("callback context changed")
		}
		if calls == 1 {
			return nil, failure
		}
		return []byte("success"), nil
	}
	if _, err := Step(ctx, "step", run); !errors.Is(err, failure) || string(store.failure) != failure.Error() {
		t.Fatalf("failure = %v", err)
	}
	for range 2 {
		result, err := Step(ctx, "step", run)
		if err != nil || string(result) != "success" {
			t.Fatalf("retry/replay = %q, %v", result, err)
		}
	}
	if calls != 2 || len(store.states) != 2 || store.states[0] != "failed" || store.states[1] != "succeeded" {
		t.Fatalf("calls=%d states=%v", calls, store.states)
	}
	cancel()
	if ctx.Err() != context.Canceled {
		t.Fatal("cancellation lost")
	}
}

func TestStepPersistenceFailuresDoNotReportSuccess(t *testing.T) {
	for _, phase := range []string{"load", "save", "failed-save"} {
		t.Run(phase, func(t *testing.T) {
			persistence := errors.New("persistence unavailable")
			implementation := errors.New("implementation failure")
			store := &memorySteps{}
			if phase == "load" {
				store.loadError = persistence
			} else {
				store.saveError = persistence
			}
			ctx := WithStepStore(context.Background(), "job", store)
			store.context = ctx
			calls := 0
			_, err := Step(ctx, "step", func(context.Context) ([]byte, error) {
				calls++
				if phase == "failed-save" {
					return nil, implementation
				}
				return []byte("uncommitted"), nil
			})
			want := persistence
			if phase == "failed-save" {
				want = implementation
			}
			if !errors.Is(err, want) || store.saved || phase == "load" && calls != 0 {
				t.Fatalf("error=%v calls=%d saved=%v", err, calls, store.saved)
			}
		})
	}
}
