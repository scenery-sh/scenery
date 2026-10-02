package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	durablestore "scenery.sh/internal/durable/store"
)

// runPostgresHarnessDurableNotifications proves service views share one
// notification connection outside their query pool and release it on close.
func runPostgresHarnessDurableNotifications(parent context.Context, databaseURL string) (evidence map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 8*time.Second)
	defer cancel()
	base, err := durablestore.Open(ctx, "notifications-00", databaseURL, durablestore.Options{})
	if err != nil {
		return nil, err
	}
	monitor, err := openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return nil, errors.Join(err, base.Close())
	}
	defer func() { returnErr = errors.Join(returnErr, monitor.Close()) }()
	views := []*durablestore.Store{base}
	defer func() {
		for _, view := range views {
			returnErr = errors.Join(returnErr, view.Close())
		}
	}()
	for index := 1; index < 12; index++ {
		view, err := base.ForService(fmt.Sprintf("notifications-%02d", index))
		if err != nil {
			return nil, err
		}
		views = append(views, view)
	}
	for index, view := range views {
		tasks := []durablestore.TaskDeclaration{{Name: "echo", HandlerRef: "echo", MaxAttempts: 3}}
		if index == 0 {
			tasks = append(tasks, durablestore.TaskDeclaration{Name: "peer", HandlerRef: "peer", MaxAttempts: 3})
		}
		if err := view.ReconcileTasks(ctx, tasks); err != nil {
			return nil, err
		}
	}
	wakes := make([]<-chan struct{}, len(views))
	releases := make([]func(), len(views))
	for index, view := range views {
		wake, release := view.TaskWake("echo")
		defer release()
		wakes[index] = wake
		releases[index] = release
	}
	// The final LISTEN proves that the current connection subscribed every
	// channel. Inspect through the durable query pool itself: the old design
	// exhausts its ten slots before this query can complete.
	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	defer readyCancel()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var listeners, ready int
		if err := base.DB().QueryRowContext(readyCtx, `
SELECT count(*), count(*) FILTER (WHERE query = 'LISTEN "scenery_durable_notifications-11"')
FROM pg_stat_activity
WHERE datname = current_database() AND application_name = 'scenery durable notifications'
`).Scan(&listeners, &ready); err != nil {
			return nil, fmt.Errorf("durable notification query capacity: %w", err)
		}
		if ready == 1 {
			if listeners != 1 || base.DB().Stats().InUse != 0 {
				return nil, fmt.Errorf("notification connections: listeners=%d, query pool in use=%d", listeners, base.DB().Stats().InUse)
			}
			break
		}
		select {
		case <-readyCtx.Done():
			return nil, fmt.Errorf("durable listener did not subscribe all services: %w", readyCtx.Err())
		case <-ticker.C:
		}
	}
	for index := range views {
		select {
		case <-wakes[index]:
		case <-ctx.Done():
			return nil, fmt.Errorf("durable setup observation did not reconnect: %w", ctx.Err())
		}
		// Discard setup wakeups so this observation proves actual pg_notify.
		releases[index]()
		wake, release := views[index].TaskWake("echo")
		defer release()
		wakes[index] = wake
	}
	for index, view := range views {
		if _, err := view.Start(ctx, durablestore.StartRequest{ID: "notify-job", TaskName: "echo", InputBlob: []byte(`{}`)}); err != nil {
			return nil, err
		}
		select {
		case <-wakes[index]:
		case <-ctx.Done():
			return nil, fmt.Errorf("durable notification for service %s: %w", view.Service, ctx.Err())
		}
	}
	// Known deadlines shorten idle reconciliation without sleeping through a
	// future job; acquisition recovers only the task it is acquiring.
	if _, err := base.Start(ctx, durablestore.StartRequest{ID: "peer-job", TaskName: "peer", InputBlob: []byte(`{}`)}); err != nil {
		return nil, err
	}
	if _, ok, err := base.LeaseReadyJob(ctx, "worker", "peer-lease", "peer"); err != nil || !ok {
		return nil, fmt.Errorf("peer acquisition: ok=%t err=%w", ok, err)
	}
	if _, err := base.DB().ExecContext(ctx, `UPDATE scenery.durable_jobs SET run_after = now() + interval '5 seconds' WHERE service=$1 AND id='notify-job';`, base.Service); err != nil {
		return nil, err
	}
	if _, err := base.DB().ExecContext(ctx, `UPDATE scenery.durable_jobs SET lease_until = now() - interval '1 second' WHERE service=$1 AND id='peer-job';`, base.Service); err != nil {
		return nil, err
	}
	idle, ok, err := base.LeaseReadyJob(ctx, "worker", "future-lease", "echo")
	if err != nil || ok || idle.WakeAfter < 3*time.Second || idle.WakeAfter > 5*time.Second {
		return nil, fmt.Errorf("future acquisition deadline: ok=%t wake=%v err=%w", ok, idle.WakeAfter, err)
	}
	peer, found, err := base.GetJob(ctx, "peer-job")
	if err != nil || !found || peer.State != "running" {
		return nil, fmt.Errorf("echo acquisition recovered peer: state=%s found=%t err=%w", peer.State, found, err)
	}
	recovered, ok, err := base.LeaseReadyJob(ctx, "worker", "peer-recovery", "peer")
	if err != nil || !ok || recovered.Attempt != 2 {
		return nil, fmt.Errorf("peer expiration recovery: ok=%t attempt=%d err=%w", ok, recovered.Attempt, err)
	}
	if err := base.Close(); err != nil {
		return nil, err
	}
	var listeners int
	for {
		if err := monitor.QueryRowContext(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND application_name = 'scenery durable notifications'`).Scan(&listeners); err != nil {
			return nil, err
		}
		if listeners == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("durable listener survived store close: %d", listeners)
		case <-ticker.C:
		}
	}
	return map[string]any{"services": len(views), "listener_connections": 1, "query_pool_in_use": 0, "listener_connections_after_close": listeners, "task_recovery_isolated": true, "recovered_attempt": recovered.Attempt, "next_wake_ms": idle.WakeAfter.Milliseconds()}, nil
}
