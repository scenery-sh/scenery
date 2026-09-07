package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"time"

	"scenery.sh/internal/devdash"
	"scenery.sh/internal/postgresdb"
)

type worktreeDatabaseTarget struct {
	AppRoot, AppID, ResourceID, Endpoint string
}

// Retain only non-secret connection identity. A PostgreSQL restart can change
// Docker's dynamically published port while keeping all database data intact.
func (s *devSupervisor) rememberWorktreeDatabase(database postgresdb.Database, appID string) {
	var target *worktreeDatabaseTarget
	if database.Source == postgresdb.SourceManaged {
		if parsed, err := url.Parse(database.URL); err == nil {
			target = &worktreeDatabaseTarget{AppRoot: database.AppRoot, AppID: appID, ResourceID: database.ResourceID, Endpoint: parsed.Host}
		}
	}
	s.mu.Lock()
	s.postgresTarget = target
	s.mu.Unlock()
}

// The monitor observes only. It never starts, allocates or adopts a database.
// An authenticated endpoint change wakes the existing serialized rebuild loop,
// which refreshes child configuration through the ordinary resolver.
func (s *devSupervisor) startPostgresEndpointMonitor() {
	if s.worktreeRootPaths == nil {
		return
	}
	done := make(chan struct{})
	s.postgresMonitorDone = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		var lastFailure string
		for {
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
			s.mu.RLock()
			target := s.postgresTarget
			active := s.current != nil && !s.status.Compiling
			s.mu.RUnlock()
			if target == nil || !active {
				continue
			}
			ctx, cancel := context.WithTimeout(s.ctx, 3*time.Second)
			endpoint, err := observeWorktreeDatabaseEndpoint(ctx, *target)
			cancel()
			if s.ctx.Err() != nil {
				return
			}
			if err != nil {
				if err.Error() != lastFailure {
					lastFailure = err.Error()
					s.eventSink().Emit(s.ctx, devdash.DevSource{ID: "postgres", Kind: "substrate", Name: "PostgreSQL", Status: "degraded"}, "error", "worktree PostgreSQL is unavailable", map[string]any{"error": lastFailure})
				}
				continue
			}
			if lastFailure != "" {
				s.eventSink().Emit(s.ctx, devdash.DevSource{ID: "postgres", Kind: "substrate", Name: "PostgreSQL", Status: "running"}, "info", "worktree PostgreSQL connection recovered", nil)
				lastFailure = ""
			}
			if endpoint == target.Endpoint {
				continue
			}
			s.mu.RLock()
			stillCurrent := s.postgresTarget == target
			s.mu.RUnlock()
			if stillCurrent {
				select {
				case s.rebuildRequests <- struct{}{}:
					s.eventSink().Emit(s.ctx, devdash.DevSource{ID: "postgres", Kind: "substrate", Name: "PostgreSQL", Status: "starting"}, "info", "restarting the app for a verified worktree PostgreSQL endpoint change", nil)
				default:
				}
			}
		}
	}()
}

func observeWorktreeDatabaseEndpoint(ctx context.Context, target worktreeDatabaseTarget) (string, error) {
	resolver, err := newWorktreePostgresResolver(ctx, target.AppRoot, target.AppID)
	if err != nil {
		return "", err
	}
	server, running, err := resolver.observe(ctx)
	if err != nil {
		return "", err
	}
	if server == nil || server.InstanceID != target.ResourceID {
		return "", worktreePostgresPrecondition("the running application's retained database identity changed; stop and inspect the worktree")
	}
	if !running {
		return "", fmt.Errorf("the selected worktree PostgreSQL container is stopped")
	}
	return net.JoinHostPort("127.0.0.1", strconv.Itoa(server.Port)), nil
}
