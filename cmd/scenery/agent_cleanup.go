package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
)

type preRebrandCleanupResult struct {
	cliPayloadIdentity
	Home         string `json:"home"`
	StatePresent bool   `json:"state_present"`
	StateRemoved bool   `json:"state_removed"`
	Matched      []int  `json:"matched_pids,omitempty"`
	Stopped      []int  `json:"stopped_pids,omitempty"`
	Skipped      []int  `json:"skipped_unverified_pids,omitempty"`
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
		out, err := exec.Command("ps", "-axo", "pid=,uid=,command=").Output()
		if err != nil {
			return err
		}
		for _, process := range preRebrandProcesses(parseRuntimeProcesses(string(out)), legacyHome, os.Getuid(), os.Getpid()) {
			result.Matched = append(result.Matched, process.PID)
			current, ok := currentRuntimeProcess(process.PID)
			if !ok || current.UID != process.UID || current.Command != process.Command || len(preRebrandProcesses([]runtimeProcess{current}, legacyHome, os.Getuid(), os.Getpid())) != 1 {
				result.Skipped = append(result.Skipped, process.PID)
				continue
			}
			owner := localagent.CaptureOwner(process.PID, "scenery pre-rebrand cleanup")
			if err := localagent.VerifyOwner(owner); err != nil {
				result.Skipped = append(result.Skipped, process.PID)
				continue
			}
			if err := signalPID(process.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
				return fmt.Errorf("stop verified pre-rebrand process pid %d: %w", process.PID, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			for processAliveForEdge(process.PID) && ctx.Err() == nil {
				time.Sleep(50 * time.Millisecond)
			}
			cancel()
			if processAliveForEdge(process.PID) {
				// Verify the same fingerprint again before escalating.
				if err := localagent.VerifyOwner(owner); err != nil {
					result.Skipped = append(result.Skipped, process.PID)
					continue
				}
				if err := signalPID(process.PID, syscall.SIGKILL); err != nil {
					return fmt.Errorf("kill verified pre-rebrand process pid %d: %w", process.PID, err)
				}
			}
			result.Stopped = append(result.Stopped, process.PID)
		}
	}
	if opts.RemoveState && result.StatePresent {
		if len(result.Skipped) > 0 {
			return fmt.Errorf("refusing to remove %s while pre-rebrand processes could not be verified", legacyHome)
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
	if len(result.Stopped) > 0 {
		fmt.Fprintf(stdout, "stopped %d verified pre-rebrand process(es)\n", len(result.Stopped))
	} else {
		fmt.Fprintln(stdout, "no verified pre-rebrand processes found")
	}
	if result.StateRemoved {
		fmt.Fprintf(stdout, "removed pre-rebrand state %s\n", legacyHome)
	} else if result.StatePresent {
		fmt.Fprintf(stdout, "pre-rebrand state remains at %s; rerun with --remove-state to remove it\n", legacyHome)
	}
	return nil
}

func currentRuntimeProcess(pid int) (runtimeProcess, bool) {
	out, err := exec.Command("ps", "-p", fmt.Sprint(pid), "-o", "pid=,uid=,command=").Output()
	if err != nil {
		return runtimeProcess{}, false
	}
	processes := parseRuntimeProcesses(string(out))
	if len(processes) != 1 || processes[0].PID != pid {
		return runtimeProcess{}, false
	}
	return processes[0], true
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
