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

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

const (
	detachedDevChildEnv       = "ONLAVA_DEV_DETACHED_CHILD"
	detachedDevStartupTimeout = 10 * time.Second
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
		return fmt.Errorf("onlava dev --detach requires the local onlava agent; unset ONLAVA_AGENT_DISABLE")
	}
	ctx, cancel := context.WithTimeout(context.Background(), detachedDevStartupTimeout)
	defer cancel()
	client, err := localagent.Ensure(ctx)
	if err != nil {
		return err
	}
	if client == nil {
		return fmt.Errorf("onlava dev --detach requires the local onlava agent")
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
	childArgs := append([]string{"dev"}, devArgsForDetachedChild(args, root)...)
	cmd := exec.Command(exe, childArgs...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), detachedDevChildEnv+"=1")
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
		return fmt.Errorf("detached onlava dev process %d did not register an agent session before timeout: %w; see %s", cmd.Process.Pid, err, logPath)
	}
	if err := cmd.Process.Release(); err != nil {
		return err
	}
	return writeDetachedDevResult(os.Stdout, opts.JSON, detachedDevResult{
		SchemaVersion: "onlava.dev.detach.v1",
		PID:           session.OwnerPID,
		LogPath:       logPath,
		AttachCommand: fmt.Sprintf("onlava attach --app-root %q --session %s", root, session.SessionID),
		DownCommand:   fmt.Sprintf("onlava down --session %s", session.SessionID),
		Session:       session,
	})
}

func detachedDevChildMode() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(detachedDevChildEnv))) {
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
	fmt.Fprintf(w, "started onlava session %s (pid %d)\n", result.Session.SessionID, result.PID)
	for _, name := range sortedRouteNames(result.Session.Routes) {
		fmt.Fprintf(w, "%s: %s\n", name, result.Session.Routes[name])
	}
	fmt.Fprintf(w, "logs: %s\n", result.LogPath)
	fmt.Fprintf(w, "attach: %s\n", result.AttachCommand)
	fmt.Fprintf(w, "stop: %s\n", result.DownCommand)
	return nil
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
