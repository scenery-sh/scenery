package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	edgelifecycle "scenery.sh/internal/edge"
)

const deployResumeLaunchAgentLabel = "dev.scenery.deploy-resume"

func runDeployLaunchctl(args ...string) ([]byte, error) {
	return exec.Command("launchctl", args...).CombinedOutput()
}

func deployResumeLaunchAgentTarget() string {
	return fmt.Sprintf("gui/%d/%s", os.Getuid(), deployResumeLaunchAgentLabel)
}

func deployResumeLaunchAgentLoaded() bool {
	return deployResumeLaunchAgentStatus().Loaded
}

func deployResumeLaunchAgentStatus() deployLaunchAgentStatus {
	status := deployLaunchAgentStatus{}
	if runtime.GOOS != "darwin" {
		return status
	}
	out, err := deployLaunchctlFunc("print", deployResumeLaunchAgentTarget())
	if err != nil {
		return status
	}
	status.Loaded = true
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case strings.HasPrefix(line, "state ="):
			status.State = strings.TrimSpace(strings.TrimPrefix(line, "state ="))
		case strings.HasPrefix(line, "last exit code ="):
			if code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "last exit code ="))); err == nil {
				status.LastExitCode = &code
			}
		case strings.HasPrefix(line, "last exit status ="):
			if code, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "last exit status ="))); err == nil {
				status.LastExitCode = &code
			}
		}
	}
	return status
}

func deployLaunchAgentStatusFor() deployLaunchAgentStatus {
	if deployServiceManagerFunc() == "systemd" {
		installed, loaded, path := localagent.DeployResumeSystemdStatus()
		return deployLaunchAgentStatus{Installed: installed, Loaded: loaded, Path: path}
	}
	path := deployResumeLaunchAgentPath()
	_, err := os.Stat(path)
	installed := err == nil
	status := deployLaunchAgentStatus{Installed: installed, Path: path}
	if installed {
		observed := deployResumeLaunchAgentStatusFunc()
		status.Loaded = observed.Loaded
		status.State = observed.State
		status.LastExitCode = observed.LastExitCode
	}
	return status
}

func deployResumeLaunchAgentPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "~/Library/LaunchAgents/dev.scenery.deploy-resume.plist"
	}
	return filepath.Join(home, "Library", "LaunchAgents", "dev.scenery.deploy-resume.plist")
}

func installDeployResumeLaunchAgent(paths localagent.Paths) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	if deployExecutableIsHarness(exe) {
		return fmt.Errorf("refusing to install deploy resume LaunchAgent from harness binary %s", exe)
	}
	plistPath := deployResumeLaunchAgentPath()
	if err := os.MkdirAll(filepath.Dir(plistPath), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(plistPath, []byte(deployResumeLaunchAgentPlist(exe, paths.DeployResumeLogPath)), 0o644); err != nil {
		return err
	}
	// A plist on disk recovers nothing: installation means launchd loaded the
	// job. launchd can pend a RunAtLoad spawn when bootstrapped from a
	// non-Aqua context, so kickstart fires the one idempotent resume run
	// explicitly, proving the job is actually runnable.
	_, _ = deployLaunchctlFunc("bootout", deployResumeLaunchAgentTarget())
	if err := retryEdgeHelperLaunchctl(edgeHelperLaunchctlRetryWindow, time.Sleep, deployLaunchctlFunc, "bootstrap", fmt.Sprintf("gui/%d", os.Getuid()), plistPath); err != nil {
		return err
	}
	if out, err := deployLaunchctlFunc("kickstart", deployResumeLaunchAgentTarget()); err != nil {
		return fmt.Errorf("launchctl kickstart %s: %w: %s", deployResumeLaunchAgentTarget(), err, strings.TrimSpace(string(out)))
	}
	status := deployResumeLaunchAgentStatusFunc()
	if !status.Loaded {
		return fmt.Errorf("deploy resume LaunchAgent was accepted by launchctl but is not loaded")
	}
	if status.failed() {
		return fmt.Errorf("deploy resume LaunchAgent completed with exit code %d; inspect %s", *status.LastExitCode, paths.DeployResumeLogPath)
	}
	return nil
}

// installDeployAgentSupervisor hands the running agent over to launchd
// ownership: any unsupervised agent is stopped first so the bootstrapped
// KeepAlive job acquires the agent lock instead of crash-looping against it.
func installDeployAgentSupervisor(paths localagent.Paths) error {
	exe, err := deployPrivilegedHelperExecutableFunc()
	if err != nil {
		return err
	}
	if deployExecutableIsHarness(exe) {
		return fmt.Errorf("refusing to install scenery agent supervisor LaunchAgent from harness binary %s", exe)
	}
	client := localagent.NewClient(paths.SocketPath)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	health, running := currentAgentHealth(ctx, client)
	logOffset := fileSize(paths.LogPath)
	if running && health.PID > 0 {
		if err := signalAgentPID(health.PID); err != nil {
			return fmt.Errorf("stop scenery agent pid %d: %w", health.PID, err)
		}
		if err := waitForAgentStop(ctx, client, health.PID); err != nil {
			return err
		}
	}
	if _, err := localagent.InstallAgentLaunchd(exe, paths, localagent.StartOptions{RouterHTTP: true}); err != nil {
		return err
	}
	if _, err := waitForAgentStart(ctx, client, health.PID, paths.LogPath, logOffset); err != nil {
		return fmt.Errorf("supervised scenery agent did not become ready after launchd bootstrap: %w", err)
	}
	return nil
}

func deployExecutableIsHarness(exe string) bool {
	return strings.Contains(filepath.Clean(exe), string(os.PathSeparator)+".scenery"+string(os.PathSeparator)+"harness"+string(os.PathSeparator))
}

func deployResumeLaunchAgentPlist(exe, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>dev.scenery.deploy-resume</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>deploy</string>
		<string>resume</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<false/>
	<key>StandardOutPath</key>
	<string>%s</string>
	<key>StandardErrorPath</key>
	<string>%s</string>
</dict>
</plist>
`, edgelifecycle.EscapePlistString(exe), edgelifecycle.EscapePlistString(logPath), edgelifecycle.EscapePlistString(logPath))
}

func removeDeployResumeLaunchAgent() (bool, error) {
	// Boot the job out before removing the plist so launchd never keeps a
	// loaded job whose plist is gone.
	_, _ = deployLaunchctlFunc("bootout", deployResumeLaunchAgentTarget())
	err := os.Remove(deployResumeLaunchAgentPath())
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}
