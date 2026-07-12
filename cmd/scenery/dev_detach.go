package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

const (
	detachedDevChildEnv       = "SCENERY_DEV_DETACHED_CHILD"
	detachedDevStartupTimeout = 30 * time.Second
	detachedDevReadyTimeout   = 2 * time.Minute
	detachedDevWaitReady      = "ready"
	detachedDevWaitRegistered = "registered"
)

var detachedDevStartupInterval = 100 * time.Millisecond

var detachedDevBackendAcceptsConnections = backendAcceptsConnections

type detachedDevResult struct {
	SchemaVersion string             `json:"schema_version"`
	Wait          string             `json:"wait"`
	PID           int                `json:"pid"`
	LogPath       string             `json:"log_path"`
	AttachCommand string             `json:"attach_command"`
	DownCommand   string             `json:"down_command"`
	Session       localagent.Session `json:"session"`
}

func runDetachedDev(args []string, opts devOptions) error {
	waitMode, err := normalizeDetachedDevWaitMode(opts.Wait)
	if err != nil {
		return err
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}

	if localagent.DisabledByEnv() {
		return fmt.Errorf("scenery up --detach requires the local scenery agent; unset SCENERY_AGENT_DISABLE")
	}
	setupCtx, setupCancel := context.WithTimeout(context.Background(), detachedDevStartupTimeout)
	defer setupCancel()
	client, err := localagent.Ensure(setupCtx, cliBuildIdentity())
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("scenery up --detach requires the local scenery agent")
	}
	if err := rejectDetachedDuplicateDevSession(setupCtx, client, root, opts); err != nil {
		return err
	}

	paths, err := localagent.DefaultPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	logPath := detachedDevLogPath(paths, root, time.Now())
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer logFile.Close()

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	childArgs := append([]string{"up"}, devArgsForDetachedChild(args, root)...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Dir = root
	cmd.Env = append(envpolicy.Environ(), detachedDevChildEnv+"=1")
	cmd.Stdin = nil
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureDetachedChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	childPID := cmd.Process.Pid

	waitTimeout := detachedDevWaitTimeout(waitMode)
	waitCtx, waitCancel := context.WithTimeout(context.Background(), waitTimeout)
	defer waitCancel()
	session, err := waitForDetachedDevSession(waitCtx, client, root, childPID, waitMode, detachedDevExpectedFrontendRoutes(cfg.Frontends))
	if err != nil {
		_ = interruptProcessTree(cmd)
		_ = cmd.Process.Release()
		return fmt.Errorf("detached scenery up process %d did not reach %s within %s: %w; see %s", childPID, waitMode, waitTimeout, err, logPath)
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	return writeDetachedDevResult(os.Stdout, opts.JSON, detachedDevResult{
		SchemaVersion: "scenery.dev.detach.v1",
		Wait:          waitMode,
		PID:           session.OwnerPID,
		LogPath:       logPath,
		AttachCommand: fmt.Sprintf("scenery logs --follow --app-root %q", root),
		DownCommand:   fmt.Sprintf("scenery down --app-root %q", root),
		Session:       session,
	})
}

func detachedDevWaitTimeout(waitMode string) time.Duration {
	if waitMode == detachedDevWaitReady {
		return detachedDevReadyTimeout
	}
	return detachedDevStartupTimeout
}

func rejectDetachedDuplicateDevSession(ctx context.Context, client *localagent.Client, root string, opts devOptions) error {
	if client == nil {
		return nil
	}
	sessions, err := client.List(ctx, root)
	if err != nil {
		return err
	}
	return rejectLiveDuplicateDevSession(root, sessions)
}

func detachedDevChildMode() bool {
	switch strings.ToLower(strings.TrimSpace(envpolicy.Get(detachedDevChildEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func devArgsForDetachedChild(args []string, appRoot string) []string {
	filtered := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--detach" {
			continue
		}
		if arg == "--wait" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "--wait=") {
			continue
		}
		if arg == "--app-root" {
			i++
			continue
		}
		if arg == "-o" {
			i++
			continue
		}
		if strings.HasPrefix(arg, "-o=") {
			continue
		}
		filtered = append(filtered, arg)
	}
	return append(filtered, "-o", "jsonl", "--app-root", appRoot)
}

func detachedDevLogPath(paths localagent.Paths, appRoot string, now time.Time) string {
	name := safeDetachedLogName(filepath.Base(appRoot))
	sum := sha256.Sum256([]byte(filepath.Clean(appRoot)))
	stamp := now.UTC().Format("20060102T150405Z")
	return filepath.Join(paths.AgentDir, "dev", fmt.Sprintf("%s-%s-%s.log", name, stamp, hex.EncodeToString(sum[:])[:10]))
}

func safeDetachedLogName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "app"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_':
			b.WriteRune(r)
		case unicode.IsSpace(r) || r == '.':
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-_")
	if out == "" {
		return "app"
	}
	return out
}

func waitForDetachedDevSession(ctx context.Context, client *localagent.Client, appRoot string, ownerPID int, waitMode string, expectedFrontends []string) (localagent.Session, error) {
	return waitForDetachedDevSessionWithLister(ctx, client.List, appRoot, ownerPID, waitMode, expectedFrontends)
}

type detachedDevSessionLister func(context.Context, string) ([]localagent.Session, error)

func waitForDetachedDevSessionWithLister(ctx context.Context, list detachedDevSessionLister, appRoot string, ownerPID int, waitMode string, expectedFrontends []string) (localagent.Session, error) {
	ticker := time.NewTicker(detachedDevStartupInterval)
	defer ticker.Stop()
	var lastErr error
	var lastSession localagent.Session
	lastState := "not registered"
	for {
		sessions, err := list(ctx, appRoot)
		if err != nil {
			lastErr = err
		}
		for _, session := range sessions {
			if session.OwnerPID == ownerPID {
				lastSession = session
				ready, state := detachedDevReadinessState(session, waitMode, expectedFrontends)
				lastState = state
				if ready {
					return session, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			timeoutErr := fmt.Errorf("last state: %s", lastState)
			if lastErr != nil {
				return lastSession, errors.Join(ctx.Err(), timeoutErr, lastErr)
			}
			return lastSession, errors.Join(ctx.Err(), timeoutErr)
		case <-ticker.C:
		}
	}
}

func normalizeDetachedDevWaitMode(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", detachedDevWaitReady:
		return detachedDevWaitReady, nil
	case detachedDevWaitRegistered:
		return detachedDevWaitRegistered, nil
	default:
		return "", fmt.Errorf("invalid --wait %q; expected registered or ready", value)
	}
}

func detachedDevExpectedFrontendRoutes(frontends map[string]app.FrontendConfig) []string {
	configured := configuredFrontends(frontends)
	names := make([]string, 0, len(configured))
	for _, frontend := range configured {
		if name := localagentLabel(frontend.Name); name != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}

func detachedDevReadinessState(session localagent.Session, waitMode string, expectedFrontends []string) (bool, string) {
	if waitMode == detachedDevWaitRegistered {
		return true, "registered"
	}
	status := strings.TrimSpace(session.Status)
	if status != "running" {
		if status == "" {
			status = "registered"
		}
		return false, "registered; status=" + status
	}
	if backend, ok := session.Backends[localagent.RouteAPI]; !ok || strings.TrimSpace(backend.Addr) == "" {
		return false, "registered running; api backend missing"
	} else if !detachedDevBackendAcceptsConnections(devBackend{Network: backend.Network, Addr: backend.Addr}) {
		return false, "registered running; api backend not accepting connections"
	}
	for _, name := range expectedFrontends {
		backend, ok := session.Backends[name]
		if !ok || strings.TrimSpace(backend.Addr) == "" {
			return false, fmt.Sprintf("registered running; frontend %s backend missing", name)
		}
		if !detachedDevBackendAcceptsConnections(devBackend{Network: backend.Network, Addr: backend.Addr}) {
			return false, fmt.Sprintf("registered running; frontend %s backend not accepting connections", name)
		}
	}
	return true, "ready"
}

func writeDetachedDevResult(w io.Writer, jsonMode bool, result detachedDevResult) error {
	if jsonMode {
		return writeCLIJSON(w, result)
	}
	fmt.Fprintln(w, "[+] Running 1/1")
	fmt.Fprintf(
		w,
		" - App %s  %s  pid=%d\n\n",
		detachedDevAppLabel(result.Session),
		detachedDevDisplayStatus(result.Session.Status),
		result.PID,
	)
	if result.Session.Status == "running" {
		newRunConsole(w, io.Discard, false, false, detachedDevAppLabel(result.Session), result.Session.AppRoot).Banner(detachedDevRunURLs(result.Session))
	}
	fmt.Fprintln(w, "Use:")
	if statusCommand := detachedDevStatusCommand(result.Session); statusCommand != "" {
		fmt.Fprintf(w, "  status  %s\n", statusCommand)
	}
	fmt.Fprintf(w, "  logs    %s\n", result.AttachCommand)
	fmt.Fprintf(w, "  stop    %s\n", result.DownCommand)
	fmt.Fprintf(w, "\nLog file: %s\n", result.LogPath)
	if len(result.Session.Routes) > 0 {
		fmt.Fprintln(w, "\nRoutes currently registered:")
	}
	for _, name := range sortedRouteNames(result.Session.Routes) {
		fmt.Fprintf(w, "  %-10s %s\n", name, result.Session.Routes[name])
	}
	if len(result.Session.Aliases) > 0 {
		fmt.Fprintln(w, "\nAliases currently claimed:")
	}
	for _, name := range sortedRouteNames(result.Session.Aliases) {
		fmt.Fprintf(w, "  %-10s %s\n", name, result.Session.Aliases[name])
	}
	if len(result.Session.AliasConflicts) > 0 {
		fmt.Fprintln(w, "\nAliases held by other app roots:")
	}
	for _, name := range sortedAliasConflictNames(result.Session.AliasConflicts) {
		conflict := result.Session.AliasConflicts[name]
		fmt.Fprintf(w, "  %-10s %s owned by %s\n", name, conflict.Host, aliasConflictOwnerLabel(conflict))
	}
	return nil
}

func detachedDevRunURLs(session localagent.Session) runURLs {
	return runURLs{
		API:       session.Routes[localagent.RouteAPI],
		Dashboard: session.Routes[localagent.RouteDashboard],
		Frontends: frontendURLsFromAgentRoutes(session.Routes, nil),
	}
}

func detachedDevAppLabel(session localagent.Session) string {
	if value := strings.TrimSpace(session.AppRoot); value != "" {
		return value
	}
	if value := strings.TrimSpace(session.BaseAppID); value != "" {
		return value
	}
	if value := strings.TrimSpace(session.SessionID); value != "" {
		return value
	}
	return "app"
}

func aliasConflictOwnerLabel(conflict localagent.AliasLease) string {
	if value := strings.TrimSpace(conflict.AppRoot); value != "" {
		return value
	}
	return "another app root"
}

func detachedDevDisplayStatus(status string) string {
	status = strings.TrimSpace(status)
	if status == "" {
		return "Starting"
	}
	return strings.ToUpper(status[:1]) + strings.ToLower(status[1:])
}

func detachedDevStatusCommand(session localagent.Session) string {
	appRoot := strings.TrimSpace(session.AppRoot)
	if appRoot == "" {
		return "scenery ps"
	}
	return fmt.Sprintf("scenery ps --app-root %q", appRoot)
}

func sortedAliasConflictNames(conflicts map[string]localagent.AliasLease) []string {
	names := make([]string, 0, len(conflicts))
	for name, conflict := range conflicts {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(conflict.Host) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedRouteNames(routes map[string]string) []string {
	names := make([]string, 0, len(routes))
	for name, value := range routes {
		if strings.TrimSpace(name) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
