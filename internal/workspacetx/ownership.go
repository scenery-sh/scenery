package workspacetx

import (
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type ownerProcessLiveness uint8

const (
	ownerProcessUnknown ownerProcessLiveness = iota
	ownerProcessLive
	ownerProcessDead
)

type ownerProcessInfo struct {
	StartedAt string
	Exe       string
	Cmdline   []string
	Liveness  ownerProcessLiveness
}

type ownerState uint8

const (
	ownerUnknown ownerState = iota
	ownerCurrent
	ownerLive
	ownerRecoverable
)

type Owner struct {
	PID         int       `json:"pid,omitempty"`
	StartedAt   string    `json:"started_at,omitempty"`
	Exe         string    `json:"exe,omitempty"`
	CmdlineHash string    `json:"cmdline_hash,omitempty"`
	AgentPID    int       `json:"agent_pid,omitempty"`
	CreatedBy   string    `json:"created_by,omitempty"`
	RecordedAt  time.Time `json:"recorded_at"`
}

// currentProcessOwnerToken distinguishes transaction owners created by this
// process even when the OS exposes incomplete or no process metadata. It is
// machine-local transaction state, not a public identity.
var currentProcessOwnerToken = newCurrentProcessOwnerToken()

func newCurrentProcessOwnerToken() string {
	var token [16]byte
	if _, err := cryptorand.Read(token[:]); err == nil {
		return "change-transaction:" + hex.EncodeToString(token[:])
	}
	fallback := sha256.Sum256([]byte(time.Now().UTC().Format(time.RFC3339Nano) + "\x00" + strings.Join(os.Args, "\x00")))
	return "change-transaction:" + hex.EncodeToString(fallback[:16])
}

func currentOwner() Owner {
	pid := os.Getpid()
	info := processOwnerInfo(pid)
	owner := Owner{
		PID: pid, StartedAt: strings.TrimSpace(info.StartedAt), Exe: strings.TrimSpace(info.Exe),
		AgentPID: pid, CreatedBy: currentProcessOwnerToken, RecordedAt: time.Now().UTC(),
	}
	if len(info.Cmdline) > 0 {
		owner.CmdlineHash = hashCmdline(info.Cmdline)
	}
	if owner.Exe == "" {
		owner.Exe, _ = os.Executable()
	}
	if owner.CmdlineHash == "" {
		owner.CmdlineHash = hashCmdline(os.Args)
	}
	return owner
}

func captureOwner(pid int) (Owner, ownerProcessLiveness) {
	info := processOwnerInfo(pid)
	owner := Owner{PID: pid, StartedAt: strings.TrimSpace(info.StartedAt), Exe: strings.TrimSpace(info.Exe)}
	if len(info.Cmdline) > 0 {
		owner.CmdlineHash = hashCmdline(info.Cmdline)
	}
	return owner, info.Liveness
}

func ownerIsCurrent(owner Owner) bool {
	return inspectOwnerState(owner) == ownerCurrent
}

func inspectOwnerState(owner Owner) ownerState {
	if owner.PID <= 0 {
		return ownerUnknown
	}
	live, liveness := captureOwner(owner.PID)
	return ownerStateWithLive(owner, live, liveness, os.Getpid())
}

// ownerStateWithLive separates safe recovery from owner-only authorization.
// Unknown identity always blocks recovery, while only a complete match or a
// wholly uninspectable record minted by this process authorizes staged reads.
func ownerStateWithLive(owner, live Owner, liveness ownerProcessLiveness, currentPID int) ownerState {
	currentToken := owner.PID == currentPID && owner.CreatedBy == currentProcessOwnerToken
	if owner.PID <= 0 || !hasOwnerFingerprint(owner) {
		return ownerUnknown
	}
	if currentToken {
		if !hasOwnerFingerprint(live) {
			return ownerCurrent
		}
		if compareOwnerFingerprint(owner, live) == fingerprintCompleteMatch {
			return ownerCurrent
		}
		// The process-local token makes this record unsafe to recover, but partial
		// or contradictory OS metadata is not enough to authorize an owner read.
		return ownerUnknown
	}
	if liveness == ownerProcessDead {
		return ownerRecoverable
	}
	switch compareOwnerFingerprint(owner, live) {
	case fingerprintMismatch:
		return ownerRecoverable
	case fingerprintCompleteMatch:
		if liveness == ownerProcessLive {
			return ownerLive
		}
	}
	return ownerUnknown
}

type fingerprintState uint8

const (
	fingerprintPartial fingerprintState = iota
	fingerprintCompleteMatch
	fingerprintMismatch
)

func compareOwnerFingerprint(owner, live Owner) fingerprintState {
	if owner.StartedAt != "" && live.StartedAt != "" && owner.StartedAt != live.StartedAt {
		return fingerprintMismatch
	}
	if owner.CmdlineHash != "" && live.CmdlineHash != "" && owner.CmdlineHash != live.CmdlineHash {
		return fingerprintMismatch
	}
	if owner.Exe != "" && live.Exe != "" && !sameExe(owner.Exe, live.Exe) {
		return fingerprintMismatch
	}
	if owner.StartedAt == "" || live.StartedAt == "" || owner.CmdlineHash == "" || live.CmdlineHash == "" || owner.Exe == "" || live.Exe == "" {
		return fingerprintPartial
	}
	return fingerprintCompleteMatch
}

func hasOwnerFingerprint(owner Owner) bool {
	return owner.StartedAt != "" || owner.CmdlineHash != "" || owner.Exe != ""
}

func hashCmdline(args []string) string {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sameExe(left, right string) bool {
	left, right = filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right))
	return left == right || filepath.Base(left) == filepath.Base(right)
}
