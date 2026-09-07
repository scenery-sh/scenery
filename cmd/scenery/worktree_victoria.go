package main

import (
	"context"
	"fmt"
	"net"
	"strconv"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/victoria"
)

func (s *devSupervisor) controlPaths() (localagent.Paths, error) {
	if s.worktreeControlPaths != nil {
		return *s.worktreeControlPaths, nil
	}
	return localagent.Paths{}, fmt.Errorf("worktree control paths are not configured")
}

func (s *devSupervisor) configureWorktreeVictoria() error {
	if s.worktreeRootPaths == nil || !victoria.Enabled() || s.victoriaProcesses.start != nil {
		return nil
	}
	// Failed private endpoint selection must never activate default global ports.
	s.victoriaProcesses.start = func(context.Context, string, victoria.Console) *victoria.Stack { return nil }
	s.victoriaProcesses.portsAvailable = func() bool { return false }
	ports := map[string]int{}
	var listeners []net.Listener
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for _, spec := range victoria.ComponentSpecs() {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			return err
		}
		listeners = append(listeners, listener)
		_, portText, err := net.SplitHostPort(listener.Addr().String())
		if err != nil {
			return err
		}
		port, err := strconv.Atoi(portText)
		if err != nil {
			return err
		}
		ports[spec.Name] = port
	}
	// Planned endpoints are not readiness claims. Runtime exporters retry while
	// optional component downloads and startup proceed asynchronously.
	for _, spec := range victoria.ComponentSpecs() {
		base := fmt.Sprintf("http://127.0.0.1:%d", ports[spec.Name])
		s.victoriaDesiredEnv = append(s.victoriaDesiredEnv,
			spec.OTELVar+"="+base+spec.EndpointPath,
			spec.SceneryURLVar+"="+base,
			spec.SceneryEndpointVar+"="+base+spec.EndpointPath,
		)
	}
	s.victoriaDesiredEnv = append(s.victoriaDesiredEnv, "SCENERY_DEV_OBSERVABILITY_BACKEND=victoria")
	s.victoriaProcesses.start = func(ctx context.Context, root string, console victoria.Console) *victoria.Stack {
		return victoria.StartOwnedAtRoot(ctx, root, console, ports)
	}
	// Verified old component PIDs, not unrelated default ports, govern recovery.
	s.victoriaProcesses.portsAvailable = func() bool { return true }
	return nil
}

func (s *devSupervisor) observabilityEnvironment() []string {
	if s.victoriaDesiredEnv != nil {
		return s.victoriaDesiredEnv
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.victoria.Env()
}
