package workspacetx

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
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

func currentOwner() Owner {
	pid := os.Getpid()
	info := processOwnerInfo(pid)
	owner := Owner{
		PID: pid, StartedAt: strings.TrimSpace(info.StartedAt), Exe: strings.TrimSpace(info.Exe),
		AgentPID: pid, CreatedBy: "change-transaction", RecordedAt: time.Now().UTC(),
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

func verifyOwner(owner Owner) error {
	if owner.PID <= 0 {
		return errors.New("owner pid is missing")
	}
	live := captureOwner(owner.PID)
	if live.StartedAt == "" && live.CmdlineHash == "" && live.Exe == "" {
		return errors.New("owner process is not inspectable")
	}
	if owner.StartedAt != "" && live.StartedAt != "" && owner.StartedAt != live.StartedAt {
		return errors.New("owner process start time changed")
	}
	if owner.CmdlineHash != "" && live.CmdlineHash != "" && owner.CmdlineHash != live.CmdlineHash {
		return errors.New("owner process command fingerprint changed")
	}
	if owner.Exe != "" && live.Exe != "" && !sameExe(owner.Exe, live.Exe) {
		return errors.New("owner process executable changed")
	}
	if owner.StartedAt == "" && owner.CmdlineHash == "" && owner.Exe == "" {
		return errors.New("owner fingerprint is missing")
	}
	return nil
}

func captureOwner(pid int) Owner {
	info := processOwnerInfo(pid)
	owner := Owner{PID: pid, StartedAt: strings.TrimSpace(info.StartedAt), Exe: strings.TrimSpace(info.Exe)}
	if len(info.Cmdline) > 0 {
		owner.CmdlineHash = hashCmdline(info.Cmdline)
	}
	return owner
}

func ownerIsCurrent(owner Owner) bool {
	if owner.PID != os.Getpid() || verifyOwner(owner) != nil {
		return false
	}
	current := currentOwner()
	return owner.StartedAt == current.StartedAt && owner.Exe == current.Exe && owner.CmdlineHash == current.CmdlineHash
}

func hashCmdline(args []string) string {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sameExe(left, right string) bool {
	left, right = filepath.Clean(strings.TrimSpace(left)), filepath.Clean(strings.TrimSpace(right))
	return left == right || filepath.Base(left) == filepath.Base(right)
}
