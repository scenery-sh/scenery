package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
	"scenery.sh/internal/envpolicy"
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
	AppRoot string
	JSON    bool
	Watch   bool
}

type downOptions struct {
	AppRoot string
	DB      bool
	State   bool
	All     bool
	JSON    bool
}

type downResponse struct {
	SchemaVersion      string   `json:"schema_version"`
	SessionID          string   `json:"session_id"`
	AppRoot            string   `json:"app_root,omitempty"`
	Deleted            bool     `json:"deleted"`
	RecordPreserved    bool     `json:"record_preserved"`
	DBCleanup          bool     `json:"db_cleanup"`
	StateCleanup       bool     `json:"state_cleanup"`
	StateRootRemoved   string   `json:"state_root_removed,omitempty"`
	DBBranchPinRemoved bool     `json:"db_branch_pin_removed"`
	Messages           []string `json:"messages,omitempty"`
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
			return fmt.Errorf("stop scenery agent pid %d: %w", oldHealth.PID, err)
		}
		if err := waitForAgentStop(ctx, client, oldHealth.PID); err != nil {
			return err
		}
	}
	logOffset := fileSize(paths.LogPath)
	if err := localagent.StartProcess(paths, localagent.StartOptions{
		RouterAddr: opts.RouterAddr,
		RouterTLS:  opts.effectiveRouterTLS(),
		RouterHTTP: opts.RouterHTTP,
		Trust:      opts.Trust,
	}); err != nil {
		return err
	}
	health, err := waitForAgentStart(ctx, client, oldHealth.PID, paths.LogPath, logOffset)
	if err != nil {
		return err
	}
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": "scenery.agent.restart.v1",
			"old_pid":        oldHealth.PID,
			"pid":            health.PID,
			"socket_path":    health.SocketPath,
			"router_addr":    health.RouterAddr,
			"router_scheme":  health.RouterScheme,
		})
	}
	fmt.Fprintf(os.Stdout, "restarted scenery agent")
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
			return fmt.Errorf("timed out waiting for scenery agent pid %d to stop: %w", pid, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForAgentStart(ctx context.Context, client *localagent.Client, oldPID int, logPath string, logOffset int64) (localagent.HealthResponse, error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		health, err := client.Health(ctx)
		if err == nil && (oldPID == 0 || health.PID != oldPID) {
			return health, nil
		}
		lastErr = err
		if failure := agentStartFailureFromLog(logPath, logOffset); failure != nil {
			return localagent.HealthResponse{}, failure
		}
		select {
		case <-ctx.Done():
			if failure := agentStartFailureFromLog(logPath, logOffset); failure != nil {
				return localagent.HealthResponse{}, failure
			}
			if lastErr == nil {
				lastErr = ctx.Err()
			}
			return localagent.HealthResponse{}, fmt.Errorf("timed out waiting for restarted scenery agent: %w", lastErr)
		case <-ticker.C:
		}
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func agentStartFailureFromLog(path string, offset int64) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil
		}
	}
	data, err := io.ReadAll(io.LimitReader(file, 64<<10))
	if err != nil || len(data) == 0 {
		return nil
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.Contains(line, "listen scenery agent router") {
			return fmt.Errorf("restarted scenery agent failed to start: %s", line)
		}
		if strings.Contains(line, "permission denied") {
			return fmt.Errorf("restarted scenery agent failed to start: %s", line)
		}
	}
	return nil
}

func statusCommand(args []string) error {
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	return statusCommandWithClient(client, os.Stdout, args)
}

