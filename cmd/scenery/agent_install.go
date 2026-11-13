package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

var (
	agentSupervisorInstallFunc = localagent.InstallAgentLaunchd
	agentSupervisorRemoveFunc  = localagent.RemoveAgentLaunchd
	agentExecutableFunc        = os.Executable
)

func parseAgentSupervisorArgs(name string, args []string) (bool, error) {
	jsonOutput := false
	flags := newCLIFlagSet("system agent " + name)
	registerJSONOutput(flags, &jsonOutput)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return false, err
	}
	return jsonOutput, rejectCLIPositionals(positionals)
}

// agentInstallCommand installs the launchd job that continuously supervises
// the machine agent. A running unsupervised agent is stopped first, because
// the job starts the agent as soon as it is loaded.
func agentInstallCommand(args []string) error {
	jsonOutput, err := parseAgentSupervisorArgs("install", args)
	if err != nil {
		return err
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return err
	}
	if err := localagent.EnsureDirs(paths); err != nil {
		return err
	}
	exe, err := agentExecutableFunc()
	if err != nil {
		return err
	}
	if strings.Contains(filepath.ToSlash(exe), "/.scenery/harness/") {
		return preconditionErrorf("refusing to install the agent supervisor from the worktree harness binary %s; use the installed scenery", exe)
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
	plistPath, err := agentSupervisorInstallFunc(exe, paths, localagent.StartOptions{RouterHTTP: true})
	if err != nil {
		return err
	}
	started, err := waitForAgentStart(ctx, client, health.PID, paths.LogPath, logOffset)
	if err != nil {
		return fmt.Errorf("supervised scenery agent did not become ready after launchd bootstrap: %w", err)
	}
	if jsonOutput {
		return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.agent.supervisor", map[string]any{
			"action": "install", "changed": true, "label": localagent.AgentLaunchdLabel, "path": plistPath, "pid": started.PID,
		}))
	}
	_, _ = fmt.Fprintf(os.Stdout, "installed launchd supervisor %s; scenery agent running as pid %d\n", localagent.AgentLaunchdLabel, started.PID)
	return nil
}

// agentUninstallCommand removes the launchd job. Booting the job out stops
// the agent it supervises; `scenery system agent restart` starts one again.
func agentUninstallCommand(args []string) error {
	jsonOutput, err := parseAgentSupervisorArgs("uninstall", args)
	if err != nil {
		return err
	}
	plistPath, err := localagent.AgentLaunchdPlistPath()
	if err != nil {
		return err
	}
	removed, err := agentSupervisorRemoveFunc()
	if err != nil {
		return err
	}
	if jsonOutput {
		return writeCLIJSON(os.Stdout, withCLIPayloadIdentity("scenery.agent.supervisor", map[string]any{
			"action": "uninstall", "changed": removed, "label": localagent.AgentLaunchdLabel, "path": plistPath,
		}))
	}
	if !removed {
		_, _ = fmt.Fprintf(os.Stdout, "launchd supervisor %s is not installed\n", localagent.AgentLaunchdLabel)
		return nil
	}
	_, _ = fmt.Fprintf(os.Stdout, "removed launchd supervisor %s; the scenery agent is stopped until `scenery system agent restart`\n", localagent.AgentLaunchdLabel)
	return nil
}
