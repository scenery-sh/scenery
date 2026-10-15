package connectionruntime

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSchemaValidationCachesOnlySuccessPerPool(t *testing.T) {
	var cache schemaChecks
	pool := new(sql.DB)
	var calls atomic.Int32
	check := func() error { calls.Add(1); return nil }
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			if err := cache.ensure(t.Context(), pool, check); err != nil {
				t.Error(err)
			}
		})
	}
	workers.Wait()
	if calls.Load() != 1 {
		t.Fatal("successful schema validation was repeated")
	}
	if err := cache.ensure(t.Context(), new(sql.DB), check); err != nil || calls.Load() != 2 {
		t.Fatal("a different pool reused schema validation")
	}
	for _, canceled := range []bool{false, true} {
		pool := new(sql.DB)
		ctx, cancel := context.WithCancel(t.Context())
		err := cache.ensure(ctx, pool, func() error {
			if canceled {
				cancel()
				return nil
			}
			return errors.New("schema unavailable")
		})
		cancel()
		if err == nil {
			t.Fatal("failed or canceled validation succeeded")
		}
		if err := cache.ensure(t.Context(), pool, check); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cache.ensure(ctx, pool, check); !errors.Is(err, context.Canceled) {
		t.Fatal("cached validation ignored cancellation")
	}
}

func TestSchemaValidationWaitCanBeCanceled(t *testing.T) {
	var cache schemaChecks
	pool := new(sql.DB)
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		done <- cache.ensure(t.Context(), pool, func() error { close(entered); <-release; return nil })
	}()
	<-entered
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := cache.ensure(ctx, pool, func() error { t.Error("waiter checked schema"); return nil }); !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
