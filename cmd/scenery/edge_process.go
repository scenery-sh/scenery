package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func stopStaleUserCaddyEdges(paths localagent.Paths, timeout time.Duration) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	configs := []string{filepath.Clean(paths.EdgeConfigPath)}
	if home, err := os.UserHomeDir(); err == nil {
		configs = append(configs, filepath.Join(home, ".onlava", "agent", "edge", "Caddyfile"))
	}
	for _, process := range parseRuntimeProcesses(string(out)) {
		if process.UID != os.Getuid() || !managedCaddyCommandMatches(process.Command, configs) {
			continue
		}
		owner := localagent.CaptureOwner(process.PID, "stale scenery edge")
		if err := localagent.VerifyOwner(owner); err != nil {
			return fmt.Errorf("verify stale Caddy edge pid %d: %w", process.PID, err)
		}
		if err := signalPID(process.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale Caddy edge pid %d: %w", process.PID, err)
		}
		deadline := time.Now().Add(timeout)
		for processAliveForEdge(process.PID) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAliveForEdge(process.PID) {
			if err := signalPID(process.PID, syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill stale Caddy edge pid %d: %w", process.PID, err)
			}
		}
	}
	return nil
}

func stopStaleUserSceneryAgents(socketPath, routerAddr string, timeout time.Duration) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	for _, process := range parseRuntimeProcesses(string(out)) {
		if process.UID != os.Getuid() || process.PID == os.Getpid() || !edgeAgentCommandMatches(process.Command, socketPath, routerAddr) {
			continue
		}
		owner := localagent.CaptureOwner(process.PID, "stale scenery agent")
		if err := localagent.VerifyOwner(owner); err != nil {
			return fmt.Errorf("verify stale scenery agent pid %d: %w", process.PID, err)
		}
		if err := signalPID(process.PID, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale scenery agent pid %d: %w", process.PID, err)
		}
		deadline := time.Now().Add(timeout)
		for processAliveForEdge(process.PID) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAliveForEdge(process.PID) {
			if err := signalPID(process.PID, syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill stale scenery agent pid %d: %w", process.PID, err)
			}
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

func stopStaleRootCaddyEdge(ownerHome string, timeout time.Duration) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("stale root Caddy cleanup must run as root")
	}
	configPath := filepath.Join(ownerHome, "agent", "edge", "Caddyfile")
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		command := strings.Join(fields[2:], " ")
		if pidErr != nil || uidErr != nil || uid != 0 || pid <= 0 {
			continue
		}
		if !strings.Contains(command, "caddy run") || !strings.Contains(command, "--config "+configPath) {
			continue
		}
		if err := signalPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale root Caddy edge pid %d: %w", pid, err)
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if !processAliveForEdge(pid) {
				return nil
			}
			time.Sleep(50 * time.Millisecond)
		}
		if err := signalPID(pid, syscall.SIGKILL); err != nil {
			return fmt.Errorf("kill stale root Caddy edge pid %d: %w", pid, err)
		}
		return nil
	}
	return nil
}

func stopStaleRootSceneryEdgeAgent(ownerHome, routerAddr string, timeout time.Duration) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("stale root agent cleanup must run as root")
	}
	socketPath := filepath.Join(ownerHome, "run", "agent.sock")
	routerAddr = strings.TrimSpace(routerAddr)
	if routerAddr == "" {
		routerAddr = localagent.RouterAddrFromEnv()
	}
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 3 {
			continue
		}
		pid, pidErr := strconv.Atoi(fields[0])
		uid, uidErr := strconv.Atoi(fields[1])
		command := strings.Join(fields[2:], " ")
		if pidErr != nil || uidErr != nil || uid != 0 || pid <= 0 {
			continue
		}
		if !edgeAgentCommandMatches(command, socketPath, routerAddr) {
			continue
		}
		if err := signalPID(pid, syscall.SIGTERM); err != nil {
			return fmt.Errorf("stop stale root scenery system edge agent pid %d: %w", pid, err)
		}
		deadline := time.Now().Add(timeout)
		for processAliveForEdge(pid) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if processAliveForEdge(pid) {
			if err := signalPID(pid, syscall.SIGKILL); err != nil {
				return fmt.Errorf("kill stale root scenery system edge agent pid %d: %w", pid, err)
			}
		}
	}
	return nil
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

func processAliveForEdge(pid int) bool {
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
	return !processZombieForEdge(pid)
}

func processZombieForEdge(pid int) bool {
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
		return "", fmt.Errorf("pid must be positive")
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
	defer file.Close()
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