func statusCommandWithClient(client *localagent.Client, stdout io.Writer, args []string) error {
	opts, err := parseStatusArgs(args)
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
		if err := writeStatus(ctx, client, stdout, appRoot, opts); err != nil {
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
			return statusOptions{}, fmt.Errorf("scenery ps no longer accepts --session; use --app-root to inspect an app directory")
		default:
			return statusOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func writeStatus(ctx context.Context, client *localagent.Client, stdout io.Writer, appRoot string, opts statusOptions) error {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return err
	}
	sessions = markInconsistentStatusSessions(sessions)
	if opts.JSON {
		health, _ := client.Health(ctx)
		substrates, _ := client.ListSubstrates(ctx)
		enc := json.NewEncoder(stdout)
		if !opts.Watch {
			enc.SetIndent("", "  ")
		}
		return enc.Encode(map[string]any{
			"schema_version": "scenery.agent.status.v1",
			"agent":          health,
			"sessions":       sessions,
			"substrates":     substrates,
		})
	}
	writeStatusTable(stdout, sessions)
	if opts.Watch {
		fmt.Fprintln(stdout, "---")
	}
	return nil
}

func markInconsistentStatusSessions(sessions []localagent.Session) []localagent.Session {
	out := append([]localagent.Session(nil), sessions...)
	for i := range out {
		if !sessionStatusHealthy(out[i].Status) {
			continue
		}
		status, reason := classifySessionStatus(out[i])
		if status != "" {
			out[i].Status = status
			out[i].StatusReason = reason
		}
	}
	return out
}

func sessionStatusHealthy(status string) bool {
	switch strings.TrimSpace(status) {
	case "starting", "running":
		return true
	default:
		return false
	}
}

func classifySessionStatus(session localagent.Session) (string, string) {
	if status, reason := classifySessionOwnerStatus(session); status != "" {
		return status, reason
	}
	if session.AppPID != "" {
		pid := atoiPID(session.AppPID)
		if pid <= 0 {
			return "degraded", "app pid is invalid"
		}
		if _, ok := inspectProcess(pid); !ok {
			return "degraded", fmt.Sprintf("app process %d is not running", pid)
		}
	}
	if status, reason := classifyConfiguredEdgeRoutesStatus(session); status != "" {
		return status, reason
	}
	return "", ""
}

func classifyConfiguredEdgeRoutesStatus(session localagent.Session) (string, string) {
	baseDomain := normalizeRouteNamespaceHost(session.RouteNamespace.BaseDomain)
	if baseDomain == "" || baseDomain == localagent.DefaultRouteBaseDomain {
		return "", ""
	}
	for route, raw := range session.Routes {
		parsed, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || parsed.Host == "" {
			continue
		}
		port := parsed.Port()
		if parsed.Scheme == "https" && port != "" && port != "443" {
			return "degraded", fmt.Sprintf("configured route base domain %s requires edge, but route %s uses internal/diagnostic router port %s; run `scenery system edge status`", baseDomain, route, port)
		}
	}
	return "", ""
}

func classifySessionOwnerStatus(session localagent.Session) (string, string) {
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return "stale", "owner pid is missing"
	}
	owner := session.Owner
	if owner.PID != ownerPID {
		owner = localagent.CaptureOwner(ownerPID, "scenery up")
	}
	if owner.PID <= 0 {
		owner.PID = ownerPID
	}
	err := localagent.VerifyOwner(owner)
	if err == nil {
		return "", ""
	}
	if _, ok := inspectProcess(ownerPID); ok {
		return "degraded", "owner fingerprint mismatch: " + err.Error()
	}
	return "stale", "owner process is not running: " + err.Error()
}

func sessionOwnerLive(session localagent.Session) bool {
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return false
	}
	owner := session.Owner
	if owner.PID != ownerPID {
		owner = localagent.CaptureOwner(ownerPID, "scenery up")
	} else if owner.PID <= 0 {
		owner.PID = ownerPID
	}
	return localagent.VerifyOwner(owner) == nil
}

func downCommand(args []string) error {
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	return downCommandWithClient(client, os.Stdout, args)
}

func downCommandWithClient(client *localagent.Client, stdout io.Writer, args []string) error {
	opts, err := parseDownArgs(args)
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
	appRoot := session.AppRoot
	if strings.TrimSpace(appRoot) == "" {
		appRoot, _ = resolveStatusAppRoot(opts.AppRoot)
	}
	if err := stopDeletedSessionProcesses(ctx, session); err != nil {
		return err
	}
	deletedSession, deleted, err := deleteStoppedSessionRecord(ctx, client, session)
	if err != nil {
		return err
	}
	resp := downResponse{
		SchemaVersion: "scenery.down.v1",
		SessionID:     firstNonEmpty(deletedSession.SessionID, session.SessionID),
		AppRoot:       appRoot,
		Deleted:       deleted,
		DBCleanup:     opts.DB,
		StateCleanup:  opts.State,
	}
	runtimeLabel := firstNonEmpty(appRoot, deletedSession.AppRoot, session.AppRoot, deletedSession.SessionID, session.SessionID)
	if !deleted {
		resp.RecordPreserved = true
		resp.Messages = append(resp.Messages, fmt.Sprintf("stopped scenery dev runtime processes for %s; preserved active runtime record because the owner changed", runtimeLabel))
		if opts.JSON {
			return writeDownJSON(stdout, resp)
		}
		fmt.Fprintln(stdout, resp.Messages[0])
		return nil
	}
	if opts.DB {
		message, err := dropSessionManagedDatabase(ctx, client, appRoot, deletedSession)
		if err != nil {
			return err
		}
		resp.Messages = append(resp.Messages, message)
		if !opts.JSON {
			fmt.Fprintln(stdout, message)
		}
	}
	if opts.State {
		if err := removeSessionStateRoot(deletedSession); err != nil {
			return err
		}
		resp.StateRootRemoved = deletedSession.StateRoot
		stateMessage := fmt.Sprintf("removed scenery dev runtime state %s", deletedSession.StateRoot)
		resp.Messages = append(resp.Messages, stateMessage)
		if !opts.JSON {
			fmt.Fprintln(stdout, stateMessage)
		}
		removedPin, err := removeDBWorktreeDBPinForSession(appRoot, deletedSession)
		if err != nil {
			return err
		}
		if removedPin {
			resp.DBBranchPinRemoved = true
			pinMessage := fmt.Sprintf("removed scenery database branch pin for dev runtime %s", runtimeLabel)
			resp.Messages = append(resp.Messages, pinMessage)
			if !opts.JSON {
				fmt.Fprintln(stdout, pinMessage)
			}
		}
	}
	stopMessage := fmt.Sprintf("stopped scenery dev runtime for %s", runtimeLabel)
	resp.Messages = append(resp.Messages, stopMessage)
	if opts.JSON {
		return writeDownJSON(stdout, resp)
	}
	fmt.Fprintln(stdout, stopMessage)
	return nil
}

