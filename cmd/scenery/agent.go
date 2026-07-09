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
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/devdash"
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
	SchemaVersion    string   `json:"schema_version"`
	SessionID        string   `json:"session_id"`
	AppRoot          string   `json:"app_root,omitempty"`
	Deleted          bool     `json:"deleted"`
	RecordPreserved  bool     `json:"record_preserved"`
	DBCleanup        bool     `json:"db_cleanup"`
	StateCleanup     bool     `json:"state_cleanup"`
	StateRootRemoved string   `json:"state_root_removed,omitempty"`
	Messages         []string `json:"messages,omitempty"`
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
		Identity: cliBuildIdentity(),
		JSON:     opts.JSON,
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
	flags := newCLIFlagSet("agent")
	flags.StringVar(&opts.SocketPath, "socket", "", "")
	flags.StringVar(&opts.RouterAddr, "router-listen", "", "")
	flags.BoolFunc("router-tls", "", func(string) error { opts.RouterTLS, opts.RouterHTTP = true, false; return nil })
	flags.BoolFunc("router-http", "", func(string) error { opts.RouterHTTP, opts.RouterTLS = true, false; return nil })
	flags.BoolFunc("trust", "", func(string) error { opts.Trust, opts.RouterTLS, opts.RouterHTTP = true, true, false; return nil })
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return agentOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return agentOptions{}, err
	}
	return opts, nil
}

func (opts agentOptions) effectiveRouterTLS() bool {
	return opts.Trust || opts.RouterTLS
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
	flags := newCLIFlagSet("ps")
	flags.BoolVar(&opts.JSON, "json", false, "")
	flags.BoolVar(&opts.Watch, "watch", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	rejectCLIFlag(flags, "session", "scenery ps no longer accepts --session; use --app-root to inspect an app directory")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return statusOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return statusOptions{}, err
	}
	return opts, nil
}

func writeStatus(ctx context.Context, client *localagent.Client, stdout io.Writer, appRoot string, opts statusOptions) error {
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return err
	}
	sessions = markInconsistentStatusSessions(sessions)
	substrates, _ := statusSubstrates(ctx, client)
	if opts.JSON {
		health, _ := client.Health(ctx)
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
	writeStatusTable(stdout, sessions, substrates)
	if opts.Watch {
		fmt.Fprintln(stdout, "---")
	}
	return nil
}

func statusSubstrates(ctx context.Context, client *localagent.Client) ([]localagent.Substrate, error) {
	substrates, err := client.ListSubstrates(ctx)
	if err != nil {
		return nil, err
	}
	out := substrates[:0]
	for _, substrate := range substrates {
		if localagent.VerifyOwner(substrate.Owner) != nil {
			_, _ = client.DeleteSubstrate(ctx, substrate.Kind)
			continue
		}
		out = append(out, substrate)
	}
	return out, nil
}

func markInconsistentStatusSessions(sessions []localagent.Session) []localagent.Session {
	out := append([]localagent.Session(nil), sessions...)
	for i := range out {
		out[i].Status, out[i].StatusReason = effectiveSessionStatus(out[i])
	}
	return out
}

func effectiveSessionStatus(session localagent.Session) (string, string) {
	status := strings.TrimSpace(session.Status)
	reason := strings.TrimSpace(session.StatusReason)
	if !sessionStatusHealthy(status) {
		return status, reason
	}
	if next, nextReason := classifySessionStatus(session); next != "" {
		return next, nextReason
	}
	return status, reason
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
	if status, reason := classifySessionRegisteredProcessStatus(session); status != "" {
		return status, reason
	}
	if status, reason := classifyConfiguredEdgeRoutesStatus(session); status != "" {
		return status, reason
	}
	return "", ""
}

