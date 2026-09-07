package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/localproxy"
)

type DevSessionController struct {
	root             string
	cfg              app.Config
	env              app.ResolvedEnv
	listen           devListenRequest
	console          *runConsole
	paths            *localagent.Paths
	environment      []string
	frontendOverride frontendOverrideResolver
	onRegister       func(localagent.RegisterRequest)
}

func (c *DevSessionController) agentPaths() (localagent.Paths, error) {
	if c.paths != nil {
		return *c.paths, nil
	}
	return commandAgentPaths()
}

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
	DomainURL         string
	Cleanup           func()
	Paths             localagent.Paths
	MachinePaths      localagent.Paths
	Environment       []string
	Owner             *worktreeRuntimeOwner
}

type devSessionRegister func(context.Context, localagent.RegisterRequest) (localagent.Session, error)

func registerDevSessionRequest(ctx context.Context, request localagent.RegisterRequest, observe func(localagent.RegisterRequest), register devSessionRegister) (localagent.Session, error) {
	if observe != nil {
		observe(request)
	}
	return register(ctx, request)
}

func prepareDevAgentSessionDetailed(ctx context.Context, root string, cfg app.Config, env app.ResolvedEnv, listen devListenRequest, console *runConsole) (*PreparedDevSession, error) {
	return (&DevSessionController{root: root, cfg: cfg, env: env, listen: listen, console: console}).Prepare(ctx)
}

