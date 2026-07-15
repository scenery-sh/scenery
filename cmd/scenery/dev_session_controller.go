package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

var devSessionTestHooks struct {
	sync.Mutex
	register func(localagent.RegisterRequest)
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
	FrontendReady     <-chan error
	// DomainURL is the session's dev domain base URL after a successful
	// end-to-end edge probe; empty when no dev.routing.domain applies or the
	// edge is not serving it yet.
	DomainURL string
	Cleanup   func()
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
	routingMode, err := devRoutingMode(cfg)
	if err != nil {
		return prepared, err
	}
	requiresPortlessEdge := routingMode == localagent.RouteModeHost && configRequiresPortlessEdge(cfg)
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return prepared, err
	}
	branch := discoverDevGitBranch(root)
	sessionID := localagent.SessionID(root, branch)
	domainHost := localagent.DevDomainHost(cfg.Dev.Routing.Domain, branch)
	publicRoutes, err := devExposeRouteNames(cfg)
	if err != nil {
		return prepared, err
	}
	if err := validateFrontendServeModes(cfg); err != nil {
		return prepared, err
	}
	var routeManifest localagent.RouteManifest
	var portLease localagent.PortLease
	if routingMode == localagent.RouteModePath {
		portLease, err = allocateDevPortLease(defaultDevPortLeasePath(paths), devPortLeaseRequest{
			AppRoot:       root,
			SessionID:     sessionID,
			BaseAppID:     cfg.AppID(),
			Branch:        branch,
			WorktreeLabel: firstNonEmpty(branch, filepath.Base(root)),
			Start:         cfg.Dev.Routing.PortStart,
			End:           cfg.Dev.Routing.PortEnd,
			Port:          cfg.Dev.Routing.Port,
			OwnerPID:      os.Getpid(),
		})
		if err != nil {
			return prepared, err
		}
		routeManifest = pathRouteManifestForLease(portLease, domainHost, publicRoutes)
	}
	if localagent.DisabledByEnv() {
		if requiresPortlessEdge {
			return prepared, fmt.Errorf("host routing for %q requires the scenery agent and local edge; unset SCENERY_AGENT_DISABLE or use dev.routing.mode \"path\"", routeNamespace.BaseDomain)
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
	var agentHealth localagent.HealthResponse
	agentUnavailable := false
	if err := c.runPhase("Connecting scenery dev agent", func() error {
		var err error
		client, err = localagent.Ensure(ctx, cliBuildIdentity())
		if err != nil {
			if requiresPortlessEdge {
				return fmt.Errorf("host routing for %q requires the scenery agent and local edge; agent unavailable: %w", routeNamespace.BaseDomain, err)
			}
			fmt.Fprintf(os.Stderr, "scenery: agent unavailable; continuing without routed app URLs: %v\n", err)
			agentUnavailable = true
			return nil
		}
		if err := ensureDevAgentDashboardBackend(ctx, client); err != nil {
			if requiresPortlessEdge {
				return fmt.Errorf("host routing for %q requires the scenery agent dashboard for edge probing; dashboard unavailable: %w", routeNamespace.BaseDomain, err)
			}
			fmt.Fprintf(os.Stderr, "scenery: agent dashboard unavailable; continuing without routed app URLs: %v\n", err)
			agentUnavailable = true
			return nil
		}
		agentHealth, err = client.Health(ctx)
		if err != nil {
			return err
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
	backend := fallback
	if listen.Addr != "" {
		backend = devBackend{Network: "tcp", Addr: listen.Addr}
		backends[localagent.RouteAPI] = localagent.Backend{Network: "tcp", Addr: listen.Addr}
	} else {
		backend = devBackend{
			Network: "unix",
			Addr:    devAPIUnixSocketPath(localagent.StateRoot(root, sessionID)),
		}
		backends[localagent.RouteAPI] = localagent.Backend{Network: backend.Network, Addr: backend.Addr}
	}
	if err := rejectLiveDuplicateDevSession(root, existingSessions); err != nil {
		return prepared, err
	}
	var session localagent.Session
	if requiresPortlessEdge {
		if err := c.runPhase("Checking configured edge", func() error {
			_, err := checkConfiguredEdgeReadiness(ctx, client, routeNamespace.BaseDomain)
			return err
		}); err != nil {
			return prepared, err
		}
	}
	frontendSeedSession, err := localagent.NewSession(localagent.RegisterRequest{
		BaseAppID:      cfg.AppID(),
		AppRoot:        root,
		SessionID:      sessionID,
		Branch:         branch,
		Status:         "starting",
		OwnerPID:       os.Getpid(),
		Backends:       backends,
		RouteNamespace: routeNamespace,
		RouteManifest:  routeManifest,
	}, agentHealth.RouterAddr, agentHealth.RouterScheme, nil)
	if err != nil {
		return prepared, err
	}
	var frontendBackends map[string]localagent.Backend
	var frontendProcesses []*managedFrontendProcess
	var frontendReady <-chan error
	if len(configuredFrontends(cfg.Frontends)) > 0 {
		if err := c.runPhase("Starting frontend dev servers", func() error {
			var wait func(context.Context) error
			var err error
			frontendBackends, frontendProcesses, wait, err = beginManagedFrontendBackendsForSession(ctx, root, cfg, baseEnv, frontendSeedSession)
			if wait != nil {
				ready := make(chan error, 1)
				go func() {
					ready <- c.runPhase("Waiting for frontend dev servers", func() error {
						return wait(ctx)
					})
					close(ready)
				}()
				frontendReady = ready
			}
			return err
		}); err != nil {
			return prepared, err
		}
	}
	prepared.FrontendProcesses = frontendProcesses
	prepared.FrontendReady = frontendReady
	if len(frontendProcesses) > 0 {
		restorers = append(restorers, func() {
			stopManagedFrontendProcesses(frontendProcesses)
		})
	}
	if len(frontendBackends) > 0 {
		for name, backend := range frontendBackends {
			backends[name] = backend
		}
	}
	if err := c.runPhase("Registering dev session routes", func() error {
		var err error
		req := localagent.RegisterRequest{
			BaseAppID:      cfg.AppID(),
			AppRoot:        root,
			SessionID:      sessionID,
			Branch:         branch,
			Status:         "starting",
			OwnerPID:       os.Getpid(),
			Backends:       backends,
			RouteNamespace: routeNamespace,
			RouteManifest:  routeManifest,
			Processes:      frontendSessionProcesses(frontendProcesses),
			ClaimOwner:     true,
			ClaimAliases:   listen.ClaimAliases,
		}
		notifyDevSessionRegister(req)
		session, err = client.Register(ctx, req)
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
		return nil
	}); err != nil {
		return prepared, err
	}
	if routingMode == localagent.RouteModePath {
		if strings.TrimSpace(session.RouteManifest.DomainURL) != "" || session.DomainHostConflict != nil {
			if err := c.runPhase("Checking dev domain edge", func() error {
				domainURL, warning := validateDevDomainURL(ctx, session)
				prepared.DomainURL = domainURL
				if warning != "" {
					fmt.Fprintf(os.Stderr, "scenery: %s\n", warning)
				}
				return nil
			}); err != nil {
				return prepared, err
			}
		}
		redirectURL := ""
		if prepared.DomainURL != "" {
			redirectURL = prepared.DomainURL
		} else if domain := strings.TrimSpace(cfg.Deploy.Domain); domain != "" {
			redirectURL = "https://" + domain
		}
		if err := c.runPhase("Starting local path router", func() error {
			token, err := ensureEdgeToken(paths.EdgeTokenPath)
			if err != nil {
				return err
			}
			health, err := client.Health(ctx)
			if err != nil {
				return err
			}
			// The upstream is the live agent's actual router address: with an
			// ephemeral --router-listen (127.0.0.1:0) the env-derived address
			// is not dialable, only health reports where the router bound.
			cleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
				Session:          session,
				PortLease:        portLease,
				EdgeToken:        token,
				UpstreamAddr:     firstNonEmpty(strings.TrimSpace(health.RouterAddr), localagent.RouterAddrFromEnv()),
				DashboardBackend: health.DashboardBackend,
				RedirectURL:      redirectURL,
			})
			if err != nil {
				return err
			}
			if cleanup != nil {
				restorers = append(restorers, cleanup)
			}
			return nil
		}); err != nil {
			_, _ = client.Delete(ctx, session.SessionID, false)
			return prepared, err
		}
	}
	prepared.Client = client
	prepared.Session = &session
	prepared.Backend = backend.normalized()
	return prepared, nil
}

func notifyDevSessionRegister(req localagent.RegisterRequest) {
	devSessionTestHooks.Lock()
	fn := devSessionTestHooks.register
	devSessionTestHooks.Unlock()
	if fn != nil {
		fn(req)
	}
}
