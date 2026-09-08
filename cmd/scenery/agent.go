package main

import (
	"context"
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
)

type agentOptions struct {
	SocketPath string
	RouterAddr string
	RouterTLS  bool
	RouterHTTP bool
	Trust      bool
	JSON       bool
}

type agentCleanupOptions struct {
	RemoveState bool
	JSON        bool
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
	cliPayloadIdentity
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
	DB        bool
	State     bool
	All       bool
	JSON      bool
}

type pruneResponse struct {
	cliPayloadIdentity
	Cutoff           string                   `json:"cutoff"`
	Pruned           []string                 `json:"pruned"`
	Skipped          []string                 `json:"skipped"`
	DBCleanup        bool                     `json:"db_cleanup"`
	StateCleanup     bool                     `json:"state_cleanup"`
	DevEventsPruned  int64                    `json:"dev_events_pruned"`
	DevSourcesPruned int64                    `json:"dev_sources_pruned"`
	Resources        []worktreePrunedResource `json:"resources"`
}

func agentCommand(args []string) error {
	if len(args) > 0 && args[0] == "serve" {
		return runContractAgentServer(os.Stdin, os.Stdout, args)
	}
	if len(args) > 0 && args[0] == "restart" {
		return agentRestartCommand(args[1:])
	}
	if len(args) > 0 && args[0] == "cleanup" {
		return agentCleanupCommand(args[1:])
	}
	opts, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	if err := reapStaleAgentRouterOwner(opts); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.JSON {
		paths, err := commandAgentPaths()
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
		_, _ = fmt.Fprintf(os.Stdout, "{\"type\":\"agent.start\",\"socket_path\":%q,\"router_addr\":%q,\"router_scheme\":%q}\n", paths.SocketPath, firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv()), routerScheme)
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
	defer func() { _ = dashboard.Close() }()
	return server.Run(ctx)
}

func reapStaleAgentRouterOwner(opts agentOptions) error {
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	if opts.SocketPath != "" {
		paths.SocketPath = filepath.Clean(opts.SocketPath)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if localagent.NewClient(paths.SocketPath).Ping(ctx) == nil {
		return nil
	}
	return stopStaleUserSceneryAgents(paths.SocketPath, firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv()), 2*time.Second)
}

var (
	agentSupervisorStatusFunc    = localagent.AgentLaunchdStatusForSocket
	agentSupervisorKickstartFunc = localagent.KickstartAgentLaunchd
	agentSupervisorBootstrapFunc = localagent.BootstrapAgentLaunchd
)

func agentRestartCommand(args []string) error {
	opts, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	paths, err := commandAgentPaths()
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
	health, supervised, err := restartAgentViaSupervisor(ctx, client, paths, oldHealth, running)
	if err != nil {
		return err
	}
	if !supervised {
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
		health, err = waitForAgentStart(ctx, client, oldHealth.PID, paths.LogPath, logOffset)
		if err != nil {
			return err
		}
	}
	if opts.JSON {
		return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.agent.restart", map[string]any{
			"old_pid":       oldHealth.PID,
			"pid":           health.PID,
			"socket_path":   health.SocketPath,
			"router_addr":   health.RouterAddr,
			"router_scheme": health.RouterScheme,
			"supervised":    supervised,
		}))
	}
	_, _ = fmt.Fprintf(os.Stdout, "restarted scenery agent")
	if health.PID > 0 {
		_, _ = fmt.Fprintf(os.Stdout, " (pid %d)", health.PID)
	}
	if supervised {
		_, _ = fmt.Fprintf(os.Stdout, " under launchd supervisor %s", localagent.AgentLaunchdLabel)
	}
	_, _ = fmt.Fprintln(os.Stdout)
	return nil
}

