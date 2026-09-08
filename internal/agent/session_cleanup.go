package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"scenery.sh/internal/devprocess"
	"strconv"
	"strings"
	"time"
)

const staleSessionCleanupGrace = 2 * time.Second

func CleanupStaleSessionProcesses(ctx context.Context, current Session, previous []Session) error {
	return cleanupStaleDevSessionProcessesWithDependencies(ctx, current, previous, staleDevSessionCleanupDependencies{
		sameScope:       SameSessionCleanupScope,
		stopRegistered:  StopStaleRegisteredSessionProcesses,
		stopCommands:    stopSessionCommandProcesses,
		stopEnvironment: stopSessionEnvProcesses,
	})
}

type staleDevSessionCleanupDependencies struct {
	sameScope       func(Session, Session) bool
	stopRegistered  func(context.Context, Session, Session, map[int]bool) error
	stopCommands    func(context.Context, Session, map[int]bool) error
	stopEnvironment func(context.Context, Session, map[int]bool) error
}

func cleanupStaleDevSessionProcessesWithDependencies(ctx context.Context, current Session, previous []Session, deps staleDevSessionCleanupDependencies) error {
	if strings.TrimSpace(current.AppRoot) == "" || strings.TrimSpace(current.SessionID) == "" {
		return nil
	}
	var errs []error
	seen := map[int]bool{}
	for _, session := range previous {
		if !deps.sameScope(current, session) {
			continue
		}
		errs = append(errs, deps.stopRegistered(ctx, current, session, seen))
	}
	errs = append(errs, deps.stopCommands(ctx, current, seen))
	errs = append(errs, deps.stopEnvironment(ctx, current, seen))
	return errors.Join(errs...)
}

func sameAgentSession(a, b Session) bool {
	return cleanAbsPath(a.AppRoot) == cleanAbsPath(b.AppRoot) &&
		strings.TrimSpace(a.SessionID) == strings.TrimSpace(b.SessionID)
}

func SameSessionCleanupScope(current, previous Session) bool {
	if sameAgentSession(current, previous) {
		return true
	}
	if cleanAbsPath(current.AppRoot) != cleanAbsPath(previous.AppRoot) {
		return false
	}
	_, live := SessionOwnerProcessLive(previous)
	return !live
}

func StopStaleRegisteredSessionProcesses(ctx context.Context, current, previous Session, seen map[int]bool) error {
	var errs []error
	currentOwnerPID := firstPositiveInt(current.OwnerPID, current.Owner.PID)
	previousOwnerPID := firstPositiveInt(previous.OwnerPID, previous.Owner.PID)
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

func shouldSignalSessionOwner(session Session) bool {
	owner := session.Owner
	effectivePID := firstPositiveInt(session.OwnerPID, owner.PID)
	if owner.PID != effectivePID {
		owner = Owner{}
	}
	if owner.PID <= 0 {
		owner.PID = session.OwnerPID
	}
	if owner.PID <= 0 {
		return false
	}
	if err := VerifyOwner(owner); err == nil {
		return true
	}
	info, ok := devprocess.Inspect(owner.PID)
	return ok && looksLikeSceneryDashboardProcess(info)
}

func stopSessionOwnerPID(ctx context.Context, pid int) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	if err := proc.Signal(os.Interrupt); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if devprocess.WaitForExit(ctx, pid, staleSessionCleanupGrace) {
		return nil
	}
	if err := proc.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if devprocess.WaitForExit(ctx, pid, time.Second) {
		return nil
	}
	return fmt.Errorf("stale scenery up owner process %d did not exit after SIGKILL", pid)
}

func stopStaleSessionChildPID(ctx context.Context, pid int) error {
	if err := devprocess.TerminateTreePID(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if devprocess.WaitForExit(ctx, pid, staleSessionCleanupGrace) {
		return nil
	}
	if err := devprocess.KillTreePID(pid); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	if devprocess.WaitForExit(ctx, pid, time.Second) {
		return nil
	}
	return fmt.Errorf("stale scenery session child process %d did not exit after SIGKILL", pid)
}

func sessionProcessPIDs(session Session) []int {
	seen := map[int]bool{}
	var pids []int
	if pid := atoiPID(session.AppPID); pid > 0 {
		process := session.Processes[RouteAPI]
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

func shouldSignalSessionProcess(process Process) bool {
	if process.PID <= 0 {
		return false
	}
	if process.Owner.PID <= 0 {
		info, ok := devprocess.Inspect(process.PID)
		return ok && looksLikeScenerySessionChildProcess(info)
	}
	return process.Owner.PID == process.PID && VerifyOwner(process.Owner) == nil
}

func looksLikeScenerySessionChildProcess(info devprocess.ProcessInfo) bool {
	command := strings.ToLower(filepath.ToSlash(strings.TrimSpace(info.Command)))
	return strings.Contains(command, "/.scenery/") ||
		strings.Contains(command, "scenery-app-") ||
		strings.Contains(command, "worker.ts")
}

func stopSessionCommandProcesses(ctx context.Context, current Session, seen map[int]bool) error {
	output, err := exec.Command("ps", "-axo", "pid=,stat=,command=").Output()
	if err != nil {
		return nil
	}
	return stopSessionCommandProcessesFromPS(ctx, current, seen, string(output), stopStaleSessionChildPID)
}

func stopSessionCommandProcessesFromPS(ctx context.Context, current Session, seen map[int]bool, output string, stopPID func(context.Context, int) error) error {
	stateRoot := filepath.ToSlash(cleanAbsPath(current.StateRoot))
	if stateRoot == "" {
		return nil
	}
	var errs []error
	for _, line := range strings.Split(output, "\n") {
		pid, stat, command, ok := parsePSCommandLine(line)
		if !ok || pid <= 0 || pid == os.Getpid() || strings.Contains(stat, "Z") || seen[pid] {
			continue
		}
		if !commandMatchesSessionStateRoot(command, stateRoot) {
			continue
		}
		if err := stopPID(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	return errors.Join(errs...)
}

func parsePSCommandLine(line string) (int, string, string, bool) {
	fields := strings.Fields(strings.TrimSpace(line))
	if len(fields) < 3 {
		return 0, "", "", false
	}
	pid, err := strconv.Atoi(fields[0])
	if err != nil {
		return 0, "", "", false
	}
	return pid, fields[1], strings.Join(fields[2:], " "), true
}

func commandMatchesSessionStateRoot(command, stateRoot string) bool {
	command = filepath.ToSlash(strings.TrimSpace(command))
	return strings.Contains(command, stateRoot+"/") && strings.Contains(command, "scenery-app")
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
