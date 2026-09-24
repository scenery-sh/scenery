package agent

import (
	"path/filepath"
	"strings"

	"scenery.sh/internal/devprocess"
)

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
