package devprocess

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ProcessInfo is one observed process-table row, not an ownership credential.
type ProcessInfo struct {
	PID     int
	PPID    int
	State   string
	Command string
}

func Inspect(pid int) (ProcessInfo, bool) {
	cmd := exec.Command("ps", "-o", "pid=,ppid=,stat=,command=", "-p", strconv.Itoa(pid))
	output, err := cmd.Output()
	if err != nil {
		return ProcessInfo{}, false
	}
	line := strings.TrimSpace(string(output))
	if line == "" {
		return ProcessInfo{}, false
	}
	parts := strings.Fields(line)
	if len(parts) < 4 {
		return ProcessInfo{}, false
	}
	gotPID, err := strconv.Atoi(parts[0])
	if err != nil {
		return ProcessInfo{}, false
	}
	ppid, err := strconv.Atoi(parts[1])
	if err != nil {
		return ProcessInfo{}, false
	}
	return ProcessInfo{PID: gotPID, PPID: ppid, State: parts[2], Command: strings.Join(parts[3:], " ")}, true
}

func WaitForExit(ctx context.Context, pid int, timeout time.Duration) bool {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if info, ok := Inspect(pid); !ok || strings.Contains(info.State, "Z") {
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

func Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	return !zombie(pid)
}

func zombie(pid int) bool {
	switch runtime.GOOS {
	case "darwin", "linux":
	default:
		return false
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
}
