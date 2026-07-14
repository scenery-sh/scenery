package agent

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// AgentLaunchdLabel is the launchd user-agent job that continuously
// supervises the scenery agent control plane and router. It is the required
// availability owner for machines that serve public deploy traffic: launchd
// restarts the agent when it exits, and every scenery stop/start path must
// cooperate with that supervisor instead of racing its KeepAlive respawn.
const AgentLaunchdLabel = "dev.scenery.agent"

// launchdBootstrapRetryWindow bounds retries of launchctl bootstrap after a
// bootout: launchctl bootout returns before launchd has finished tearing the
// old job down, so an immediate bootstrap of the replacement can fail
// transiently with EIO.
const launchdBootstrapRetryWindow = 10 * time.Second

var (
	launchctlRunFunc     = runLaunchctl
	launchAgentsDirFunc  = defaultLaunchAgentsDir
	launchdSleepFunc     = time.Sleep
	launchdUserIDFunc    = os.Getuid
	launchdSupportedFunc = func() bool { return runtime.GOOS == "darwin" }
)

func runLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func defaultLaunchAgentsDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents"), nil
}

// LaunchdAgentStatus reports launchd supervision truth for the scenery agent:
// plist presence is installation, Loaded/Running come from launchd itself.
// Presence of the plist file alone never means the agent is supervised.
type LaunchdAgentStatus struct {
	Supported        bool   `json:"supported"`
	PlistPresent     bool   `json:"installed"`
	SupervisesSocket bool   `json:"supervises_socket"`
	Loaded           bool   `json:"loaded"`
	Running          bool   `json:"running"`
	PID              int    `json:"pid,omitempty"`
	PlistPath        string `json:"path"`
	Label            string `json:"label"`
}

func launchdGUITarget() string {
	return fmt.Sprintf("gui/%d/%s", launchdUserIDFunc(), AgentLaunchdLabel)
}

// AgentLaunchdPlistPath resolves the supervised agent plist path without
// checking whether it exists.
func AgentLaunchdPlistPath() (string, error) {
	dir, err := launchAgentsDirFunc()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AgentLaunchdLabel+".plist"), nil
}

// AgentLaunchdPlist renders the supervised agent job. KeepAlive keeps the
// agent continuously owned by launchd; RunAtLoad starts it at bootstrap and
// at every login. launchd's default PATH is only the system directories, so
// the job pins a PATH that includes the standard Homebrew and local prefixes
// — the agent's dashboard shells out to tools like docker (managed Postgres)
// and codex (Symphony runner) that live there.
func AgentLaunchdPlist(exe string, paths Paths, opts StartOptions) string {
	args := append([]string{exe}, agentProcessArgs(paths, opts)...)
	var argLines strings.Builder
	for _, arg := range args {
		fmt.Fprintf(&argLines, "\t\t<string>%s</string>\n", plistEscape(arg))
	}
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
%s
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ProcessType</key>
	<string>Interactive</string>
	<key>ThrottleInterval</key>
	<integer>2</integer>
	<key>EnvironmentVariables</key>
	<dict>
		<key>PATH</key>
		<string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
	</dict>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, plistEscape(AgentLaunchdLabel), strings.TrimRight(argLines.String(), "\n"), plistEscape(paths.LogPath), plistEscape(paths.LogPath))
}

