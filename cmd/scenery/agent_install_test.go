package main

import (
	"errors"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
)

func TestParseAgentSupervisorArgs(t *testing.T) {
	if jsonOutput, err := parseAgentSupervisorArgs("install", []string{"-o", "json"}); err != nil || !jsonOutput {
		t.Fatalf("json output = %v, %v", jsonOutput, err)
	}
	if _, err := parseAgentSupervisorArgs("install", []string{"extra"}); err == nil {
		t.Fatal("a positional argument was accepted")
	}
	if _, err := parseAgentSupervisorArgs("uninstall", []string{"--socket", "/tmp/x"}); err == nil {
		t.Fatal("an agent start flag was accepted")
	}
}

func TestAgentInstallRefusesTheHarnessBinary(t *testing.T) {
	isolateCommandAgentHomeAt(t, t.TempDir())
	previousExe, previousInstall := agentExecutableFunc, agentSupervisorInstallFunc
	t.Cleanup(func() { agentExecutableFunc, agentSupervisorInstallFunc = previousExe, previousInstall })
	agentExecutableFunc = func() (string, error) { return "/repo/.scenery/harness/bin/scenery", nil }
	agentSupervisorInstallFunc = func(string, localagent.Paths, localagent.StartOptions) (string, error) {
		t.Fatal("the supervisor was installed from a harness binary")
		return "", nil
	}
	err := agentInstallCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "harness binary") {
		t.Fatalf("install from harness = %v", err)
	}
}

func TestAgentInstallReportsAFailedBootstrap(t *testing.T) {
	isolateCommandAgentHomeAt(t, t.TempDir())
	previousExe, previousInstall := agentExecutableFunc, agentSupervisorInstallFunc
	t.Cleanup(func() { agentExecutableFunc, agentSupervisorInstallFunc = previousExe, previousInstall })
	agentExecutableFunc = func() (string, error) { return "/usr/local/bin/scenery", nil }
	var gotExe string
	var gotOptions localagent.StartOptions
	agentSupervisorInstallFunc = func(exe string, _ localagent.Paths, options localagent.StartOptions) (string, error) {
		gotExe, gotOptions = exe, options
		return "", errors.New("bootstrap refused")
	}
	err := agentInstallCommand(nil)
	if err == nil || !strings.Contains(err.Error(), "bootstrap refused") {
		t.Fatalf("install = %v", err)
	}
	if gotExe != "/usr/local/bin/scenery" || !gotOptions.RouterHTTP {
		t.Fatalf("install arguments = %q %+v", gotExe, gotOptions)
	}
}

func TestAgentUninstallReportsWhetherAJobWasRemoved(t *testing.T) {
	previous := agentSupervisorRemoveFunc
	t.Cleanup(func() { agentSupervisorRemoveFunc = previous })
	for _, removed := range []bool{true, false} {
		agentSupervisorRemoveFunc = func() (bool, error) { return removed, nil }
		if err := agentUninstallCommand(nil); err != nil {
			t.Fatalf("uninstall removed=%v: %v", removed, err)
		}
	}
	agentSupervisorRemoveFunc = func() (bool, error) { return false, errors.New("plist is busy") }
	if err := agentUninstallCommand(nil); err == nil || !strings.Contains(err.Error(), "plist is busy") {
		t.Fatalf("uninstall failure = %v", err)
	}
}
