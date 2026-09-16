package devprocess

import (
	"context"
	"os"
	"os/exec"
	"runtime"
	"slices"
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
	info, ok := InspectAll([]int{pid})[pid]
	return info, ok
}

// InspectAll observes every named process with one process-table read. A
// session names dozens of processes, and a separate read for each dominated
// every status report. Processes that are gone are absent from the result.
func InspectAll(pids []int) map[int]ProcessInfo {
	selected := make([]string, 0, len(pids))
	for _, pid := range pids {
		if pid > 0 {
			selected = append(selected, strconv.Itoa(pid))
		}
	}
	if len(selected) == 0 {
		return map[int]ProcessInfo{}
	}
	// ps exits non-zero when any selected process is gone but still lists the
	// others.
	output, _ := exec.Command("ps", "-o", "pid=,ppid=,stat=,command=", "-p", strings.Join(selected, ",")).Output()
	rows := parseProcessRows(string(output))
	for pid := range rows {
		if !slices.Contains(selected, strconv.Itoa(pid)) {
			delete(rows, pid)
		}
	}
	return rows
}

func parseProcessRows(output string) map[int]ProcessInfo {
	rows := map[int]ProcessInfo{}
	for line := range strings.Lines(output) {
		parts := strings.Fields(line)
		if len(parts) < 4 {
			continue
		}
		pid, err := strconv.Atoi(parts[0])
		if err != nil {
			continue
		}
		ppid, err := strconv.Atoi(parts[1])
		if err != nil {
			continue
		}
		rows[pid] = ProcessInfo{PID: pid, PPID: ppid, State: parts[2], Command: strings.Join(parts[3:], " ")}
	}
	return rows
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
