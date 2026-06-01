package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	localagent "github.com/pbrazdil/onlava/internal/agent"
)

const staleSessionCleanupGrace = 2 * time.Second

func cleanupSupersededDevSessions(ctx context.Context, current localagent.Session, previous []localagent.Session) error {
	if strings.TrimSpace(current.AppRoot) == "" || strings.TrimSpace(current.SessionID) == "" {
		return nil
	}
	var errs []error
	seen := map[int]bool{}
	for _, session := range previous {
		if !sameAgentSession(current, session) {
			continue
		}
		errs = append(errs, stopSupersededSessionProcesses(ctx, current, session, seen))
	}
	errs = append(errs, stopSessionEnvProcesses(ctx, current, seen))
	return errors.Join(errs...)
}

func sameAgentSession(a, b localagent.Session) bool {
	return cleanAbsPath(a.AppRoot) == cleanAbsPath(b.AppRoot) &&
		strings.TrimSpace(a.SessionID) == strings.TrimSpace(b.SessionID)
}

func stopSupersededSessionProcesses(ctx context.Context, current, previous localagent.Session, seen map[int]bool) error {
	var errs []error
	currentOwnerPID := firstPositiveInt(current.Owner.PID, current.OwnerPID)
	previousOwnerPID := firstPositiveInt(previous.Owner.PID, previous.OwnerPID)
	if previousOwnerPID > 0 && previousOwnerPID != os.Getpid() && previousOwnerPID != currentOwnerPID {
		if shouldSignalSessionOwner(previous) {
			errs = append(errs, stopSessionOwnerPID(ctx, previousOwnerPID))
			seen[previousOwnerPID] = true
		}
	}
	for _, pid := range sessionProcessPIDs(previous) {
		if pid <= 0 || pid == os.Getpid() || pid == currentOwnerPID || seen[pid] {
			continue
		}
		if err := stopStaleSessionChildPID(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	return errors.Join(errs...)
}

func shouldSignalSessionOwner(session localagent.Session) bool {
	owner := session.Owner
	if owner.PID <= 0 {
		owner.PID = session.OwnerPID
	}
	if owner.PID <= 0 {
		return false
	}
	if err := localagent.VerifyOwner(owner); err == nil {
		return true
	}
	info, ok := inspectProcess(owner.PID)
	return ok && looksLikeOnlavaDashboardProcess(info)
}

func stopSessionOwnerPID(ctx context.Context, pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if waitForPIDExit(ctx, pid, staleSessionCleanupGrace) {
		return nil
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if waitForPIDExit(ctx, pid, time.Second) {
		return nil
	}
	return fmt.Errorf("stale onlava dev owner process %d did not exit after SIGKILL", pid)
}

func stopStaleSessionChildPID(ctx context.Context, pid int) error {
	if err := terminateProcessIDTree(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if waitForPIDExit(ctx, pid, staleSessionCleanupGrace) {
		return nil
	}
	if err := killProcessIDTree(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if waitForPIDExit(ctx, pid, time.Second) {
		return nil
	}
	return fmt.Errorf("stale onlava session child process %d did not exit after SIGKILL", pid)
}

func waitForPIDExit(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, ok := inspectProcess(pid); !ok || strings.Contains(info.stat, "Z") {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case <-ticker.C:
		}
	}
}

func sessionProcessPIDs(session localagent.Session) []int {
	seen := map[int]bool{}
	var pids []int
	if pid := atoiPID(session.AppPID); pid > 0 {
		process := session.Processes[localagent.RouteAPI]
		process.PID = pid
		if shouldSignalSessionProcess(process) {
			seen[pid] = true
			pids = append(pids, pid)
		}
	}
	for _, process := range session.Processes {
		if process.PID > 0 && !seen[process.PID] && shouldSignalSessionProcess(process) {
			seen[process.PID] = true
			pids = append(pids, process.PID)
		}
	}
	return pids
}

func shouldSignalSessionProcess(process localagent.Process) bool {
	if process.PID <= 0 {
		return false
	}
	if process.Owner.PID <= 0 {
		info, ok := inspectProcess(process.PID)
		return ok && looksLikeOnlavaSessionChildProcess(info)
	}
	return process.Owner.PID == process.PID && localagent.VerifyOwner(process.Owner) == nil
}

func looksLikeOnlavaSessionChildProcess(info procInfo) bool {
	command := strings.ToLower(filepath.ToSlash(strings.TrimSpace(info.cmd)))
	return strings.Contains(command, "/.onlava/") ||
		strings.Contains(command, "onlava-app-") ||
		strings.Contains(command, "worker.ts")
}

func atoiPID(value string) int {
	pid, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return 0
	}
	return pid
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
