package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"syscall"
	"time"

	"scenery.sh/internal/devprocess"
)

// Session cleanup signals only processes a session recorded as its own: the
// supervisor owner and each registered child, each verified against the
// identity captured when it was registered. A process that merely resembles a
// session process by its command line, environment, listening port or parent
// belongs to someone else and is never selected. A recorded child stays owned
// after its supervisor is gone, so a stale session's children are still
// stopped through its record.

const staleSessionCleanupGrace = 2 * time.Second

// CleanupStaleSessionProcesses stops the recorded processes of every previous
// session in current's cleanup scope.
func CleanupStaleSessionProcesses(ctx context.Context, current Session, previous []Session) error {
	if strings.TrimSpace(current.AppRoot) == "" || strings.TrimSpace(current.SessionID) == "" {
		return nil
	}
	var errs []error
	seen := map[int]bool{}
	for _, session := range previous {
		if !SameSessionCleanupScope(current, session) {
			continue
		}
		errs = append(errs, StopStaleRegisteredSessionProcesses(ctx, current, session, seen))
	}
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

// StopStaleRegisteredSessionProcesses stops previous's verified owner and
// children, never current's owner or this process.
func StopStaleRegisteredSessionProcesses(ctx context.Context, current, previous Session, seen map[int]bool) error {
	keep := map[int]bool{os.Getpid(): true}
	if pid := firstPositiveInt(current.OwnerPID, current.Owner.PID); pid > 0 {
		keep[pid] = true
	}
	return stopRecordedSessionProcesses(ctx, previous, keep, seen, VerifyOwner, stopRecordedSessionOwner, stopRecordedSessionChild)
}

// StopDeletedSessionProcesses stops the verified owner and children a deleted
// session recorded.
func StopDeletedSessionProcesses(ctx context.Context, session Session) error {
	return stopRecordedSessionProcesses(ctx, session, map[int]bool{os.Getpid(): true}, map[int]bool{}, VerifyOwner, stopRecordedSessionOwner, stopRecordedSessionChild)
}

type recordedProcessStop func(context.Context, Owner) error

func stopRecordedSessionProcesses(ctx context.Context, session Session, keep, seen map[int]bool, verify func(Owner) error, stopOwner, stopChild recordedProcessStop) error {
	var errs []error
	if owner, ok := recordedSessionOwner(session, verify); ok && !keep[owner.PID] && !seen[owner.PID] {
		errs = append(errs, stopOwner(ctx, owner))
		seen[owner.PID] = true
	}
	for _, owner := range recordedSessionChildren(session, verify) {
		if keep[owner.PID] || seen[owner.PID] {
			continue
		}
		errs = append(errs, stopChild(ctx, owner))
		seen[owner.PID] = true
	}
	return errors.Join(errs...)
}

// recordedSessionOwner returns the session's supervisor owner when its record
// names the effective owner PID and the live process still verifies.
func recordedSessionOwner(session Session, verify func(Owner) error) (Owner, bool) {
	owner := session.Owner
	pid := firstPositiveInt(session.OwnerPID, owner.PID)
	if pid <= 0 || owner.PID != pid || verify(owner) != nil {
		return Owner{}, false
	}
	return owner, true
}

// recordedSessionChildren returns, in PID order, the registered children whose
// recorded owner names their PID and still verifies.
func recordedSessionChildren(session Session, verify func(Owner) error) []Owner {
	var owners []Owner
	selected := map[int]bool{}
	for _, process := range session.Processes {
		if process.PID <= 0 || process.Owner.PID != process.PID || selected[process.PID] || verify(process.Owner) != nil {
			continue
		}
		selected[process.PID] = true
		owners = append(owners, process.Owner)
	}
	sort.Slice(owners, func(i, j int) bool { return owners[i].PID < owners[j].PID })
	return owners
}

func stopRecordedSessionOwner(ctx context.Context, owner Owner) error {
	return stopRecordedProcess(ctx, owner, "stale scenery up owner",
		func(pid int) error { return signalRecordedPID(pid, os.Interrupt) },
		func(pid int) error { return signalRecordedPID(pid, syscall.SIGKILL) })
}

func stopRecordedSessionChild(ctx context.Context, owner Owner) error {
	return stopRecordedProcess(ctx, owner, "stale scenery session child", devprocess.TerminateTreePID, devprocess.KillTreePID)
}

// stopRecordedProcess asks the recorded process to stop and escalates only
// while the same recorded identity still verifies.
func stopRecordedProcess(ctx context.Context, owner Owner, label string, terminate, kill func(int) error) error {
	if err := terminate(owner.PID); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if devprocess.WaitForExit(ctx, owner.PID, staleSessionCleanupGrace) {
		return nil
	}
	if VerifyOwner(owner) != nil {
		return nil
	}
	if err := kill(owner.PID); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	if devprocess.WaitForExit(ctx, owner.PID, time.Second) {
		return nil
	}
	return fmt.Errorf("%s process %d did not exit after SIGKILL", label, owner.PID)
}

func signalRecordedPID(pid int, signal os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return nil
	}
	return proc.Signal(signal)
}

func firstPositiveInt(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
