package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
)

type runtimeProcess struct {
	PID     int
	UID     int
	Command string
}

func parseRuntimeProcesses(output string) []runtimeProcess {
	var processes []runtimeProcess
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		if pidErr == nil && uidErr == nil && pid > 0 {
			processes = append(processes, runtimeProcess{PID: pid, UID: uid, Command: strings.Join(fields[2:], " ")})
		}
	}
	return processes
}

// stopRecordedStaleAgent stops the agent recorded as holding this home's
// agent lock when it no longer answers on its socket. It acts only on that
// record and only while the live process still verifies against it; a process
// that merely looks like an agent, or shares the router address, belongs to
// someone else.
func stopRecordedStaleAgent(paths localagent.Paths, timeout time.Duration) error {
	record, err := localagent.LoadAgentOwner(paths)
	if err != nil {
		return nil
	}
	owner := record.Owner
	if owner.PID == os.Getpid() || localagent.VerifyOwner(owner) != nil {
		return nil
	}
	if err := signalPID(owner.PID, syscall.SIGTERM); err != nil {
		return fmt.Errorf("stop stale scenery agent pid %d: %w", owner.PID, err)
	}
	deadline := time.Now().Add(timeout)
	for localagent.VerifyOwner(owner) == nil && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if localagent.VerifyOwner(owner) == nil {
		if err := signalPID(owner.PID, syscall.SIGKILL); err != nil {
			return fmt.Errorf("kill stale scenery agent pid %d: %w", owner.PID, err)
		}
	}
	return nil
}

func managedCaddyCommandMatches(command string, configPaths []string) bool {
	if !strings.Contains(command, "caddy run") {
		return false
	}
	for _, configPath := range configPaths {
		if strings.Contains(command, "--config "+filepath.Clean(configPath)) {
			return true
		}
	}
	return false
}

func stopEdge(paths localagent.Paths, timeout time.Duration) error {
	return edgelifecycle.Stop(paths, timeout)
}

func signalPID(pid int, signal os.Signal) error {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	if err := proc.Signal(signal); err != nil && !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}

func processUID(pid int) (int, error) {
	if pid <= 0 {
		return 0, fmt.Errorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "uid=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(out)))
}

func processStartTime(pid int) (string, error) {
	if pid <= 0 {
		return "", fmt.Errorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func processCommand(pid int) (string, error) {
	if pid <= 0 {
		return "", usageErrorf("pid must be positive")
	}
	out, err := exec.Command("ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func tailFileFromOffset(path string, offset, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err == nil {
		if offset < 0 {
			offset = 0
		}
		if offset > info.Size() {
			offset = info.Size()
		}
		if info.Size()-offset > limit {
			offset = info.Size() - limit
		}
		_, _ = file.Seek(offset, io.SeekStart)
	}
	data, err := io.ReadAll(io.LimitReader(file, limit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

func splitHostPort(addr string) (string, string) {
	host, port, err := net.SplitHostPort(strings.TrimSpace(addr))
	if err != nil {
		return "", ""
	}
	return host, port
}