// restartAgentViaSupervisor restarts the agent through launchd when the
// installed supervised plist manages this socket. Kickstart replaces the
// running agent atomically, so the restart cooperates with KeepAlive instead
// of racing it; a plist that exists but is not loaded is repaired by
// bootstrapping it. It returns supervised=false when no supervisor owns the
// socket, leaving the caller on the unsupervised stop/start path.
func restartAgentViaSupervisor(ctx context.Context, client *localagent.Client, paths localagent.Paths, oldHealth localagent.HealthResponse, running bool) (localagent.HealthResponse, bool, error) {
	status := agentSupervisorStatusFunc(paths.SocketPath)
	if !status.Supported || !status.PlistPresent || !status.SupervisesSocket {
		return localagent.HealthResponse{}, false, nil
	}
	logOffset := fileSize(paths.LogPath)
	// A reachable agent that is not the supervisor's own process (an
	// unsupervised agent, or one from before supervision was installed)
	// holds the agent lock and would make every supervised spawn fail
	// closed; stop it first so launchd's process can take ownership.
	if running && oldHealth.PID > 0 && oldHealth.PID != status.PID {
		if err := signalAgentPID(oldHealth.PID); err != nil {
			return localagent.HealthResponse{}, true, fmt.Errorf("stop scenery agent pid %d: %w", oldHealth.PID, err)
		}
		if err := waitForAgentStop(ctx, client, oldHealth.PID); err != nil {
			return localagent.HealthResponse{}, true, err
		}
	}
	if !status.Loaded {
		if err := agentSupervisorBootstrapFunc(); err != nil {
			return localagent.HealthResponse{}, true, err
		}
	} else if err := agentSupervisorKickstartFunc(true); err != nil {
		return localagent.HealthResponse{}, true, err
	}
	health, err := waitForAgentStart(ctx, client, oldHealth.PID, paths.LogPath, logOffset)
	return health, true, err
}

func parseAgentArgs(args []string) (agentOptions, error) {
	var opts agentOptions
	flags := newCLIFlagSet("agent")
	flags.StringVar(&opts.SocketPath, "socket", "", "")
	flags.StringVar(&opts.RouterAddr, "router-listen", "", "")
	flags.BoolFunc("router-tls", "", func(string) error { opts.RouterTLS, opts.RouterHTTP = true, false; return nil })
	flags.BoolFunc("router-http", "", func(string) error { opts.RouterHTTP, opts.RouterTLS = true, false; return nil })
	flags.BoolFunc("trust", "", func(string) error { opts.Trust, opts.RouterTLS, opts.RouterHTTP = true, true, false; return nil })
	registerJSONOutput(flags, &opts.JSON)
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
	defer func() { _ = file.Close() }()
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
	return runWorktreeStatus(context.Background(), os.Stdout, args)
}

func parseStatusArgs(args []string) (statusOptions, error) {
	opts := statusOptions{JSON: false}
	flags := newCLIFlagSet("ps")
	registerJSONOutput(flags, &opts.JSON)
	flags.BoolVar(&opts.Watch, "watch", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return statusOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return statusOptions{}, err
	}
	return opts, nil
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
	for route, record := range session.RouteManifest.Routes {
		raw := record.URL
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
	return runWorktreeDown(context.Background(), os.Stdout, args)
}

func writeDownJSON(w io.Writer, resp downResponse) error {
	return writeCLIJSON(w, resp)
}

func parseDownArgs(args []string) (downOptions, error) {
	var opts downOptions
	flags := newCLIFlagSet("down")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.BoolVar(&opts.DB, "db", false, "")
	flags.BoolVar(&opts.State, "state", false, "")
	flags.BoolVar(&opts.All, "all", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return downOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return downOptions{}, err
	}
	return opts, nil
}

func pruneCommand(args []string) error {
	return runWorktreePrune(context.Background(), os.Stdout, args)
}

func parsePruneArgs(args []string) (pruneOptions, error) {
	var opts pruneOptions
	age := ""
	flags := newCLIFlagSet("prune")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&age, "older-than", "", "")
	flags.BoolVar(&opts.DB, "db", false, "")
	flags.BoolVar(&opts.State, "state", false, "")
	flags.BoolVar(&opts.All, "all", false, "")
	registerJSONOutput(flags, &opts.JSON)
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
