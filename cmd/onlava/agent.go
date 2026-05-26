package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	localagent "github.com/pbrazdil/onlava/internal/agent"
	"github.com/pbrazdil/onlava/internal/app"
)

type agentOptions struct {
	SocketPath string
	RouterAddr string
	JSON       bool
}

type statusOptions struct {
	AppRoot   string
	SessionID string
	JSON      bool
}

type downOptions struct {
	AppRoot   string
	SessionID string
}

func agentCommand(args []string) error {
	opts, err := parseAgentArgs(args)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if opts.JSON {
		paths, err := localagent.DefaultPaths()
		if err != nil {
			return err
		}
		if opts.SocketPath != "" {
			paths.SocketPath = opts.SocketPath
		}
		fmt.Fprintf(os.Stdout, "{\"type\":\"agent.start\",\"socket_path\":%q,\"router_addr\":%q}\n", paths.SocketPath, firstNonEmpty(opts.RouterAddr, localagent.RouterAddrFromEnv()))
	}
	return localagent.Run(ctx, localagent.RunOptions{
		SocketPath: opts.SocketPath,
		RouterAddr: opts.RouterAddr,
		JSON:       opts.JSON,
	})
}

func parseAgentArgs(args []string) (agentOptions, error) {
	var opts agentOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--socket":
			i++
			if i >= len(args) {
				return agentOptions{}, fmt.Errorf("missing value for --socket")
			}
			opts.SocketPath = args[i]
		case "--router-listen":
			i++
			if i >= len(args) {
				return agentOptions{}, fmt.Errorf("missing value for --router-listen")
			}
			opts.RouterAddr = args[i]
		case "--json":
			opts.JSON = true
		default:
			return agentOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func statusCommand(args []string) error {
	opts, err := parseStatusArgs(args)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	appRoot := ""
	if opts.AppRoot != "" {
		appRoot, err = resolveStatusAppRoot(opts.AppRoot)
		if err != nil {
			return err
		}
	}
	sessions, err := client.List(ctx, appRoot)
	if err != nil {
		return err
	}
	if opts.SessionID != "" {
		filtered := sessions[:0]
		for _, session := range sessions {
			if session.SessionID == opts.SessionID {
				filtered = append(filtered, session)
			}
		}
		sessions = filtered
	}
	if opts.JSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(map[string]any{
			"schema_version": "onlava.agent.status.v1",
			"sessions":       sessions,
		})
	}
	for _, session := range sessions {
		fmt.Fprintf(os.Stdout, "%s\t%s\t%s\n", session.SessionID, session.Status, session.AppRoot)
	}
	return nil
}

func parseStatusArgs(args []string) (statusOptions, error) {
	opts := statusOptions{JSON: false}
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--app-root":
			i++
			if i >= len(args) {
				return statusOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			i++
			if i >= len(args) {
				return statusOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.SessionID = args[i]
		default:
			return statusOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func downCommand(args []string) error {
	opts, err := parseDownArgs(args)
	if err != nil {
		return err
	}
	client, err := localagent.DefaultClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	sessionID := strings.TrimSpace(opts.SessionID)
	if sessionID == "" {
		appRoot, err := resolveStatusAppRoot(opts.AppRoot)
		if err != nil {
			return err
		}
		sessions, err := client.List(ctx, appRoot)
		if err != nil {
			return err
		}
		if len(sessions) == 0 {
			return fmt.Errorf("no onlava agent session found for %s", appRoot)
		}
		sessionID = sessions[0].SessionID
	}
	session, err := client.Delete(ctx, sessionID, true)
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "stopped onlava session %s\n", session.SessionID)
	return nil
}

func parseDownArgs(args []string) (downOptions, error) {
	var opts downOptions
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--app-root":
			i++
			if i >= len(args) {
				return downOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--session":
			i++
			if i >= len(args) {
				return downOptions{}, fmt.Errorf("missing value for --session")
			}
			opts.SessionID = args[i]
		default:
			return downOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func resolveStatusAppRoot(value string) (string, error) {
	start := strings.TrimSpace(value)
	if start == "" {
		start = "."
	}
	root, _, err := app.DiscoverRoot(start)
	if err == nil {
		return root, nil
	}
	if value != "" {
		abs, absErr := filepath.Abs(value)
		if absErr != nil {
			return "", errors.Join(err, absErr)
		}
		return filepath.Clean(abs), nil
	}
	return "", err
}
