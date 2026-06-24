package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

type DevSessionController struct {
	root    string
	cfg     app.Config
	listen  devListenRequest
	console *runConsole
}

// runPhase reports a timed run-output step when a console is attached so
// session preparation time (agent connect, frontend dev servers) is visible
// in `scenery up` instead of disappearing into unaccounted wall time.
func (c *DevSessionController) runPhase(title string, fn func() error) error {
	if c.console == nil {
		return fn()
	}
	return c.console.Phase(title, fn)
}

type PreparedDevSession struct {
	Client            *localagent.Client
	Session           *localagent.Session
	Backend           devBackend
	FrontendProcesses []*managedFrontendProcess
	Cleanup           func()
}

func devAPIUnixSocketPath(stateRoot string) string {
	path := filepath.Join(stateRoot, "run", "api.sock")
	if len(path) <= 100 {
		return path
	}
	id := shortIdentityHash(stateRoot)
	if id == "" {
		id = "api"
	}
	return filepath.Join(os.TempDir(), "scenery-api-"+id+".sock")
}

func prepareDevAgentSession(ctx context.Context, root string, cfg app.Config, listen devListenRequest, console *runConsole) (*localagent.Client, *localagent.Session, devBackend, func(), error) {
	prepared, err := prepareDevAgentSessionDetailed(ctx, root, cfg, listen, console)
	if err != nil {
		if prepared != nil && prepared.Cleanup != nil {
			prepared.Cleanup()
		}
		return nil, nil, devBackend{}, func() {}, err
	}
	cleanup := func() {}
	if prepared.Cleanup != nil {
		cleanup = prepared.Cleanup
	}
	return prepared.Client, prepared.Session, prepared.Backend, cleanup, nil
}

func prepareDevAgentSessionDetailed(ctx context.Context, root string, cfg app.Config, listen devListenRequest, console *runConsole) (*PreparedDevSession, error) {
	return (&DevSessionController{root: root, cfg: cfg, listen: listen, console: console}).Prepare(ctx)
}