func writeDownJSON(w io.Writer, resp downResponse) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(resp)
}

func deleteStoppedSessionRecord(ctx context.Context, client *localagent.Client, session localagent.Session) (localagent.Session, bool, error) {
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		deletedSession, deleted, err := client.DeleteUnowned(ctx, session.SessionID)
		if err == nil {
			if !deleted {
				return session, false, nil
			}
			if strings.TrimSpace(deletedSession.SessionID) == "" {
				return session, true, nil
			}
			return deletedSession, true, nil
		}
		if localagent.IsNotFound(err) {
			return session, true, nil
		}
		return localagent.Session{}, false, err
	}
	deletedSession, deleted, err := client.DeleteOwnedSession(ctx, session, false)
	if err == nil {
		if !deleted {
			return session, false, nil
		}
		if strings.TrimSpace(deletedSession.SessionID) == "" {
			return session, true, nil
		}
		return deletedSession, true, nil
	}
	if localagent.IsNotFound(err) {
		return session, true, nil
	}
	return localagent.Session{}, false, err
}

func stopDeletedSessionProcesses(ctx context.Context, session localagent.Session) error {
	var errs []error
	seen := map[int]bool{}
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID > 0 && ownerPID != os.Getpid() && shouldSignalSessionOwner(session) {
		errs = append(errs, stopSessionOwnerPID(ctx, ownerPID))
		seen[ownerPID] = true
	}
	for _, pid := range sessionProcessPIDs(session) {
		if pid <= 0 || pid == os.Getpid() || seen[pid] {
			continue
		}
		if err := stopStaleSessionChildPID(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	errs = append(errs, stopSessionCommandProcesses(ctx, session, seen))
	errs = append(errs, stopSessionEnvProcesses(ctx, session, seen))
	return errors.Join(errs...)
}

func resolveDownSession(ctx context.Context, client *localagent.Client, opts downOptions) (localagent.Session, error) {
	appRoot, err := resolveStatusAppRoot(opts.AppRoot)
	if err != nil {
		return localagent.Session{}, err
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return localagent.Session{}, err
	}
	if len(sessions) == 0 {
		return localagent.Session{}, fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
	}
	return sessions[0], nil
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
			return downOptions{}, fmt.Errorf("scenery down no longer accepts --session; use --app-root to stop an app directory's dev runtime")
		case "--db":
			opts.DB = true
		case "--state":
			opts.State = true
		case "--all":
			opts.All = true
		case "--json":
			opts.JSON = true
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
	if !strings.Contains(clean, string(filepath.Separator)+".scenery"+string(filepath.Separator)+"sessions"+string(filepath.Separator)) {
		return fmt.Errorf("refusing to remove unexpected session state path %s", clean)
	}
	return os.RemoveAll(clean)
}

func pruneCommand(args []string) error {
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	return pruneCommandWithDeps(client, os.Stdout, openDevdashStore, args)
}

func pruneCommandWithDeps(client *localagent.Client, stdout io.Writer, openStore func() (*devdash.Store, error), args []string) error {
	opts, err := parsePruneArgs(args)
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
	devEventsPruned := int64(0)
	devSourcesPruned := int64(0)
	store, storeErr := openStore()
	if storeErr == nil {
		defer store.Close()
	}
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
		if store != nil {
			events, sources, err := store.DeleteDevEventsForSession(ctx, deleted.BaseAppID, deleted.SessionID)
			if err != nil {
				return err
			}
			devEventsPruned += events
			devSourcesPruned += sources
		}
		pruned = append(pruned, deleted.SessionID)
	}
	if opts.JSON {
		return json.NewEncoder(stdout).Encode(map[string]any{
			"cutoff":             cutoff.Format(time.RFC3339Nano),
			"pruned":             pruned,
			"skipped":            skipped,
			"dev_events_pruned":  devEventsPruned,
			"dev_sources_pruned": devSourcesPruned,
		})
	}
	for _, id := range pruned {
		fmt.Fprintf(stdout, "pruned scenery session %s\n", id)
	}
	fmt.Fprintf(stdout, "scenery prune complete: pruned=%d skipped=%d dev_events=%d dev_sources=%d\n", len(pruned), len(skipped), devEventsPruned, devSourcesPruned)
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
	if sessionOwnerLive(session) {
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

func dropSessionManagedDatabase(ctx context.Context, client *localagent.Client, appRoot string, session localagent.Session) (string, error) {
	if strings.TrimSpace(appRoot) == "" {
		return "", fmt.Errorf("app root is required to drop a managed database")
	}
	_, cfg, err := app.DiscoverRoot(appRoot)
	if err != nil {
		return "", err
	}
	if appConfigUsesBranchingPostgres(cfg) {
		branch, removed, err := removeDBBranchLeaseForSession(appRoot, session)
		if err != nil {
			return "", err
		}
		if !removed {
			return "no local database branch lease to remove for this dev runtime", nil
		}
		return fmt.Sprintf("removed local database branch lease %s for this dev runtime", branch), nil
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return "", err
	}
	if client != nil {
		baseEnv, err = envWithManagedPostgresAdminURL(ctx, cfg, baseEnv, client)
		if err != nil {
			return "", err
		}
	}
	plan, err := resolveManagedPostgresPlan(cfg, &session, baseEnv)
	if err != nil {
		return "", err
	}
	if plan == nil {
		return "", fmt.Errorf("dev.services.postgres is not configured")
	}
	if err := dropManagedPostgresDatabase(ctx, plan.AdminURL, plan.DatabaseName); err != nil {
		return "", err
	}
	return "dropped scenery managed database for this dev runtime", nil
}

func removeDBWorktreeDBPinForSession(appRoot string, session localagent.Session) (bool, error) {
	if strings.TrimSpace(appRoot) == "" {
		return false, nil
	}
	_, cfg, err := app.DiscoverRoot(appRoot)
	if err != nil {
		if errors.Is(err, app.ErrRootNotFound) {
			return false, nil
		}
		return false, err
	}
	if !appConfigUsesBranchingPostgres(cfg) {
		return false, nil
	}
	path := worktreeDBPinPath(appRoot)
	pin, ok, err := readWorktreeDBPin(path)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	if sessionID := strings.TrimSpace(session.SessionID); sessionID != "" {
		if strings.TrimSpace(pin.SessionID) != "" {
			if pin.SessionID != sessionID {
				return false, nil
			}
		} else if firstNonEmpty(strings.TrimSpace(dbPostgresService(cfg).BranchPolicy), dbBranchDefaultPolicy) == "session" {
			branch, _, err := deriveDBBranchName(appRoot, cfg, &session)
			if err != nil {
				return false, err
			}
			expected, err := buildWorktreeDBPinForSession(appRoot, cfg, &session, branch)
			if err != nil {
				return false, err
			}
			if !sameDBBranchLease(pin, expected) && !sameDBBranch(pin, expected) {
				return false, nil
			}
		}
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func removeDBBranchLeaseForSession(appRoot string, session localagent.Session) (string, bool, error) {
	if strings.TrimSpace(appRoot) == "" {
		return "", false, nil
	}
	_, cfg, err := app.DiscoverRoot(appRoot)
	if err != nil {
		if errors.Is(err, app.ErrRootNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if !appConfigUsesBranchingPostgres(cfg) {
		return "", false, nil
	}
	pin, ok, err := readWorktreeDBPin(worktreeDBPinPath(appRoot))
	if err != nil || !ok {
		return "", false, err
	}
	if sessionID := strings.TrimSpace(session.SessionID); sessionID != "" && strings.TrimSpace(pin.SessionID) != "" && pin.SessionID != sessionID {
		return "", false, nil
	}
	branch, removed, err := removeCurrentDBBranchLease(appRoot, cfg)
	if err != nil || !removed {
		return branch, removed, err
	}
	_ = os.Remove(worktreeDBPinPath(appRoot))
	return branch, true, nil
}

func appConfigUsesBranchingPostgres(cfg app.Config) bool {
	_, svc, ok := managedPostgresDeclared(cfg)
	return ok && postgresServiceUsesBranching(svc)
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