func (c *DevSessionController) Prepare(ctx context.Context) (*PreparedDevSession, error) {
	var cleanup []func()
	prepared := &PreparedDevSession{Cleanup: func() {
		for i := len(cleanup) - 1; i >= 0; i-- {
			cleanup[i]()
		}
	}}
	if err := validateFrontendServeModes(c.cfg); err != nil {
		return prepared, &codedCLIError{code: 3, err: err}
	}
	if _, err := devRoutingMode(c.env); err != nil {
		return prepared, err
	}
	machinePaths, err := c.agentPaths()
	if err != nil {
		return prepared, err
	}
	var owner *worktreeRuntimeOwner
	if err := c.runPhase("Starting worktree control", func() error {
		var err error
		owner, err = acquireWorktreeRuntime(ctx, machinePaths, c.root, c.cfg, c.env)
		return err
	}); err != nil {
		return prepared, err
	}
	cleanup = append(cleanup, owner.Close)
	prepared.Owner, prepared.Paths, prepared.MachinePaths = owner, owner.paths.ControlPaths(), machinePaths
	root, client := owner.paths.AppRoot, owner.client
	health, err := client.Health(ctx)
	if err != nil {
		return prepared, err
	}
	baseEnvironment := c.environment
	if baseEnvironment == nil {
		baseEnvironment = envpolicy.Environ()
	}
	baseEnvironment = overlayEnv(baseEnvironment, map[string]string{
		"SCENERY_DEV_DASHBOARD_ADDR": health.DashboardBackend.Addr,
		"SCENERY_DEV_CACHE_DIR":      filepath.Join(prepared.Paths.AgentDir, "dashboard"),
	})
	prepared.Environment = baseEnvironment
	baseEnv, err := appEnvWithDotEnv(baseEnvironment, root, c.env.DotEnvFiles()...)
	if err != nil {
		return prepared, err
	}
	branch := discoverDevGitBranch(root)
	sessionID := localagent.SessionID(root, branch)
	_, portText, err := net.SplitHostPort(owner.browser.Addr().String())
	if err != nil {
		return prepared, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		return prepared, err
	}
	now := time.Now().UTC()
	lease := localagent.PortLease{
		ArtifactIdentity: localagent.NewPortLeaseIdentity(),
		AppRoot:          root, SessionID: sessionID, BaseAppID: c.cfg.AppID(), Branch: branch,
		WorktreeLabel: firstNonEmpty(branch, filepath.Base(root)), Port: port,
		URL: fmt.Sprintf("http://localhost:%d", port), OwnerPID: os.Getpid(),
		Owner: localagent.CaptureOwner(os.Getpid(), "scenery worktree router"), CreatedAt: now, UpdatedAt: now,
	}
	publicRoutes, err := devExposeRouteNames(c.cfg, c.env)
	if err != nil {
		return prepared, err
	}
	// Local path routing remains available independently of machine-edge domains.
	manifest := pathRouteManifestForLease(lease, "", publicRoutes, c.cfg.RootFrontend())
	backend := devBackend{Network: "unix", Addr: filepath.Join(owner.paths.SocketDir, "api.sock")}
	if c.listen.Addr != "" {
		backend = devBackend{Network: "tcp", Addr: c.listen.Addr}
	} else if c.listen.PreferTCP {
		addr, err := freeLoopbackAddr()
		if err != nil {
			return prepared, err
		}
		backend = devBackend{Network: "tcp", Addr: addr}
	}
	backends := map[string]localagent.Backend{localagent.RouteAPI: {Network: backend.Network, Addr: backend.Addr}}
	existing, err := client.List(ctx, root)
	if err != nil {
		return prepared, err
	}
	req := localagent.RegisterRequest{
		BaseAppID: c.cfg.AppID(), Environment: c.env.Name, AppRoot: root,
		SessionID: sessionID, Branch: branch, Status: "starting", OwnerPID: os.Getpid(),
		Backends: backends, RouteNamespace: routeNamespaceForConfig(c.cfg), RouteManifest: manifest,
		ClaimOwner: true, ClaimAliases: c.listen.ClaimAliases,
	}
	seed, err := localagent.NewSession(req, health.RouterAddr, health.RouterScheme, nil)
	if err != nil {
		return prepared, err
	}
	if len(configuredFrontends(c.cfg.Frontends)) > 0 {
		if err := c.runPhase("Starting frontend dev servers", func() error {
			resolveOverride := c.frontendOverride
			if resolveOverride == nil {
				resolveOverride = localproxy.FrontendOverride
			}
			frontends, processes, wait, err := beginManagedFrontendBackendsForSessionWithOverride(ctx, root, c.cfg, baseEnv, seed, resolveOverride)
			prepared.FrontendProcesses = processes
			cleanup = append(cleanup, func() { stopManagedFrontendProcesses(processes) })
			for name, backend := range frontends {
				backends[name] = backend
			}
			if wait != nil {
				ready := make(chan error, 1)
				go func() {
					ready <- c.runPhase("Waiting for frontend dev servers", func() error { return wait(ctx) })
					close(ready)
				}()
				prepared.FrontendReady = ready
			}
			return err
		}); err != nil {
			return prepared, err
		}
	}
	req.Processes = frontendSessionProcesses(prepared.FrontendProcesses)
	var session localagent.Session
	if err := c.runPhase("Registering worktree routes", func() error {
		var err error
		session, err = registerDevSessionRequest(ctx, req, c.onRegister, client.Register)
		return err
	}); err != nil {
		return prepared, err
	}
	for _, previous := range existing {
		if sameDevSessionCleanupScope(session, previous) {
			if err := stopStaleRegisteredSessionProcesses(ctx, session, previous, map[int]bool{}); err != nil {
				return prepared, err
			}
		}
	}
	token, err := ensureEdgeToken(prepared.Paths.EdgeTokenPath)
	if err != nil {
		return prepared, err
	}
	routerCleanup, err := startLocalPathRouter(ctx, localPathRouterOptions{
		Session: session, PortLease: lease, EdgeToken: token, UpstreamAddr: health.RouterAddr,
		DashboardBackend: health.DashboardBackend, Agent: client, Listener: owner.browser,
	})
	if err != nil {
		_, _ = client.Delete(ctx, session.SessionID, false)
		return prepared, err
	}
	cleanup = append(cleanup, routerCleanup)
	domainURL, edgeCleanup, domainWarning := publishWorktreeDomain(ctx, machinePaths, session, c.env.Domain, owner.browser.Addr().String(), c.listen.ClaimAliases)
	cleanup = append(cleanup, edgeCleanup)
	if domainWarning != "" {
		fmt.Fprintln(os.Stderr, "scenery: "+domainWarning)
	}
	if domainURL != "" {
		req.RouteManifest.DomainHost, req.RouteManifest.DomainURL = c.env.Domain, domainURL
		updated, err := client.Register(ctx, req)
		if err != nil {
			return prepared, err
		}
		session = updated
		prepared.DomainURL = domainURL
	}
	prepared.Client, prepared.Session, prepared.Backend = client, &session, backend.normalized()
	return prepared, nil
}
