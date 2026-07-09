package edge

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
	"scenery.sh/internal/envpolicy"
)

const defaultStartupSettle = 1500 * time.Millisecond

// StartConfig describes one managed Caddy edge process. Start owns process
// startup and persistence of the matching edge and helper-target state.
type StartConfig struct {
	Binary         string
	Paths          localagent.Paths
	PublicAddr     string
	TargetAddr     string
	HTTPTargetAddr string
	AdminSocket    string
	UpstreamAddr   string
	StartupSettle  time.Duration
}

// Start launches the configured Caddy edge and records its running state.
func Start(config StartConfig) error {
	settle := config.StartupSettle
	if settle <= 0 {
		settle = defaultStartupSettle
	}
	logOffset := fileSize(config.Paths.EdgeLogPath)
	logFile, err := os.OpenFile(config.Paths.EdgeLogPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	cmd := exec.Command(config.Binary, "run", "--config", config.Paths.EdgeConfigPath, "--adapter", "caddyfile")
	cmd.Env = envpolicy.Environ()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.Stdin = nil
	configureDetachedChildProcess(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- cmd.Wait()
	}()
	if err := waitForStartup(exitCh, config.Paths.EdgeLogPath, logOffset, settle); err != nil {
		_ = os.Remove(config.AdminSocket)
		_ = logFile.Close()
		return err
	}
	startedAt, _ := processStartTime(cmd.Process.Pid)
	target := localagent.EdgeTargetState{
		Kind:           localagent.EdgeKindCaddy,
		TargetAddr:     config.TargetAddr,
		HTTPTargetAddr: config.HTTPTargetAddr,
		PID:            cmd.Process.Pid,
		OwnerUID:       os.Getuid(),
		OwnerGID:       os.Getgid(),
		ProcessStart:   startedAt,
		Executable:     config.Binary,
		UpdatedAt:      time.Now().UTC(),
	}
	if err := localagent.WriteEdgeTargetState(config.Paths.EdgeTargetPath, target); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	state := localagent.EdgeState{
		Kind:         localagent.EdgeKindCaddy,
		Status:       localagent.EdgeStatusRunning,
		PID:          cmd.Process.Pid,
		PublicAddr:   config.PublicAddr,
		PublicScheme: "https",
		HTTPSListen:  config.TargetAddr,
		UpstreamAddr: config.UpstreamAddr,
		AdminSocket:  config.AdminSocket,
		ConfigPath:   config.Paths.EdgeConfigPath,
		LogPath:      config.Paths.EdgeLogPath,
		UpdatedAt:    time.Now().UTC(),
	}
	if err := localagent.WriteEdgeState(config.Paths.EdgeStatePath, state); err != nil {
		_ = signalPID(cmd.Process.Pid, syscall.SIGTERM)
		_ = logFile.Close()
		return err
	}
	_ = logFile.Close()
	return nil
}

// Stop terminates the Caddy process recorded in paths.
func Stop(paths localagent.Paths, timeout time.Duration) error {
	state, err := localagent.LoadEdgeState(paths.EdgeStatePath)
	if err != nil {
		return err
	}
	if state.PID <= 0 || !processAlive(state.PID) {
		return nil
	}
	if err := signalPID(state.PID, syscall.SIGTERM); err != nil {
		return err
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processAlive(state.PID) {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timed out waiting for Caddy edge pid %d to stop", state.PID)
}

// Reload applies a Caddyfile through the configured admin socket.
func Reload(binary, configPath, adminSocket string) error {
	cmd := exec.Command(binary, reloadArgs(configPath, adminSocket)...)
	cmd.Env = envpolicy.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("caddy reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// TrustLocalCA starts a temporary admin-only Caddy instance and installs its
// local CA in the system trust store.
func TrustLocalCA(binary string, stdout, stderr io.Writer) error {
	dir, err := os.MkdirTemp("", "scenery-caddy-trust-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	adminSocket := filepath.Join(dir, "admin.sock")
	configPath := filepath.Join(dir, "Caddyfile")
	logPath := filepath.Join(dir, "caddy.log")
	if err := os.WriteFile(configPath, []byte(trustConfig(adminSocket)), 0o600); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	run := exec.Command(binary, "run", "--config", configPath, "--adapter", "caddyfile")
	run.Env = envpolicy.Environ()
	run.Stdout = logFile
	run.Stderr = logFile
	run.Stdin = nil
	configureChildProcess(run)
	if err := run.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	exitCh := make(chan error, 1)
	go func() {
		exitCh <- run.Wait()
	}()
	if err := waitForAdminSocket(adminSocket, exitCh, logPath, 15*time.Second); err != nil {
		_ = logFile.Close()
		return err
	}
	trust := exec.Command(binary, "trust", "--config", configPath, "--adapter", "caddyfile")
	trust.Env = envpolicy.Environ()
	trust.Stdout = stdout
	trust.Stderr = stderr
	err = trust.Run()
	if stopErr := stopStartedProcess(run.Process.Pid, exitCh, 2*time.Second); err == nil {
		err = stopErr
	}
	_ = logFile.Close()
	return err
}

func reloadArgs(configPath, adminSocket string) []string {
	return []string{"reload", "--config", configPath, "--adapter", "caddyfile", "--address", "unix//" + adminSocket}
}

func trustConfig(adminSocket string) string {
	return fmt.Sprintf(`{
	local_certs
	admin unix//%s
}
`, adminSocket)
}

func waitForAdminSocket(adminSocket string, exitCh <-chan error, logPath string, timeout time.Duration) error {
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case err := <-exitCh:
			tail := tailFile(logPath, 0, 4096)
			if tail != "" {
				return fmt.Errorf("temporary Caddy trust server exited during startup: %s", tail)
			}
			if err != nil {
				return fmt.Errorf("temporary Caddy trust server exited during startup: %w", err)
			}
			return fmt.Errorf("temporary Caddy trust server exited during startup")
		case <-deadline.C:
			tail := tailFile(logPath, 0, 4096)
			if tail != "" {
				return fmt.Errorf("temporary Caddy trust server did not expose admin socket %s within %s: %s", adminSocket, timeout, tail)
			}
			return fmt.Errorf("temporary Caddy trust server did not expose admin socket %s within %s", adminSocket, timeout)
		case <-ticker.C:
			conn, err := net.DialTimeout("unix", adminSocket, 50*time.Millisecond)
			if err == nil {
				_ = conn.Close()
				return nil
			}
		}
	}
}

func stopStartedProcess(pid int, exitCh <-chan error, timeout time.Duration) error {
	if pid <= 0 {
		return nil
	}
	_ = signalPID(pid, syscall.SIGTERM)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case err := <-exitCh:
		return err
	case <-timer.C:
		_ = signalPID(pid, syscall.SIGKILL)
		return fmt.Errorf("timed out waiting for temporary Caddy trust server pid %d to stop", pid)
	}
}

func waitForStartup(exitCh <-chan error, logPath string, logOffset int64, settle time.Duration) error {
	timer := time.NewTimer(settle)
	defer timer.Stop()
	select {
	case err := <-exitCh:
		tail := tailFile(logPath, logOffset, 4096)
		if tail != "" {
			return fmt.Errorf("Caddy edge exited during startup: %s", tail)
		}
		if err != nil {
			return fmt.Errorf("Caddy edge exited during startup: %w", err)
		}
		return fmt.Errorf("Caddy edge exited during startup")
	case <-timer.C:
		return nil
	}
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

func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil || proc.Signal(syscall.Signal(0)) != nil {
		return false
	}
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		return true
	}
	out, err := exec.Command("ps", "-o", "stat=", "-p", strconv.Itoa(pid)).Output()
	return err != nil || !strings.HasPrefix(strings.TrimSpace(string(out)), "Z")
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

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func tailFile(path string, offset, limit int64) string {
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	if info, err := file.Stat(); err == nil {
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
