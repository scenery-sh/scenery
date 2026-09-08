//go:build !linux

package agent

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

func stopSessionEnvProcesses(ctx context.Context, current Session, seen map[int]bool) error {
	output, err := exec.Command("ps", "eww", "-axo", "pid=,stat=,command=").Output()
	if err != nil {
		return nil
	}
	var errs []error
	for _, line := range strings.Split(string(output), "\n") {
		pid, stat, env, ok := parsePSEnvProcessLine(line)
		if !ok || pid <= 0 || pid == os.Getpid() || strings.Contains(stat, "Z") || seen[pid] {
			continue
		}
		if !envMatchesSession(env, current) || !sceneryOwnedSessionEnv(env) {
			continue
		}
		if err := stopStaleSessionChildPID(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	return errors.Join(errs...)
}

func parsePSEnvProcessLine(line string) (int, string, map[string]string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 {
		return 0, "", nil, false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", nil, false
	}
	env := map[string]string{}
	for _, field := range fields[2:] {
		name, value, ok := strings.Cut(field, "=")
		if !ok || name == "" {
			continue
		}
		env[name] = value
	}
	return pid, fields[1], env, true
}

func envMatchesSession(env map[string]string, session Session) bool {
	return cleanAbsPath(env["SCENERY_APP_ROOT"]) == cleanAbsPath(session.AppRoot) &&
		strings.TrimSpace(env["SCENERY_SESSION_ID"]) == strings.TrimSpace(session.SessionID)
}

func sceneryOwnedSessionEnv(env map[string]string) bool {
	return strings.TrimSpace(env["SCENERY_DEV_SUPERVISOR"]) == "1"
}