func plistEscape(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

// InstallAgentLaunchd writes the supervised agent plist and actually loads it:
// installation means a bootstrapped launchd job, not a plist file on disk.
// RunAtLoad starts the agent immediately, so callers must stop any
// unsupervised agent that still holds the agent lock before installing.
func InstallAgentLaunchd(exe string, paths Paths, opts StartOptions) (string, error) {
	if !launchdSupportedFunc() {
		return "", fmt.Errorf("scenery agent launchd supervision is currently supported on macOS")
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(plistPath, []byte(AgentLaunchdPlist(exe, paths, opts)), 0o644); err != nil {
		return "", err
	}
	_, _ = launchctlRunFunc("bootout", launchdGUITarget())
	if err := bootstrapAndStartAgentLaunchd(plistPath); err != nil {
		return "", err
	}
	return plistPath, nil
}

// bootstrapAndStartAgentLaunchd loads the job and then starts it explicitly:
// launchd can pend a RunAtLoad spawn when the job is bootstrapped from a
// non-Aqua context (observed as "pended nondemand spawn = speculative"), so
// bootstrap alone does not guarantee a running agent.
func bootstrapAndStartAgentLaunchd(plistPath string) error {
	if err := retryLaunchctl(launchdBootstrapRetryWindow, "bootstrap", fmt.Sprintf("gui/%d", launchdUserIDFunc()), plistPath); err != nil {
		return err
	}
	if err := KickstartAgentLaunchd(false); err != nil {
		if AgentLaunchdStatusForSocket("").Running {
			return nil
		}
		return err
	}
	return nil
}

// BootstrapAgentLaunchd loads an already-installed supervised agent plist.
// It repairs the "plist present but job unloaded" state without rewriting
// the plist.
func BootstrapAgentLaunchd() error {
	if !launchdSupportedFunc() {
		return fmt.Errorf("scenery agent launchd supervision is currently supported on macOS")
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(plistPath); err != nil {
		return err
	}
	return bootstrapAndStartAgentLaunchd(plistPath)
}

// RemoveAgentLaunchd boots the supervised agent job out of launchd before
// removing its plist, so teardown never leaves a loaded job pointing at a
// deleted plist.
func RemoveAgentLaunchd() (bool, error) {
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return false, err
	}
	if launchdSupportedFunc() {
		_, _ = launchctlRunFunc("bootout", launchdGUITarget())
	}
	err = os.Remove(plistPath)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// KickstartAgentLaunchd asks launchd to (re)start the supervised agent. With
// kill=true the running agent is terminated and respawned atomically, which
// is the cooperative form of `scenery system agent restart` under
// supervision.
func KickstartAgentLaunchd(kill bool) error {
	args := []string{"kickstart"}
	if kill {
		args = append(args, "-k")
	}
	args = append(args, launchdGUITarget())
	out, err := launchctlRunFunc(args...)
	if err != nil {
		return fmt.Errorf("launchctl kickstart %s: %w: %s", launchdGUITarget(), err, strings.TrimSpace(string(out)))
	}
	return nil
}

var launchdPrintPIDRE = regexp.MustCompile(`(?m)^\s*pid = (\d+)\s*$`)
var launchdPrintStateRE = regexp.MustCompile(`(?m)^\s*state = (\S+)\s*$`)

// AgentLaunchdStatusForSocket reports supervision truth for the agent that
// owns socketPath. SupervisesSocket is false when the installed plist manages
// a different agent home (for example test homes), so callers never treat a
// foreign plist as their supervisor.
func AgentLaunchdStatusForSocket(socketPath string) LaunchdAgentStatus {
	status := LaunchdAgentStatus{
		Supported: launchdSupportedFunc(),
		Label:     AgentLaunchdLabel,
	}
	plistPath, err := AgentLaunchdPlistPath()
	if err != nil {
		return status
	}
	status.PlistPath = plistPath
	data, err := os.ReadFile(plistPath)
	if err != nil {
		return status
	}
	status.PlistPresent = true
	socketPath = strings.TrimSpace(socketPath)
	if socketPath != "" {
		status.SupervisesSocket = strings.Contains(string(data), "<string>"+plistEscape(socketPath)+"</string>")
	}
	if !status.Supported {
		return status
	}
	out, err := launchctlRunFunc("print", launchdGUITarget())
	if err != nil {
		return status
	}
	status.Loaded = true
	if match := launchdPrintPIDRE.FindSubmatch(out); match != nil {
		status.PID, _ = strconv.Atoi(string(match[1]))
	}
	if match := launchdPrintStateRE.FindSubmatch(out); match != nil {
		status.Running = string(match[1]) == "running"
	}
	if status.PID > 0 {
		status.Running = true
	}
	return status
}

func retryLaunchctl(window time.Duration, args ...string) error {
	deadline := time.Now().Add(window)
	for {
		out, err := launchctlRunFunc(args...)
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("launchctl %s: %w: %s", args[0], err, strings.TrimSpace(string(out)))
		}
		launchdSleepFunc(250 * time.Millisecond)
	}
}

// startSupervisedAgentProcess routes agent starts through launchd when the
// installed supervised plist manages the requested socket. It returns true
// when launchd owns the start, so callers never spawn an unsupervised agent
// that races the KeepAlive respawn.
func startSupervisedAgentProcess(paths Paths) bool {
	if !launchdSupportedFunc() {
		return false
	}
	status := AgentLaunchdStatusForSocket(paths.SocketPath)
	if !status.PlistPresent || !status.SupervisesSocket {
		return false
	}
	if !status.Loaded {
		return BootstrapAgentLaunchd() == nil
	}
	if err := KickstartAgentLaunchd(false); err != nil {
		// A KeepAlive respawn can win the race with kickstart; a running
		// supervised agent is success regardless of who started it.
		return AgentLaunchdStatusForSocket(paths.SocketPath).Running
	}
	return true
}
