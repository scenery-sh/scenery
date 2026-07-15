package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Systemd unit names for the Linux public-deploy host. One Linux user (the
// setup invoker, normally root) owns the Scenery agent home; the units run as
// that user via system-level systemd.
const (
	AgentSystemdUnitName        = "scenery-agent.service"
	DeployResumeSystemdUnitName = "scenery-deploy-resume.service"
)

var (
	systemctlRunFunc     = runSystemctl
	systemdUnitDirFunc   = func() string { return "/etc/systemd/system" }
	systemdSupportedFunc = defaultSystemdSupported
)

func runSystemctl(args ...string) ([]byte, error) {
	return exec.Command("systemctl", args...).CombinedOutput()
}

func defaultSystemdSupported() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	info, err := os.Stat("/run/systemd/system")
	return err == nil && info.IsDir()
}

// SystemdSupported reports whether this host runs systemd as PID 1.
func SystemdSupported() bool { return systemdSupportedFunc() }

// AgentSystemdUnitPath resolves the supervised agent unit path.
func AgentSystemdUnitPath() string {
	return filepath.Join(systemdUnitDirFunc(), AgentSystemdUnitName)
}

// AgentSystemdUnit renders the systemd unit that continuously supervises the
// scenery agent, mirroring the macOS launchd job: restart always, start after
// the network, logs to journald.
func AgentSystemdUnit(exe string, paths Paths, opts StartOptions) string {
	args := append([]string{exe}, agentProcessArgs(paths, opts)...)
	home := filepath.Dir(paths.Home)
	return fmt.Sprintf(`[Unit]
Description=Scenery agent control plane and router
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s
Restart=always
RestartSec=2
Environment=HOME=%s
Environment=PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

[Install]
WantedBy=multi-user.target
`, systemdEscapeExec(args), home)
}

// DeployResumeSystemdUnit renders the boot-time oneshot that restarts every
// enabled deploy target after the agent and edge are up.
func DeployResumeSystemdUnit(exe string, paths Paths) string {
	home := filepath.Dir(paths.Home)
	return fmt.Sprintf(`[Unit]
Description=Scenery deploy resume
After=network-online.target %s scenery-edge.service
Wants=network-online.target

[Service]
Type=oneshot
ExecStart=%s deploy resume -o json
Environment=HOME=%s
Environment=PATH=/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin

[Install]
WantedBy=multi-user.target
`, AgentSystemdUnitName, systemdEscapeExec([]string{exe}), home)
}

// systemdEscapeExec renders an ExecStart command line, quoting arguments that
// contain whitespace. Paths under Scenery control never contain quotes.
func systemdEscapeExec(args []string) string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		if strings.ContainsAny(arg, " \t") {
			arg = `"` + arg + `"`
		}
		out = append(out, arg)
	}
	return strings.Join(out, " ")
}

