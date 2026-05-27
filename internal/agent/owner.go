package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func CurrentOwner(createdBy string) Owner {
	return CaptureOwner(os.Getpid(), createdBy)
}

func CaptureOwner(pid int, createdBy string) Owner {
	if pid <= 0 {
		return Owner{}
	}
	info := processOwnerInfo(pid)
	owner := Owner{
		PID:        pid,
		StartedAt:  strings.TrimSpace(info.StartedAt),
		Exe:        strings.TrimSpace(info.Exe),
		AgentPID:   os.Getpid(),
		CreatedBy:  strings.TrimSpace(createdBy),
		RecordedAt: time.Now().UTC(),
	}
	if len(info.Cmdline) > 0 {
		owner.CmdlineHash = hashCmdline(info.Cmdline)
	}
	if pid == os.Getpid() {
		if owner.Exe == "" {
			if exe, err := os.Executable(); err == nil {
				owner.Exe = exe
			}
		}
		if owner.CmdlineHash == "" {
			owner.CmdlineHash = hashCmdline(os.Args)
		}
	}
	return owner
}

func OwnerFromRequest(pid int, owner Owner, createdBy string) Owner {
	if owner.PID <= 0 {
		owner.PID = pid
	}
	if owner.PID <= 0 {
		return Owner{}
	}
	if owner.StartedAt == "" && owner.Exe == "" && owner.CmdlineHash == "" {
		captured := CaptureOwner(owner.PID, firstNonEmpty(owner.CreatedBy, createdBy))
		if captured.PID > 0 {
			if owner.AgentPID == 0 {
				owner.AgentPID = captured.AgentPID
			}
			if owner.RecordedAt.IsZero() {
				owner.RecordedAt = captured.RecordedAt
			}
			owner.StartedAt = captured.StartedAt
			owner.Exe = captured.Exe
			owner.CmdlineHash = captured.CmdlineHash
		}
	}
	if owner.AgentPID == 0 {
		owner.AgentPID = os.Getpid()
	}
	if owner.CreatedBy == "" {
		owner.CreatedBy = strings.TrimSpace(createdBy)
	}
	if owner.RecordedAt.IsZero() {
		owner.RecordedAt = time.Now().UTC()
	}
	return owner
}

func VerifyOwner(owner Owner) error {
	if owner.PID <= 0 {
		return errors.New("owner pid is missing")
	}
	live := CaptureOwner(owner.PID, "")
	if live.PID <= 0 {
		return errors.New("owner process is not inspectable")
	}
	if owner.StartedAt != "" && live.StartedAt != "" && owner.StartedAt != live.StartedAt {
		return errors.New("owner process start time changed")
	}
	if owner.CmdlineHash != "" && live.CmdlineHash != "" && owner.CmdlineHash != live.CmdlineHash {
		return errors.New("owner process command fingerprint changed")
	}
	if owner.Exe != "" && live.Exe != "" && !sameOwnerExe(owner.Exe, live.Exe) {
		return errors.New("owner process executable changed")
	}
	if owner.StartedAt == "" && owner.CmdlineHash == "" && owner.Exe == "" {
		return errors.New("owner fingerprint is missing")
	}
	return nil
}

func ownerForSignal(pid int, owner Owner) (Owner, error) {
	if owner.PID <= 0 {
		owner.PID = pid
	}
	if owner.PID <= 0 {
		return Owner{}, errors.New("owner pid is missing")
	}
	if err := VerifyOwner(owner); err != nil {
		return owner, err
	}
	return owner, nil
}

func hashCmdline(args []string) string {
	var b strings.Builder
	for i, arg := range args {
		if i > 0 {
			b.WriteByte(0)
		}
		b.WriteString(arg)
	}
	sum := sha256.Sum256([]byte(b.String()))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sameOwnerExe(a, b string) bool {
	a = filepath.Clean(strings.TrimSpace(a))
	b = filepath.Clean(strings.TrimSpace(b))
	if a == b {
		return true
	}
	return filepath.Base(a) == filepath.Base(b)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
