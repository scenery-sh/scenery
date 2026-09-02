package agent

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
)

const publicRouteOwnerCheckInterval = 250 * time.Millisecond

type publicRoute struct {
	target  DeployTarget
	session Session
	running bool
}

type publicRouteSnapshot struct {
	byHost map[string]publicRoute
}

// publicRouteStore owns the lifecycle-published view used by public requests.
// Requests read one immutable snapshot; deploy/session changes build a new one.
type publicRouteStore struct {
	mu       sync.Mutex
	targets  map[string]DeployTarget
	snapshot atomic.Pointer[publicRouteSnapshot]
}

func (s *Server) refreshPublicRouteTargets() error {
	if s == nil {
		return nil
	}
	registry, err := LoadDeployRegistry(s.paths.DeployPath)
	targets := map[string]DeployTarget{}
	if err == nil {
		for _, target := range registry.Targets {
			host := normalizeRouteRequestHost(target.Domain)
			if !target.Enabled || host == "" {
				continue
			}
			if _, exists := targets[host]; !exists {
				targets[host] = target
			}
		}
	}

	s.publicRoutes.mu.Lock()
	defer s.publicRoutes.mu.Unlock()
	s.publicRoutes.targets = targets
	s.publishPublicRoutesLocked()
	return err
}

func (s *Server) refreshPublicRouteSessions() {
	if s == nil {
		return
	}
	s.publicRoutes.mu.Lock()
	defer s.publicRoutes.mu.Unlock()
	if len(s.publicRoutes.targets) == 0 {
		return
	}
	s.publishPublicRoutesLocked()
}

func (s *Server) publishPublicRoutesLocked() {
	routes := make(map[string]publicRoute, len(s.publicRoutes.targets))
	verified := map[string]bool{}
	checked := map[string]bool{}
	for host, target := range s.publicRoutes.targets {
		route := publicRoute{target: target}
		if s.registry != nil {
			for _, session := range s.registry.FindByAppRoot(target.AppRoot) {
				if session.Status != "running" || filepath.Clean(session.AppRoot) != filepath.Clean(target.AppRoot) {
					continue
				}
				if !checked[session.SessionID] {
					verified[session.SessionID] = s.registry.sessionOwnerVerifies(session)
					checked[session.SessionID] = true
				}
				if !verified[session.SessionID] {
					continue
				}
				route.session = session
				route.running = true
				break
			}
		}
		routes[host] = route
	}
	s.publicRoutes.snapshot.Store(&publicRouteSnapshot{byHost: routes})
}

func (s *Server) publicRouteForHost(host string) (publicRoute, bool) {
	host = normalizeRouteRequestHost(host)
	if host == "" || s == nil {
		return publicRoute{}, false
	}
	snapshot := s.publicRoutes.snapshot.Load()
	if snapshot == nil {
		return publicRoute{}, false
	}
	route, ok := snapshot.byHost[host]
	return route, ok
}

// monitorPublicRoutes moves deploy-file and owner verification off the HTTP
// request path. The file watcher handles atomic deploy-registry replacement;
// the bounded ticker invalidates sessions whose owner fingerprint no longer
// verifies.
func (s *Server) monitorPublicRoutes(ctx context.Context) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		slog.Warn("failed to watch scenery public deploy registry; using polling fallback", "err", err)
		s.monitorPublicRoutesByPolling(ctx)
		return
	}
	defer watcher.Close()
	if err := watcher.Add(filepath.Dir(s.paths.DeployPath)); err != nil {
		slog.Warn("failed to watch scenery public deploy registry; using polling fallback", "err", err)
		s.monitorPublicRoutesByPolling(ctx)
		return
	}
	if err := s.refreshPublicRouteTargets(); err != nil {
		slog.Warn("failed to refresh scenery public routes", "err", err)
	}

	ticker := time.NewTicker(publicRouteOwnerCheckInterval)
	defer ticker.Stop()
	deployPath := filepath.Clean(s.paths.DeployPath)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.refreshPublicRouteSessions()
		case event, ok := <-watcher.Events:
			if !ok {
				s.monitorPublicRoutesByPolling(ctx)
				return
			}
			if filepath.Clean(event.Name) != deployPath {
				continue
			}
			if err := s.refreshPublicRouteTargets(); err != nil {
				slog.Warn("failed to refresh scenery public routes", "err", err)
			}
		case watchErr, ok := <-watcher.Errors:
			if !ok {
				s.monitorPublicRoutesByPolling(ctx)
				return
			}
			slog.Warn("scenery public deploy registry watcher failed", "err", watchErr)
			if err := s.refreshPublicRouteTargets(); err != nil {
				slog.Warn("failed to refresh scenery public routes", "err", err)
			}
		}
	}
}

func (s *Server) monitorPublicRoutesByPolling(ctx context.Context) {
	ticker := time.NewTicker(publicRouteOwnerCheckInterval)
	defer ticker.Stop()
	lastError := ""
	for {
		if err := s.refreshPublicRouteTargets(); err != nil {
			if err.Error() != lastError {
				slog.Warn("failed to refresh scenery public routes", "err", err)
				lastError = err.Error()
			}
		} else {
			lastError = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
