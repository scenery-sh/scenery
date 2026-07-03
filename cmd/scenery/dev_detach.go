package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
)

var detachedDevStartupInterval = 100 * time.Millisecond

type detachedDevResult struct {
	SchemaVersion string             `json:"schema_version"`
	PID           int                `json:"pid"`
	LogPath       string             `json:"log_path"`
	AttachCommand string             `json:"attach_command"`
	DownCommand   string             `json:"down_command"`
	Session       localagent.Session `json:"session"`
}

func runDetachedDev(args []string, opts devOptions) error {
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, _, err := app.DiscoverRoot(start)
	if err != nil {
		return err
	}

	if localagent.DisabledByEnv() {
		return fmt.Errorf("scenery up --detach requires the local scenery agent; unset SCENERY_AGENT_DISABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), detachedDevStartupTimeout)
	defer cancel()
	client, err := localagent.Ensure(ctx, cliBuildIdentity())
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("scenery up --detach requires the local scenery agent")
	}
	if err := rejectDetachedDuplicateDevSession(ctx, client, root, opts); err != nil {
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

	session, err := waitForDetachedDevSession(ctx, client, root, cmd.Process.Pid)
	if err != nil {
		_ = interruptProcessTree(cmd)
		_ = cmd.Process.Release()
		return fmt.Errorf("detached scenery up process %d did not register an agent session before timeout: %w; see %s", cmd.Process.Pid, err, logPath)
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	return writeDetachedDevResult(os.Stdout, opts.JSON, detachedDevResult{
		SchemaVersion: "scenery.dev.detach.v1",
		PID:           session.OwnerPID,
		LogPath:       logPath,
		AttachCommand: fmt.Sprintf("scenery logs --follow --app-root %q", root),
		DownCommand:   fmt.Sprintf("scenery down --app-root %q", root),
		Session:       session,
	})
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
		if arg == "--app-root" {
			i++
			continue
		}
		filtered = append(filtered, arg)
	}
	return append(filtered, "--app-root", appRoot)
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

func waitForDetachedDevSession(ctx context.Context, client *localagent.Client, appRoot string, ownerPID int) (localagent.Session, error) {
	ticker := time.NewTicker(detachedDevStartupInterval)
	defer ticker.Stop()
	var lastErr error
	for {
		sessions, err := client.List(ctx, appRoot)
		if err != nil {
			lastErr = err
		}
		for _, session := range sessions {
			if session.OwnerPID == ownerPID {
				return session, nil
			}
		}
		select {
		case <-ctx.Done():
			if lastErr != nil {
				return localagent.Session{}, errors.Join(ctx.Err(), lastErr)
			}
			return localagent.Session{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func writeDetachedDevResult(w io.Writer, jsonMode bool, result detachedDevResult) error {
	if jsonMode {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(result)
	}
	fmt.Fprintln(w, "[+] Running 1/1")
	fmt.Fprintf(
		w,
		" - App %s  %s  pid=%d\n\n",
		detachedDevAppLabel(result.Session),
		detachedDevDisplayStatus(result.Session.Status),
		result.PID,
	)
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