func classifySessionRegisteredProcessStatus(session localagent.Session) (string, string) {
	if len(session.Processes) == 0 {
		return "", ""
	}
	names := make([]string, 0, len(session.Processes))
	for name := range session.Processes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		process := session.Processes[name]
		if process.PID <= 0 {
			return "degraded", fmt.Sprintf("registered process %s pid is invalid", name)
		}
		if _, ok := inspectProcess(process.PID); !ok {
			return "degraded", fmt.Sprintf("registered process %s pid %d is not running", name, process.PID)
		}
		if process.Owner.PID > 0 {
			if err := localagent.VerifyOwner(process.Owner); err != nil {
				return "degraded", fmt.Sprintf("registered process %s owner fingerprint mismatch: %v", name, err)
			}
		}
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
	if opts.All {
		opts.DB = true
		opts.State = true
	}
	session, runtimeMissing, err := resolveDownSession(ctx, client, opts)
	if err != nil {
		return err
	}
	appRoot := session.AppRoot
	if strings.TrimSpace(appRoot) == "" {
		appRoot, _ = resolveStatusAppRoot(opts.AppRoot)
	}
	if runtimeMissing {
		resp := downResponse{
			SchemaVersion: "scenery.down.v1",
			AppRoot:       appRoot,
			DBCleanup:     opts.DB,
			StateCleanup:  opts.State,
		}
		message := fmt.Sprintf("no scenery dev runtime found for app root %s; runtime stop skipped", appRoot)
		resp.Messages = append(resp.Messages, message)
		if !opts.JSON {
			fmt.Fprintln(stdout, message)
		}
		if opts.DB {
			dbMessage, err := dropSessionManagedDatabase(ctx, appRoot)
			if err != nil {
				return err
			}
			resp.Messages = append(resp.Messages, dbMessage)
			if !opts.JSON {
				fmt.Fprintln(stdout, dbMessage)
			}
		}
		if opts.State {
			stateMessage := "no scenery dev runtime state found to remove"
			resp.Messages = append(resp.Messages, stateMessage)
			if !opts.JSON {
				fmt.Fprintln(stdout, stateMessage)
			}
		}
		if opts.JSON {
			return writeDownJSON(stdout, resp)
		}
		return nil
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
		message, err := dropSessionManagedDatabase(ctx, appRoot)
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

func resolveDownSession(ctx context.Context, client *localagent.Client, opts downOptions) (localagent.Session, bool, error) {
	appRoot, err := resolveStatusAppRoot(opts.AppRoot)
	if err != nil {
		return localagent.Session{}, false, err
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return localagent.Session{}, false, err
	}
	return resolveDownSessionFromList(appRoot, sessions, opts)
}

func resolveDownSessionFromList(appRoot string, sessions []localagent.Session, opts downOptions) (localagent.Session, bool, error) {
	if len(sessions) == 0 {
		if opts.DB {
			return localagent.Session{AppRoot: appRoot}, true, nil
		}
		return localagent.Session{}, false, fmt.Errorf("no scenery dev runtime found for app root %s", appRoot)
	}
	return sessions[0], false, nil
}

func parseDownArgs(args []string) (downOptions, error) {
	var opts downOptions
	flags := newCLIFlagSet("down")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	rejectCLIFlag(flags, "session", "scenery down no longer accepts --session; use --app-root to stop an app directory's dev runtime")
	flags.BoolVar(&opts.DB, "db", false, "")
	flags.BoolVar(&opts.State, "state", false, "")
	flags.BoolVar(&opts.All, "all", false, "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return downOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return downOptions{}, err
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
	age := ""
	flags := newCLIFlagSet("prune")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&age, "older-than", "", "")
	flags.BoolVar(&opts.JSON, "json", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return pruneOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return pruneOptions{}, err
	}
	if age != "" {
		opts.OlderThan, err = parsePruneAge(age)
		if err != nil {
			return pruneOptions{}, err
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

func dropSessionManagedDatabase(ctx context.Context, appRoot string) (string, error) {
	if strings.TrimSpace(appRoot) == "" {
		return "", fmt.Errorf("app root is required to drop a managed database")
	}
	appRoot, cfg, err := discoverConfiguredApp(appRoot)
	if err != nil {
		return "", err
	}
	database, err := resolvePostgresDatabaseForCLI(ctx, appRoot, cfg)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(database.Database) == "" {
		return "no managed database is configured", nil
	}
	if err := dropPostgresDatabase(ctx, database, dbCLIOptions{}); err != nil {
		return "", err
	}
	return fmt.Sprintf("dropped managed postgres database %s", database.Database), nil
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
