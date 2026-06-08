package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
	"github.com/pbrazdil/onlava/internal/envpolicy"
)

type DevSessionController struct {
	root   string
	cfg    app.Config
	listen devListenRequest
}

type PreparedDevSession struct {
	Client            *localagent.Client
	Session           *localagent.Session
	Backend           devBackend
	FrontendProcesses []*managedFrontendProcess
	Cleanup           func()
}

func prepareDevAgentSession(ctx context.Context, root string, cfg app.Config, listen devListenRequest) (*localagent.Client, *localagent.Session, devBackend, func(), error) {
	prepared, err := (&DevSessionController{root: root, cfg: cfg, listen: listen}).Prepare(ctx)
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
			return prepared, fmt.Errorf("proxy.route_base_domain %q requires the onlava agent and local edge; unset ONLAVA_AGENT_DISABLE or remove proxy.route_base_domain", routeNamespace.BaseDomain)
		}
		prepared.Backend = fallback
		return prepared, nil
	}
	if strings.TrimSpace(envpolicy.Get("ONLAVA_DEV_DASHBOARD_ADDR")) == "" {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return prepared, err
		}
		_ = envpolicy.Set("ONLAVA_DEV_DASHBOARD_ADDR", addr)
		restorers = append(restorers, func() {
			_ = envpolicy.Unset("ONLAVA_DEV_DASHBOARD_ADDR")
		})
	}
	client, err := localagent.Ensure(ctx)
	if err != nil {
		if requiresPortlessEdge {
			return prepared, fmt.Errorf("proxy.route_base_domain %q requires the onlava agent and local edge; agent unavailable: %w", routeNamespace.BaseDomain, err)
		}
		fmt.Fprintf(os.Stderr, "onlava: agent unavailable; continuing without routed session URLs: %v\n", err)
		prepared.Backend = fallback
		return prepared, nil
	}
	if err := ensureDevAgentDashboardBackend(ctx, client); err != nil {
		if requiresPortlessEdge {
			return prepared, fmt.Errorf("proxy.route_base_domain %q requires the onlava agent dashboard for edge probing; dashboard unavailable: %w", routeNamespace.BaseDomain, err)
		}
		fmt.Fprintf(os.Stderr, "onlava: agent dashboard unavailable; continuing without routed session URLs: %v\n", err)
		prepared.Backend = fallback
		return prepared, nil
	}
	if strings.TrimSpace(envpolicy.Get("ONLAVA_DEV_CACHE_DIR")) == "" {
		paths, err := localagent.DefaultPaths()
		if err != nil {
			return prepared, err
		}
		if strings.TrimSpace(envpolicy.Get("ONLAVA_AGENT_HOME")) == "" {
			_ = envpolicy.Set("ONLAVA_AGENT_HOME", paths.Home)
			restorers = append(restorers, func() {
				_ = envpolicy.Unset("ONLAVA_AGENT_HOME")
			})
		}
		_ = envpolicy.Set("ONLAVA_DEV_CACHE_DIR", filepath.Join(paths.AgentDir, "dashboard"))
		restorers = append(restorers, func() {
			_ = envpolicy.Unset("ONLAVA_DEV_CACHE_DIR")
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
	sessionID := strings.TrimSpace(listen.SessionID)
	if listen.NewSession {
		generated, err := localagent.UniqueSessionID(root, "")
		if err != nil {
			return prepared, err
		}
		sessionID = generated
	}
	if err := rejectLiveDuplicateDevSession(root, sessionID, existingSessions); err != nil {
		return prepared, err
	}
	if requiresPortlessEdge {
		if _, err := checkConfiguredEdgeReadiness(ctx, client, routeNamespace.BaseDomain); err != nil {
			return prepared, err
		}
	}
	session, err := client.Register(ctx, localagent.RegisterRequest{
		BaseAppID:      cfg.AppID(),
		AppRoot:        root,
		SessionID:      sessionID,
		Status:         "starting",
		OwnerPID:       os.Getpid(),
		Backends:       backends,
		RouteNamespace: routeNamespace,
		ClaimOwner:     true,
		ClaimAliases:   listen.ClaimAliases,
	})
	if err != nil {
		return prepared, err
	}
	if requiresPortlessEdge {
		if err := verifyConfiguredEdgeSessionRoute(ctx, client, session, routeNamespace.BaseDomain, true); err != nil {
			_, _ = client.Delete(ctx, session.SessionID, false)
			return prepared, err
		}
	}
	if err := cleanupStaleDevSessionProcesses(ctx, session, existingSessions); err != nil {
		return prepared, err
	}
	backend := fallback
	if listen.Addr == "" {
		backend = devBackend{
			Network: "unix",
			Addr:    filepath.Join(session.StateRoot, "run", "api.sock"),
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
			return prepared, err
		}
		if requiresPortlessEdge {
			if err := verifyConfiguredEdgeSessionRoute(ctx, client, session, routeNamespace.BaseDomain, false); err != nil {
				_, _ = client.Delete(ctx, session.SessionID, false)
				return prepared, err
			}
		}
	}
	frontendBackends, frontendProcesses, err := managedFrontendBackendsForSession(ctx, root, cfg, baseEnv, session)
	if err != nil {
		return prepared, err
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
