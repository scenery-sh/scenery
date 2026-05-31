package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

type agentOptions struct {
	SocketPath string
	RouterAddr string
	RouterTLS  bool
	RouterHTTP bool
	Trust      bool
	JSON       bool
}

type statusOptions struct {
	AppRoot   string
	SessionID string
	JSON      bool
	Watch     bool
}

type downOptions struct {
	AppRoot   string
	SessionID string
	DB        bool
	State     bool
	All       bool
}

type pruneOptions struct {
	AppRoot   string
	OlderThan time.Duration
	JSON      bool
}

func agentCommand(args []string) error {
	if len(args) > 0 && args[0] == "restart" {
		return agentRestartCommand(args[1:])
	}
	opts, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.JSON {
		paths, err := localagent.DefaultPaths()
		if err != nil {
			return err
		}
		if opts.SocketPath != "" {
			paths.SocketPath = opts.SocketPath
		}
		routerTLS := opts.effectiveRouterTLS()
		routerScheme := "http"
		if routerTLS {
			routerScheme = "https"
		}
		fmt.Fprintf(os.Stdout, "{\"type\":\"agent.start\",\"socket_path\":%q,\"router_addr\":%q,\"router_scheme\":%q}\n", paths.SocketPath, firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv()), routerScheme)
	}
	dashboardAddr, err := freeLoopbackAddr()
	if err != nil {
		return err
	}
	server, err := localagent.NewServer(localagent.RunOptions{
		SocketPath:   opts.SocketPath,
		RouterAddr:   opts.RouterAddr,
		RouterTLS:    opts.effectiveRouterTLS(),
		InstallTrust: opts.Trust,
		DashboardBackend: localagent.Backend{
			Network: "tcp",
			Addr:    dashboardAddr,
		},
		JSON: opts.JSON,
	})
	if err != nil {
		return err
	}
	dashboard, err := startAgentDashboard(ctx, server, dashboardAddr)
	if err != nil {
		_ = server.Close()
		return err
	}
	defer dashboard.Close()
	return server.Run(ctx)
}

func agentRestartCommand(args []string) error {
	opts, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if opts.SocketPath != "" {
		paths.SocketPath = filepath.Clean(opts.SocketPath)
		paths.RunDir = filepath.Dir(paths.SocketPath)
	}
	client := localagent.NewClient(paths.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	oldHealth, running := currentAgentHealth(ctx, client)
	if running && oldHealth.PID > 0 {
		if err := signalAgentPID(oldHealth.PID); err != nil {
			return fmt.Errorf("stop onlava agent pid %d: %w", oldHealth.PID, err)
		}
		if err := waitForAgentStop(ctx, client, oldHealth.PID); err != nil {
			return err
		}
	}
	if err := localagent.StartProcess(paths, localagent.StartOptions{
		RouterAddr: opts.RouterAddr,
		RouterTLS:  opts.effectiveRouterTLS(),
		RouterHTTP: opts.RouterHTTP,
		Trust:      opts.Trust,
	}); err != nil {
		return err
	}
	health, err := waitForAgentStart(ctx, client, oldHealth.PID)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": "onlava.agent.restart.v1",
			"old_pid":        oldHealth.PID,
			"pid":            health.PID,
			"socket_path":    health.SocketPath,
			"router_addr":    health.RouterAddr,
			"router_scheme":  health.RouterScheme,
		})
	}
	fmt.Fprintf(os.Stdout, "restarted onlava agent")
	if health.PID > 0 {
		fmt.Fprintf(os.Stdout, " (pid %d)", health.PID)
	}
	fmt.Fprintln(os.Stdout)
	return nil
}

