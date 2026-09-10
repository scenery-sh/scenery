package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
)

type worktreeRuntimeOwner struct {
	paths     localagent.WorktreePaths
	record    localagent.WorktreeRecord
	client    *localagent.Client
	server    *localagent.Server
	dashboard *agentDashboardRuntime
	live      *localagent.ProcessLock
	browser   net.Listener
	cancel    context.CancelFunc
	failure   chan error
	runDone   chan struct{}
	once      sync.Once
}

func (o *worktreeRuntimeOwner) Close() {
	if o == nil {
		return
	}
	o.once.Do(func() {
		if o.dashboard != nil {
			_ = o.dashboard.Close()
		}
		if o.server != nil {
			_ = o.server.Close()
		}
		if o.cancel != nil {
			o.cancel()
		}
		if o.browser != nil {
			_ = o.browser.Close()
		}
		if o.runDone != nil {
			<-o.runDone
		}
		// Refresh retention age only from the current record; startup's copy
		// predates database provisioning and must never overwrite it.
		if o.record.AppID != "" {
			if op, err := o.paths.BeginOperation(); err == nil {
				if record, err := o.paths.LoadRecord(o.record.AppID); err == nil {
					_ = op.SaveRecord(record)
				}
				_ = op.Close()
			}
		}
		if o.live != nil {
			_ = o.live.Release()
		}
	})
}

func acquireWorktreeRuntime(ctx context.Context, machinePaths localagent.Paths, root string, cfg app.Config, env app.ResolvedEnv) (*worktreeRuntimeOwner, error) {
	paths, err := localagent.PathsForWorktree(machinePaths.Home, root)
	if err != nil {
		return nil, err
	}
	live, err := paths.AcquireLiveLock()
	if err != nil {
		if errors.Is(err, localagent.ErrProcessLocked) {
			return nil, existingWorktreeRuntime(ctx, paths, cfg.AppID(), env.Name)
		}
		return nil, err
	}
	o := &worktreeRuntimeOwner{paths: paths, live: live, failure: make(chan error, 1)}
	success := false
	defer func() {
		if !success {
			o.Close()
		}
	}()
	// Preserve duplicate-up inspection, but reject a new incoherent owner before
	// allocating browser endpoints, services or persisted resource authority.
	if err := build.VerifyFrameworkSession(ctx, root); err != nil {
		return nil, &codedCLIError{code: 3, err: err}
	}
	if len(cfg.Database.Migrations) > 0 {
		requirements, err := compileSQLRequirements(root)
		if err != nil {
			return nil, err
		}
		if _, err := discoverDBMigrationPlans(root, cfg, requirements); err != nil {
			return nil, &codedCLIError{code: 3, err: err}
		}
	}
	op, err := paths.BeginOperation()
	if err != nil {
		return nil, err
	}
	record, err := paths.LoadRecord(cfg.AppID())
	if errors.Is(err, os.ErrNotExist) {
		record = localagent.NewWorktreeRecord(paths, cfg.AppID())
		claimErr := localagent.CheckLegacyWorktreeClaim(machinePaths, paths)
		// A conflicting legacy claim is enforced by the existing SQL allocator
		// if the compiled program actually requests managed SQL.
		record.SQLAllocationChecked = claimErr == nil
	} else if err != nil {
		_ = op.Close()
		return nil, err
	}
	if p := record.Postgres; p != nil {
		if p.Major != 18 || p.Restore != nil || p.Phase == "restoring" || p.Phase == "restore-failed" || p.Phase == "deleting" {
			_ = op.Close()
			return nil, worktreePostgresPrecondition("retained database requires explicit engine, restore or deletion recovery before application startup")
		}
	}
	browser, err := bindWorktreeBrowser(record.RouterAddress, paths.AppRoot, env)
	if err != nil {
		_ = op.Close()
		return nil, err
	}
	o.browser = browser
	record.RouterAddress = browser.Addr().String()
	if err := op.SaveRecord(record); err != nil {
		_ = op.Close()
		return nil, err
	}
	if err := op.Close(); err != nil {
		return nil, err
	}
	o.record = record
	controlPaths := paths.ControlPaths()
	if err := localagent.EnsureDirs(controlPaths); err != nil {
		return nil, err
	}
	token, err := ensureEdgeToken(controlPaths.EdgeTokenPath)
	if err != nil {
		return nil, err
	}
	router, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	dashboard, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		_ = router.Close()
		return nil, err
	}
	o.server, err = localagent.NewWorktreeServer(paths, record, cliBuildIdentity(), localagent.Backend{Network: "tcp", Addr: dashboard.Addr().String()}, token, router)
	if err != nil {
		_ = router.Close()
		_ = dashboard.Close()
		return nil, err
	}
	// Control outlives application shutdown so owned-session cleanup and status
	// writes can complete before the lifetime lock is released.
	ownerCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	o.cancel = cancel
	o.dashboard, err = startAgentDashboardListener(ownerCtx, o.server, dashboard.Addr().String(), dashboard)
	if err != nil {
		_ = dashboard.Close()
		return nil, err
	}
	o.client = localagent.NewClient(paths.Socket)
	o.runDone = make(chan struct{})
	go func() {
		defer close(o.runDone)
		o.failure <- o.server.Run(ownerCtx)
	}()
	readyCtx, readyCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readyCancel()
	for {
		health, err := o.client.Health(readyCtx)
		if err == nil {
			if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
				return nil, err
			}
			break
		}
		select {
		case err := <-o.failure:
			return nil, fmt.Errorf("worktree control stopped during startup: %w", err)
		case <-readyCtx.Done():
			return nil, fmt.Errorf("worktree control did not become ready: %w", readyCtx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	success = true
	return o, nil
}

func existingWorktreeRuntime(ctx context.Context, paths localagent.WorktreePaths, appID, environment string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	for {
		held, err := paths.ProbeLiveLock()
		if err != nil {
			return err
		}
		if !held {
			return &codedCLIError{code: 3, err: fmt.Errorf("worktree owner exited before acquisition completed; retry up")}
		}
		_, recordErr := paths.LoadRecord(appID)
		if recordErr != nil && !errors.Is(recordErr, os.ErrNotExist) {
			return recordErr
		}
		if recordErr == nil {
			health, healthErr := client.Health(ctx)
			if healthErr == nil {
				if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
					return err
				}
				sessions, err := client.List(ctx, paths.AppRoot)
				if err != nil {
					return err
				}
				for _, session := range sessions {
					if _, live := sessionOwnerProcessLive(session); !live {
						continue
					}
					if session.Environment != environment {
						return &codedCLIError{code: 3, err: fmt.Errorf("worktree already runs environment %q; stop it before selecting %q", session.Environment, environment)}
					}
					return &devSessionAlreadyRunningError{root: paths.AppRoot, ownerPID: health.PID, session: session}
				}
			}
		}
		// The lifetime lock precedes the first record, listener and session.
		// Wait for that startup interval without allocating or stealing state.
		select {
		case <-ctx.Done():
			return &codedCLIError{code: 3, err: fmt.Errorf("worktree ownership is busy but registration is unavailable; no process was replaced: %w", ctx.Err())}
		case <-time.After(25 * time.Millisecond):
		}
	}
}

