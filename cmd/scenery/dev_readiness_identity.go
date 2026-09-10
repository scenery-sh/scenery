package main

import (
	"fmt"
	"strconv"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
)

// A listening backend can precede the supervisor's process-record publication.
// Only absent API publication is pending; contradictory ownership fails now.
func detachedDevPublishedIdentity(session localagent.Session, verify func(localagent.Owner) error) (string, error) {
	if session.Owner.PID != session.OwnerPID || !publishedOwnerFingerprint(session.Owner) {
		return "", detachedDevOwnershipFailure("Detached runtime supervisor ownership is incomplete or inconsistent.", nil)
	}
	if err := verify(session.Owner); err != nil {
		return "", detachedDevOwnershipFailure("Detached runtime supervisor ownership could not be verified.", err)
	}
	if session.AppPID == "" {
		return "registered running; api process identity not published", nil
	}
	pid, err := strconv.Atoi(session.AppPID)
	if err != nil || pid <= 0 || strconv.Itoa(pid) != session.AppPID {
		return "", detachedDevOwnershipFailure("Detached runtime API process id is invalid.", nil)
	}
	process, ok := session.Processes[localagent.RouteAPI]
	if !ok || process.Owner.PID == 0 {
		return "registered running; api process fingerprint not published", nil
	}
	if process.PID != pid || process.Owner.PID != pid || !publishedOwnerFingerprint(process.Owner) {
		return "", detachedDevOwnershipFailure("Detached runtime API ownership is incomplete or inconsistent.", nil)
	}
	if err := verify(process.Owner); err != nil {
		return "", detachedDevOwnershipFailure("Detached runtime API process ownership could not be verified.", err)
	}
	return "", nil
}

func detachedDevOwnershipFailure(message string, cause error) error {
	err := fmt.Errorf("%s", message)
	if cause != nil {
		err = fmt.Errorf("%s: %w", message, cause)
	}
	return &cliDiagnosticError{err: err, code: 3,
		diagnostic: compiler.TransportDiagnostic("failed_precondition", message), startupReason: "wait_failure"}
}

func publishedOwnerFingerprint(owner localagent.Owner) bool {
	return owner.PID > 0 && (strings.TrimSpace(owner.StartedAt) != "" || strings.TrimSpace(owner.Exe) != "" || strings.TrimSpace(owner.CmdlineHash) != "")
}
