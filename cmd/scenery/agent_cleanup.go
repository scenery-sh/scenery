package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type preRebrandCleanupResult struct {
	cliPayloadIdentity
	Home         string `json:"home"`
	StatePresent bool   `json:"state_present"`
	StateRemoved bool   `json:"state_removed"`
	// Running names legacy processes observed using the pre-rebrand paths.
	// This Scenery did not record them as its own, so it never signals them.
	Running []int `json:"running_pids,omitempty"`
}

func agentCleanupCommand(args []string) error {
	opts, err := parseAgentCleanupArgs(args)
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	return runAgentCleanup(os.Stdout, filepath.Join(home, ".onlava"), opts)
}

func parseAgentCleanupArgs(args []string) (agentCleanupOptions, error) {
	var opts agentCleanupOptions
	flags := newCLIFlagSet("agent cleanup")
	flags.BoolVar(&opts.RemoveState, "remove-state", false, "")
	registerJSONOutput(flags, &opts.JSON)
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return agentCleanupOptions{}, err
	}
	if err := rejectCLIPositionals(positionals); err != nil {
		return agentCleanupOptions{}, err
	}
	return opts, nil
}

func runAgentCleanup(stdout io.Writer, legacyHome string, opts agentCleanupOptions) error {
	return runAgentCleanupWithProcessLister(stdout, legacyHome, opts, listRuntimeProcesses)
}

type agentCleanupProcessLister func() ([]runtimeProcess, error)

func listRuntimeProcesses() ([]runtimeProcess, error) {
	out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
	if err != nil {
		return nil, err
	}
	return parseRuntimeProcesses(string(out)), nil
}

func runAgentCleanupWithProcessLister(stdout io.Writer, legacyHome string, opts agentCleanupOptions, listProcesses agentCleanupProcessLister) error {
	legacyHome = filepath.Clean(legacyHome)
	result := preRebrandCleanupResult{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.agent.cleanup"),
		Home:               legacyHome,
	}
	if _, err := os.Lstat(legacyHome); err == nil {
		result.StatePresent = true
	} else if !os.IsNotExist(err) {
		return err
	}
	if runtime.GOOS != "windows" {
		processes, err := listProcesses()
		if err != nil {
			return err
		}
		for _, process := range preRebrandProcesses(processes, legacyHome, os.Getuid(), os.Getpid()) {
			result.Running = append(result.Running, process.PID)
		}
	}
	if opts.RemoveState && result.StatePresent {
		if len(result.Running) > 0 {
			return preconditionErrorf("refusing to remove %s while pre-rebrand processes %v still use it; stop them first", legacyHome, result.Running)
		}
		if err := removePreRebrandState(legacyHome); err != nil {
			return err
		}
		result.StateRemoved = true
		result.StatePresent = false
	}
	if opts.JSON {
		return writeCLIJSON(stdout, result)
	}
	if len(result.Running) > 0 {
		_, _ = fmt.Fprintf(stdout, "pre-rebrand processes still running (stop them yourself; Scenery did not record them): %v\n", result.Running)
	} else {
		_, _ = fmt.Fprintln(stdout, "no pre-rebrand processes found")
	}
	if result.StateRemoved {
		_, _ = fmt.Fprintf(stdout, "removed pre-rebrand state %s\n", legacyHome)
	} else if result.StatePresent {
		_, _ = fmt.Fprintf(stdout, "pre-rebrand state remains at %s; rerun with --remove-state to remove it\n", legacyHome)
	}
	return nil
}

func preRebrandProcesses(processes []runtimeProcess, legacyHome string, uid, selfPID int) []runtimeProcess {
	legacyHome = filepath.Clean(legacyHome)
	config := filepath.Join(legacyHome, "agent", "edge", "Caddyfile")
	socket := filepath.Join(legacyHome, "run", "agent.sock")
	var matches []runtimeProcess
	for _, process := range processes {
		if process.UID != uid || process.PID == selfPID {
			continue
		}
		if managedCaddyCommandMatches(process.Command, []string{config}) || commandFlagPathMatches(process.Command, "--socket", socket) {
			matches = append(matches, process)
		}
	}
	return matches
}

func commandFlagPathMatches(command, flag, path string) bool {
	fields := strings.Fields(command)
	for i, field := range fields {
		if field == flag && i+1 < len(fields) && filepath.Clean(fields[i+1]) == filepath.Clean(path) {
			return true
		}
		if strings.HasPrefix(field, flag+"=") && filepath.Clean(strings.TrimPrefix(field, flag+"=")) == filepath.Clean(path) {
			return true
		}
	}
	return false
}

func removePreRebrandState(path string) error {
	path = filepath.Clean(path)
	if filepath.Base(path) != ".onlava" || path == string(filepath.Separator)+".onlava" {
		return fmt.Errorf("refusing to remove unexpected pre-rebrand state path %s", path)
	}
	return os.RemoveAll(path)
}