func bindWorktreeBrowser(previous, root string, env app.ResolvedEnv) (net.Listener, error) {
	start, end, err := normalizeDevPortRange(env.PortStart, env.PortEnd)
	if err != nil {
		return nil, err
	}
	bind := func(port int) (net.Listener, error) {
		return net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}
	linked := isLinkedGitWorktree(root)
	if env.Port != 0 && !linked {
		listener, err := bind(env.Port)
		if err != nil {
			return nil, &codedCLIError{code: 3, err: fmt.Errorf("explicit worktree browser port %d is unavailable: %w", env.Port, err)}
		}
		return listener, nil
	}
	if _, portText, err := net.SplitHostPort(previous); err == nil {
		if port, err := strconv.Atoi(portText); err == nil && port >= start && port <= end && (!linked || port != env.Port) {
			if listener, err := bind(port); err == nil {
				return listener, nil
			}
		}
	}
	preferred, err := preferredDevPort(root, start, end)
	if err != nil {
		return nil, err
	}
	for i := 0; i <= end-start; i++ {
		port := start + (preferred-start+i)%(end-start+1)
		if linked && port == env.Port {
			continue
		}
		if listener, err := bind(port); err == nil {
			return listener, nil
		}
	}
	return nil, &codedCLIError{code: 3, err: fmt.Errorf("no worktree browser port is available in %d-%d", start, end)}
}