func (c *DevSessionController) Prepare(ctx context.Context) (*PreparedDevSession, error) {
	root := c.root
	cfg := c.cfg
	listen := c.listen
	var restorers []func()
	restore := func() {
		for i := len(restorers) - 1; i >= 0; i-- {
			restorers[i]()
		}
	}
	prepared := &PreparedDevSession{Cleanup: restore}
	if listen.Addr == "" && listen.PreferTCP {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return prepared, err
		}
		listen.Network = "tcp"
		listen.Addr = addr
	}
	fallback := devBackend{Network: "tcp", Addr: listen.Addr}
	if fallback.Addr == "" {
		fallback.Addr = resolveListenAddr("", 4000)
	}
	routeNamespace := routeNamespaceForConfig(cfg)
	requiresPortlessEdge := configRequiresPortlessEdge(cfg)
	if localagent.DisabledByEnv() {
		if requiresPortlessEdge {
			return prepared, fmt.Errorf("proxy.route_base_domain %q requires the scenery agent and local edge; unset SCENERY_AGENT_DISABLE or remove proxy.route_base_domain", routeNamespace.BaseDomain)
		}
		prepared.Backend = fallback
		return prepared, nil
	}
	if strings.TrimSpace(envpolicy.Get("SCENERY_DEV_DASHBOARD_ADDR")) == "" {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return prepared, err
		}
		_ = envpolicy.Set("SCENERY_DEV_DASHBOARD_ADDR", addr)
		restorers = append(restorers, func() {
			_ = envpolicy.Unset("SCENERY_DEV_DASHBOARD_ADDR")
		})
	}
	var client *localagent.Client
	agentUnavailable := false
	if err := c.runPhase("Connecting scenery dev agent", func() error {
		var err error
		client, err = localagent.Ensure(ctx)
		if err != nil {
			if requiresPortlessEdge {
				return fmt.Errorf("proxy.route_base_domain %q requires the scenery agent and local edge; agent unavailable: %w", routeNamespace.BaseDomain, err)
			}
			fmt.Fprintf(os.Stderr, "scenery: agent unavailable; continuing without routed app URLs: %v\n", err)
			agentUnavailable = true
			return nil
		}
		if err := ensureDevAgentDashboardBackend(ctx, client); err != nil {
			if requiresPortlessEdge {
				return fmt.Errorf("proxy.route_base_domain %q requires the scenery agent dashboard for edge probing; dashboard unavailable: %w", routeNamespace.BaseDomain, err)
			}
			fmt.Fprintf(os.Stderr, "scenery: agent dashboard unavailable; continuing without routed app URLs: %v\n", err)
			agentUnavailable = true
			return nil
		}
		return nil
	}); err != nil {
		return prepared, err
	}
	if agentUnavailable {
		prepared.Backend = fallback
		return prepared, nil
	}
	if strings.TrimSpace(envpolicy.Get("SCENERY_DEV_CACHE_DIR")) == "" {
		paths, err := localagent.DefaultPaths()
		if err != nil {
			return prepared, err
		}
		if strings.TrimSpace(envpolicy.Get("SCENERY_AGENT_HOME")) == "" {
			_ = envpolicy.Set("SCENERY_AGENT_HOME", paths.Home)
			restorers = append(restorers, func() {
				_ = envpolicy.Unset("SCENERY_AGENT_HOME")
			})
		}
		_ = envpolicy.Set("SCENERY_DEV_CACHE_DIR", filepath.Join(paths.AgentDir, "dashboard"))
		restorers = append(restorers, func() {
			_ = envpolicy.Unset("SCENERY_DEV_CACHE_DIR")
		})
	}
	backends := map[string]localagent.Backend{}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), root, ".env", ".env.local")
	if err != nil {
		return prepared, err
	}
	existingSessions, _ := client.List(ctx, root)
	electricBackends, err := managedElectricBackends(cfg, baseEnv)
	if err != nil {
		return prepared, err
	}
	for name, backend := range electricBackends {
		backends[name] = backend
	}
	if listen.Addr != "" {
		backends[localagent.RouteAPI] = localagent.Backend{Network: "tcp", Addr: listen.Addr}
	}
	if err := rejectLiveDuplicateDevSession(root, existingSessions); err != nil {
		return prepared, err
	}
	var session localagent.Session
	backend := fallback
	if err := c.runPhase("Registering dev session routes", func() error {
		if requiresPortlessEdge {
			if _, err := checkConfiguredEdgeReadiness(ctx, client, routeNamespace.BaseDomain); err != nil {
				return err
			}
		}
		var err error
		session, err = client.Register(ctx, localagent.RegisterRequest{
			BaseAppID:      cfg.AppID(),
			AppRoot:        root,
			Status:         "starting",
			OwnerPID:       os.Getpid(),
			Backends:       backends,
			RouteNamespace: routeNamespace,
			ClaimOwner:     true,
			ClaimAliases:   listen.ClaimAliases,
		})
		if err != nil {
			return err
		}
		if requiresPortlessEdge {
			if err := verifyConfiguredEdgeSessionRoute(ctx, client, session, routeNamespace.BaseDomain, true); err != nil {
				_, _ = client.Delete(ctx, session.SessionID, false)
				return err
			}
		}
		if err := cleanupStaleDevSessionProcesses(ctx, session, existingSessions); err != nil {
			return err
		}
		if listen.Addr == "" {
			backend = devBackend{
				Network: "unix",
				Addr:    devAPIUnixSocketPath(session.StateRoot),
			}
			backends[localagent.RouteAPI] = localagent.Backend{Network: backend.Network, Addr: backend.Addr}
			session, err = client.Register(ctx, localagent.RegisterRequest{
				BaseAppID:      cfg.AppID(),
				AppRoot:        root,
				SessionID:      session.SessionID,
				Branch:         session.Branch,
				Status:         "starting",
				OwnerPID:       os.Getpid(),
				Backends:       backends,
				RouteNamespace: routeNamespace,
				ClaimAliases:   listen.ClaimAliases,
			})
			if err != nil {
				return err
			}
			if requiresPortlessEdge {
				if err := verifyConfiguredEdgeSessionRoute(ctx, client, session, routeNamespace.BaseDomain, false); err != nil {
					_, _ = client.Delete(ctx, session.SessionID, false)
					return err
				}
			}
		}
		return nil
	}); err != nil {
		return prepared, err
	}
	var frontendBackends map[string]localagent.Backend
	var frontendProcesses []*managedFrontendProcess
	if len(localProxyFrontends(cfg.Proxy.Frontends)) > 0 && !managedFrontendDisabled() {
		if err := c.runPhase("Starting frontend dev servers", func() error {
			var err error
			frontendBackends, frontendProcesses, err = managedFrontendBackendsForSession(ctx, root, cfg, baseEnv, session)
			return err
		}); err != nil {
			return prepared, err
		}
	}
	prepared.FrontendProcesses = frontendProcesses
	if len(frontendProcesses) > 0 {
		restorers = append(restorers, func() {
			stopManagedFrontendProcesses(frontendProcesses)
		})
	}
	if len(frontendBackends) > 0 {
		for name, backend := range frontendBackends {
			backends[name] = backend
		}
		session, err = client.Register(ctx, localagent.RegisterRequest{
			BaseAppID:      cfg.AppID(),
			AppRoot:        root,
			SessionID:      session.SessionID,
			Branch:         session.Branch,
			Status:         "starting",
			OwnerPID:       os.Getpid(),
			Backends:       backends,
			RouteNamespace: routeNamespace,
			Processes:      frontendSessionProcesses(frontendProcesses),
			ClaimAliases:   listen.ClaimAliases,
		})
		if err != nil {
			return prepared, err
		}
		if requiresPortlessEdge {
			if err := verifyConfiguredEdgeSessionRoute(ctx, client, session, routeNamespace.BaseDomain, false); err != nil {
				_, _ = client.Delete(ctx, session.SessionID, false)
				return prepared, err
			}
		}
	}
	prepared.Client = client
	prepared.Session = &session
	prepared.Backend = backend.normalized()
	return prepared, nil
}