func parseAgentArgs(args []string) (agentOptions, error) {
	var opts agentOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			i++
			if i >= len(args) {
				return agentOptions{}, fmt.Errorf("missing value for --socket")
			}
			opts.SocketPath = args[i]
		case "--router-listen":
			i++
			if i >= len(args) {
				return agentOptions{}, fmt.Errorf("missing value for --router-listen")
			}
			opts.RouterAddr = args[i]
		case "--router-tls":
			opts.RouterTLS = true
			opts.RouterHTTP = false
		case "--router-http":
			opts.RouterHTTP = true
			opts.RouterTLS = false
		case "--trust":
			opts.Trust = true
			opts.RouterTLS = true
			opts.RouterHTTP = false
		case "--json":
			opts.JSON = true
		default:
			return agentOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func (opts agentOptions) effectiveRouterTLS() bool {
	if opts.Trust || opts.RouterTLS {
		return true
	}
	if opts.RouterHTTP {
		return false
	}
	return localagent.RouterTLSDefault()
}

func currentAgentHealth(ctx context.Context, client *localagent.Client) (localagent.HealthResponse, bool) {
	health, err := client.Health(ctx)
	return health, err == nil
}

func signalAgentPID(pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(syscall.SIGTERM); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func waitForAgentStop(ctx context.Context, client *localagent.Client, pid int) error {
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		health, err := client.Health(ctx)
		if err != nil || health.PID != pid {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timed out waiting for onlava agent pid %d to stop: %w", pid, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForAgentStart(ctx context.Context, client *localagent.Client, oldPID int) (localagent.HealthResponse, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		health, err := client.Health(ctx)
		if err == nil && (oldPID == 0 || health.PID != oldPID) {
			return health, nil
		}
		lastErr = err
		select {
		case <-ctx.Done():
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return localagent.HealthResponse{}, fmt.Errorf("timed out waiting for restarted onlava agent: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func statusCommand(args []string) error {
	opts, err := parseStatusArgs(args)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	appRoot := ""
	if opts.AppRoot != "" {
		appRoot, err = resolveStatusAppRoot(opts.AppRoot)
		if err != nil {
			return err
		}
	}
	for {
		if err := writeStatus(ctx, client, appRoot, opts); err != nil {
			return err
		}
		if !opts.Watch {
			return nil
		}
		time.Sleep(time.Second)
	}
}

func parseStatusArgs(args []string) (statusOptions, error) {
	opts := statusOptions{JSON: false}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--watch":
			opts.Watch = true
		case "--app-root":
			i++
			if i >= len(args) {
				return statusOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			i++
			if i >= len(args) {
				return statusOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.SessionID = args[i]
		default:
			return statusOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func writeStatus(ctx context.Context, client *localagent.Client, appRoot string, opts statusOptions) error {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return err
	}
	if opts.SessionID != "" {
		filtered := sessions[:0]
		for _, session := range sessions {
			if session.SessionID == opts.SessionID {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	if opts.JSON {
		health, _ := client.Health(ctx)
		enc := json.NewEncoder(os.Stdout)
		if !opts.Watch {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(map[string]any{
			"schema_version": "onlava.agent.status.v1",
			"agent":          health,
			"sessions":       sessions,
		})
	}
	for _, session := range sessions {
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", session.SessionID, session.Status, session.AppRoot)
	}
	if opts.Watch {
		fmt.Fprintln(os.Stdout, "---")
	}
	return nil
}

func downCommand(args []string) error {
	opts, err := parseDownArgs(args)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	session, err := resolveDownSession(ctx, client, opts)
	if err != nil {
		return err
	}
	if opts.All {
		opts.DB = true
		opts.State = true
	}
	sessionID := session.SessionID
	appRoot := session.AppRoot
	if strings.TrimSpace(appRoot) == "" {
		appRoot, _ = resolveStatusAppRoot(opts.AppRoot)
	}
	deletedSession, err := client.Delete(ctx, sessionID, true)
	if err != nil {
		return err
	}
	if opts.DB {
		if err := dropSessionManagedDatabase(ctx, appRoot, deletedSession); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "dropped onlava managed database for session %s\n", deletedSession.SessionID)
	}
	if opts.State {
		if err := removeSessionStateRoot(deletedSession); err != nil {
			return err
		}
		fmt.Fprintf(os.Stdout, "removed onlava session state %s\n", deletedSession.StateRoot)
	}
	fmt.Fprintf(os.Stdout, "stopped onlava session %s\n", deletedSession.SessionID)
	return nil
}

func resolveDownSession(ctx context.Context, client *localagent.Client, opts downOptions) (localagent.Session, error) {
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		appRoot, err := resolveStatusAppRoot(opts.AppRoot)
		if err != nil {
			return localagent.Session{}, err
		}
		sessions, err := client.List(ctx, appRoot)
		if err != nil {
			return localagent.Session{}, err
		}
		if len(sessions) == 0 {
			return localagent.Session{}, fmt.Errorf("no onlava agent session found for %s", appRoot)
		}
		return sessions[0], nil
	}
	sessions, err := client.List(ctx, "")
	if err != nil {
		return localagent.Session{}, err
	}
	for _, session := range sessions {
		if session.SessionID == sessionID {
			return session, nil
		}
	}
	return localagent.Session{}, fmt.Errorf("no onlava agent session found for %s", sessionID)
}

func parseDownArgs(args []string) (downOptions, error) {
	var opts downOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return downOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			i++
			if i >= len(args) {
				return downOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.SessionID = args[i]
		case "--db":
			opts.DB = true
		case "--state":
			opts.State = true
		case "--all":
			opts.All = true
		default:
			return downOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func removeSessionStateRoot(session localagent.Session) error {
	if strings.TrimSpace(session.StateRoot) == "" {
		return nil
	}
	clean := filepath.Clean(session.StateRoot)
	if !strings.Contains(clean, string(filepath.Separator)+".onlava"+string(filepath.Separator)+"sessions"+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove unexpected session state path %s", clean)
	}
	return os.RemoveAll(clean)
}

func pruneCommand(args []string) error {
	opts, err := parsePruneArgs(args)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	appRoot := strings.TrimSpace(opts.AppRoot)
	if appRoot != "" {
		resolved, err := resolveStatusAppRoot(appRoot)
		if err != nil {
			return err
		}
		appRoot = resolved
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return err
	}
	cutoff := time.Now().UTC().Add(-opts.OlderThan)
	var pruned []string
	var skipped []string
	for _, session := range sessions {
		if !pruneSessionEligible(session, cutoff) {
			skipped = append(skipped, session.SessionID)
			continue
		}
		deleted, err := client.Delete(ctx, session.SessionID, false)
		if err != nil {
			return err
		}
		if err := removeSessionStateRoot(deleted); err != nil {
			return err
		}
		pruned = append(pruned, deleted.SessionID)
	}
	if opts.JSON {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"cutoff":  cutoff.Format(time.RFC3339Nano),
			"pruned":  pruned,
			"skipped": skipped,
		})
	}
	for _, id := range pruned {
		fmt.Fprintf(os.Stdout, "pruned onlava session %s\n", id)
	}
	fmt.Fprintf(os.Stdout, "onlava prune complete: pruned=%d skipped=%d\n", len(pruned), len(skipped))
	return nil
}

func parsePruneArgs(args []string) (pruneOptions, error) {
	var opts pruneOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return pruneOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--older-than":
			i++
			if i >= len(args) {
				return pruneOptions{}, fmt.Errorf("missing value for --older-than")
			}
			duration, err := parsePruneAge(args[i])
			if err != nil {
				return pruneOptions{}, err
			}
			opts.OlderThan = duration
		case "--json":
			opts.JSON = true
		default:
			return pruneOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	if opts.OlderThan <= 0 {
		return pruneOptions{}, fmt.Errorf("prune requires --older-than <duration>")
	}
	return opts, nil
}

func parsePruneAge(value string) (time.Duration, error) {
	value = strings.TrimSpace(value)
	if strings.HasSuffix(value, "d") {
		days, err := strconv.Atoi(strings.TrimSuffix(value, "d"))
		if err != nil || days <= 0 {
			return 0, fmt.Errorf("invalid --older-than duration %q", value)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return 0, fmt.Errorf("invalid --older-than duration %q", value)
	}
	return duration, nil
}

func pruneSessionEligible(session localagent.Session, cutoff time.Time) bool {
	if session.UpdatedAt.IsZero() || session.UpdatedAt.After(cutoff) {
		return false
	}
	owner := session.Owner
	if owner.PID <= 0 {
		owner.PID = session.OwnerPID
	}
	if owner.PID <= 0 {
		return true
	}
	err := localagent.VerifyOwner(owner)
	if err == nil {
		return false
	}
	if strings.Contains(err.Error(), "fingerprint is missing") {
		return false
	}
	return true
}

func dropSessionManagedDatabase(ctx context.Context, appRoot string, session localagent.Session) error {
	if strings.TrimSpace(appRoot) == "" {
		return fmt.Errorf("app root is required to drop a managed database")
	}
	_, cfg, err := app.DiscoverRoot(appRoot)
	if err != nil {
		return err
	}
	baseEnv, err := appEnvWithDotEnv(os.Environ(), appRoot)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err == nil {
		baseEnv, err = envWithManagedPostgresAdminURL(ctx, cfg, baseEnv, client)
		if err != nil {
			return err
		}
	}
	plan, err := resolveManagedPostgresPlan(cfg, &session, baseEnv)
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("dev.services.postgres is not configured")
	}
	return dropManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName)
}

func resolveStatusAppRoot(value string) (string, error) {
	start := strings.TrimSpace(value)
	if start == "" {
		start = "."
	}
	root, _, err := app.DiscoverRoot(start)
	if err == nil {
		return root, nil
	}
	if value != "" {
		abs, absErr := filepath.Abs(value)
		if absErr != nil {
			return "", errors.Join(err, absErr)
		}
		return filepath.Clean(abs), nil
	}
	return "", err
}
