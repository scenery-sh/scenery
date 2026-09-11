package nativeservice

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestFailedBatchRetainsSuccessfulNativeCleanup(t *testing.T) {
	var registry Registry
	type contextKey struct{}
	native := &struct{ ID int }{42}
	ctx := context.WithValue(context.Background(), contextKey{}, native)
	failure := errors.New("constructor failed")
	var mu sync.Mutex
	var initialized, stopped []string
	for _, address := range []string{"a-failed", "b-ready"} {
		if err := registry.Register(Registration{Address: address,
			Initialize: func(got context.Context) error {
				if got != ctx || got.Value(contextKey{}) != native {
					t.Error("constructor lost native context")
				}
				mu.Lock()
				initialized = append(initialized, address)
				mu.Unlock()
				if address == "a-failed" {
					return failure
				}
				return nil
			},
			Shutdown: func(got context.Context) error {
				if got != ctx || got.Value(contextKey{}) != native {
					t.Error("shutdown lost native context")
				}
				stopped = append(stopped, address)
				return nil
			},
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := registry.Register(Registration{Address: "dependent", Dependencies: []string{"a-failed", "b-ready"}, Initialize: func(context.Context) error { t.Error("dependent started after failed prerequisite"); return nil }}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Initialize(ctx); !errors.Is(err, failure) {
		t.Fatalf("initialize = %v", err)
	}
	if err := registry.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if len(initialized) != 2 || len(stopped) != 1 || stopped[0] != "b-ready" {
		t.Fatalf("initialized=%v stopped=%v", initialized, stopped)
	}
}

func TestCompositionSnapshotPreservesDependencyGraph(t *testing.T) {
	var registry Registry
	noop := func(context.Context) error { return nil }
	dependencies := []string{"base"}
	if err := registry.Register(Registration{Address: "base", Initialize: noop}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(Registration{Address: "app", Dependencies: dependencies, Initialize: noop}); err != nil {
		t.Fatal(err)
	}
	dependencies[0] = "caller-mutated"
	snapshot := registry.Snapshot()
	if !registry.AddDependency("app", "later") {
		t.Fatal("registered service absent")
	}
	restored := Restore(snapshot)
	snapshot.Initializers["app"] = Registration{}
	if err := restored.Initialize(context.Background()); err != nil {
		t.Fatalf("restored graph = %v", err)
	}
	if err := registry.Initialize(context.Background()); err == nil {
		t.Fatal("new unresolved dependency was lost")
	}
}
