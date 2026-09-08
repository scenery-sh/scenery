package agent

import (
	context "context"
	errors "errors"
	os "os"
	filepath "path/filepath"
	"scenery.sh/internal/devprocess"

	strings "strings"
)

func StopDeletedSessionProcesses(ctx context.Context, session Session) error {
	return stopDeletedSessionProcessesWithDependencies(ctx, session, stopDeletedSessionProcessDependencies{
		shouldSignalOwner: shouldSignalSessionOwner,
		stopOwner:         stopSessionOwnerPID,
		processPIDs:       sessionProcessPIDs,
		stopChild:         stopStaleSessionChildPID,
		stopCommands:      stopSessionCommandProcesses,
		stopEnvironment:   stopSessionEnvProcesses,
	})
}

func stopDeletedSessionProcessesWithDependencies(ctx context.Context, session Session, deps stopDeletedSessionProcessDependencies) error {
	var errs []error
	seen := map[int]bool{}
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID > 0 && ownerPID != os.Getpid() && deps.shouldSignalOwner(session) {
		errs = append(errs, deps.stopOwner(ctx, ownerPID))
		seen[ownerPID] = true
	}
	for _, pid := range deps.processPIDs(session) {
		if pid <= 0 || pid == os.Getpid() || seen[pid] {
			continue
		}
		if err := deps.stopChild(ctx, pid); err != nil {
			errs = append(errs, err)
		}
		seen[pid] = true
	}
	errs = append(errs, deps.stopCommands(ctx, session, seen))
	errs = append(errs, deps.stopEnvironment(ctx, session, seen))
	return errors.Join(errs...)
}

type stopDeletedSessionProcessDependencies struct {
	shouldSignalOwner func(Session) bool
	stopOwner         func(context.Context, int) error
	processPIDs       func(Session) []int
	stopChild         func(context.Context, int) error
	stopCommands      func(context.Context, Session, map[int]bool) error
	stopEnvironment   func(context.Context, Session, map[int]bool) error
}

func SessionOwnerProcessLive(session Session) (int, bool) {
	ownerPID := firstPositiveInt(session.OwnerPID, session.Owner.PID)
	if ownerPID <= 0 {
		return 0, false
	}
	owner := session.Owner
	if owner.PID != ownerPID {
		owner = CaptureOwner(ownerPID, "scenery up")
	} else if owner.PID <= 0 {
		owner.PID = ownerPID
	}
	if VerifyOwner(owner) == nil {
		return ownerPID, true
	}
	_, ok := devprocess.Inspect(ownerPID)
	return ownerPID, ok
}

func looksLikeSceneryDashboardProcess(info devprocess.ProcessInfo) bool {
	lower := strings.ToLower(info.Command)
	return strings.Contains(lower, "scenery") && strings.Contains(lower, " up")
}

func cleanAbsPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	return filepath.Clean(path)
}
