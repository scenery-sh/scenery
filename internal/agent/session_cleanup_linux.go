//go:build linux

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func stopSessionEnvProcesses(ctx context.Context, current Session, seen map[int]bool) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	var errs []error
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 || pid == os.Getpid() || seen[pid] {
			continue
		}
		env, ok := readProcEnv(pid)
		if !ok || !envMatchesSession(env, current) || !sceneryOwnedSessionEnv(env) {
			continue
		}
		if err := stopStaleSessionChildPID(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	return errors.Join(errs...)
}

func readProcEnv(pid int) (map[string]string, bool) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "environ"))
	if err != nil || len(data) == 0 {
		return nil, false
	}
	env := map[string]string{}
	for _, raw := range strings.Split(strings.TrimRight(string(data), "\x00"), "\x00") {
		if raw == "" {
			continue
		}
		name, value, ok := strings.Cut(raw, "=")
		if !ok || name == "" {
			continue
		}
		env[name] = value
	}
	return env, true
}

func envMatchesSession(env map[string]string, session Session) bool {
	return cleanAbsPath(env["SCENERY_APP_ROOT"]) == cleanAbsPath(session.AppRoot) &&
		strings.TrimSpace(env["SCENERY_SESSION_ID"]) == strings.TrimSpace(session.SessionID)
}

func sceneryOwnedSessionEnv(env map[string]string) bool {
	return strings.TrimSpace(env["SCENERY_DEV_SUPERVISOR"]) == "1"
}