// InstallAgentSystemd writes the supervised agent unit, reloads systemd, and
// enables and (re)starts the job. Installation means a loaded unit, not a
// file on disk.
func InstallAgentSystemd(exe string, paths Paths, opts StartOptions) (string, error) {
	if !systemdSupportedFunc() {
		return "", fmt.Errorf("scenery agent systemd supervision requires a Linux host running systemd")
	}
	unitPath := AgentSystemdUnitPath()
	if err := os.WriteFile(unitPath, []byte(AgentSystemdUnit(exe, paths, opts)), 0o644); err != nil {
		return "", err
	}
	if out, err := systemctlRunFunc("daemon-reload"); err != nil {
		return "", fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := systemctlRunFunc("enable", AgentSystemdUnitName); err != nil {
		return "", fmt.Errorf("systemctl enable %s: %w: %s", AgentSystemdUnitName, err, strings.TrimSpace(string(out)))
	}
	if out, err := systemctlRunFunc("restart", AgentSystemdUnitName); err != nil {
		return "", fmt.Errorf("systemctl restart %s: %w: %s", AgentSystemdUnitName, err, strings.TrimSpace(string(out)))
	}
	return unitPath, nil
}

// InstallDeployResumeSystemd writes and enables the boot-resume oneshot; it
// does not run it immediately.
func InstallDeployResumeSystemd(exe string, paths Paths) (string, error) {
	if !systemdSupportedFunc() {
		return "", fmt.Errorf("scenery deploy resume systemd unit requires a Linux host running systemd")
	}
	unitPath := filepath.Join(systemdUnitDirFunc(), DeployResumeSystemdUnitName)
	if err := os.WriteFile(unitPath, []byte(DeployResumeSystemdUnit(exe, paths)), 0o644); err != nil {
		return "", err
	}
	if out, err := systemctlRunFunc("daemon-reload"); err != nil {
		return "", fmt.Errorf("systemctl daemon-reload: %w: %s", err, strings.TrimSpace(string(out)))
	}
	if out, err := systemctlRunFunc("enable", DeployResumeSystemdUnitName); err != nil {
		return "", fmt.Errorf("systemctl enable %s: %w: %s", DeployResumeSystemdUnitName, err, strings.TrimSpace(string(out)))
	}
	return unitPath, nil
}

// RemoveAgentSystemd disables and stops the supervised agent unit before
// removing its file, along with the deploy-resume oneshot.
func RemoveAgentSystemd() (bool, error) {
	removed := false
	for _, name := range []string{AgentSystemdUnitName, DeployResumeSystemdUnitName} {
		unitPath := filepath.Join(systemdUnitDirFunc(), name)
		if systemdSupportedFunc() {
			_, _ = systemctlRunFunc("disable", "--now", name)
		}
		err := os.Remove(unitPath)
		if err == nil {
			removed = true
		} else if !errors.Is(err, os.ErrNotExist) {
			return removed, err
		}
	}
	if removed && systemdSupportedFunc() {
		_, _ = systemctlRunFunc("daemon-reload")
	}
	return removed, nil
}

// AgentSystemdStatusForSocket reports systemd supervision truth for the agent
// that owns socketPath, using the same status shape as launchd supervision.
func AgentSystemdStatusForSocket(socketPath string) LaunchdAgentStatus {
	status := LaunchdAgentStatus{
		Supported: systemdSupportedFunc(),
		Label:     AgentSystemdUnitName,
		PlistPath: AgentSystemdUnitPath(),
	}
	data, err := os.ReadFile(status.PlistPath)
	if err != nil {
		return status
	}
	status.PlistPresent = true
	socketPath = strings.TrimSpace(socketPath)
	if socketPath != "" {
		status.SupervisesSocket = strings.Contains(string(data), "--socket "+socketPath+" ") ||
			strings.Contains(string(data), `--socket "`+socketPath+`" `)
	}
	if !status.Supported {
		return status
	}
	out, err := systemctlRunFunc("show", AgentSystemdUnitName, "--property=LoadState,ActiveState,MainPID")
	if err != nil {
		return status
	}
	props := parseSystemctlShow(string(out))
	status.Loaded = props["LoadState"] == "loaded"
	status.Running = props["ActiveState"] == "active"
	if pid, err := strconv.Atoi(props["MainPID"]); err == nil && pid > 0 {
		status.PID = pid
	}
	return status
}

func parseSystemctlShow(out string) map[string]string {
	props := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			props[key] = value
		}
	}
	return props
}

// DeployResumeSystemdStatus reports the boot-resume oneshot unit: file
// presence and whether systemd loaded it.
func DeployResumeSystemdStatus() (installed, loaded bool, path string) {
	path = filepath.Join(systemdUnitDirFunc(), DeployResumeSystemdUnitName)
	if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
		return false, false, path
	}
	installed = true
	if !systemdSupportedFunc() {
		return installed, false, path
	}
	out, err := systemctlRunFunc("show", DeployResumeSystemdUnitName, "--property=LoadState,UnitFileState")
	if err != nil {
		return installed, false, path
	}
	props := parseSystemctlShow(string(out))
	loaded = props["LoadState"] == "loaded" && props["UnitFileState"] == "enabled"
	return installed, loaded, path
}

// startSystemdSupervisedAgent routes agent starts through systemd when the
// installed unit supervises the requested socket, so callers never spawn an
// unsupervised agent racing the Restart=always respawn.
func startSystemdSupervisedAgent(paths Paths) bool {
	if !systemdSupportedFunc() {
		return false
	}
	status := AgentSystemdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.SupervisesSocket {
		return false
	}
	if _, err := systemctlRunFunc("restart", AgentSystemdUnitName); err != nil {
		return AgentSystemdStatusForSocket(paths.SocketPath).Running
	}
	return true
}
