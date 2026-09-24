package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
)

type worktreeStatusEntry struct {
	localagent.WorktreeDiscovery
	Sessions []localagent.Session `json:"sessions"`
}

func runWorktreeStatus(ctx context.Context, stdout io.Writer, args []string) error {
	opts, err := parseStatusArgs(args)
	if err != nil {
		return err
	}
	for {
		entries, err := inspectWorktreeOwners(ctx, opts.AppRoot)
		if err != nil {
			return err
		}
		// A local agent whose starts keep failing is shown first: routing,
		// domains and every session behind them depend on it.
		incident, incidentErr := localAgentStartIncident()
		if opts.JSON {
			payload := map[string]any{"worktrees": entries}
			if incidentErr == nil {
				payload["local_agent_start"] = incident
			}
			if err := writeCLIJSON(stdout, withCLIPayloadIdentity("scenery.agent.status", payload)); err != nil {
				return err
			}
		} else {
			if incidentErr == nil {
				_, _ = fmt.Fprintf(stdout, "local agent: %s after %d failed start(s) since %s (%s): %s\n", strings.ToUpper(incident.State), incident.Attempts, incident.FirstAt.Format(time.RFC3339), incident.Class, incident.Cause)
			}
			for _, entry := range entries {
				_, _ = fmt.Fprintf(stdout, "%s\t%s\n", firstNonEmpty(entry.AppRoot, entry.Key), entry.Status)
				writeStatusTable(stdout, entry.Sessions, nil)
			}
		}
		if !opts.Watch {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

func localAgentStartIncident() (localagent.StartIncident, error) {
	paths, err := commandAgentPaths()
	if err != nil {
		return localagent.StartIncident{}, err
	}
	return localagent.LoadStartIncident(paths)
}

func inspectWorktreeOwners(ctx context.Context, root string) ([]worktreeStatusEntry, error) {
	machine, err := commandAgentPaths()
	if err != nil {
		return nil, err
	}
	var discovered []localagent.WorktreeDiscovery
	if root == "" {
		discovered, err = localagent.DiscoverWorktrees(machine.Home)
		if err != nil {
			return nil, err
		}
	} else {
		root, err = resolveStatusAppRoot(root)
		if err != nil {
			return nil, err
		}
		paths, err := commandWorktreePaths(root)
		if err != nil {
			return nil, err
		}
		item := localagent.WorktreeDiscovery{Key: paths.Key, AppRoot: paths.AppRoot, Status: "retained"}
		if _, err := paths.LoadRecord(""); err != nil {
			if os.IsNotExist(err) {
				item.Status = "absent"
			} else {
				item.Status = "incompatible-or-invalid"
			}
		}
		discovered = []localagent.WorktreeDiscovery{item}
	}
	result := make([]worktreeStatusEntry, 0, len(discovered))
	for _, item := range discovered {
		entry := worktreeStatusEntry{WorktreeDiscovery: item, Sessions: []localagent.Session{}}
		if item.Status == "retained" {
			paths, err := commandWorktreePaths(item.AppRoot)
			if err != nil {
				return nil, err
			}
			held, err := paths.ProbeLiveLock()
			if err != nil {
				entry.Status = "unavailable"
			} else if !held {
				entry.Status = "stopped"
				if _, err := os.Stat(item.AppRoot); os.IsNotExist(err) {
					entry.Status = "orphaned"
				}
			} else {
				probeCtx, cancel := context.WithTimeout(ctx, time.Second)
				client, err := commandWorktreeClient(probeCtx, item.AppRoot)
				if err == nil {
					entry.Sessions, err = client.List(probeCtx, item.AppRoot)
					client.CloseIdleConnections()
				}
				cancel()
				entry.Status = "running"
				if err != nil {
					entry.Status = "unavailable"
				} else {
					entry.Sessions = markInconsistentStatusSessions(entry.Sessions)
				}
			}
		}
		result = append(result, entry)
	}
	return result, nil
}
