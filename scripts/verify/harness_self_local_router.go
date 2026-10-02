package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Preserve the native router/agent restart journey outside ordinary test roots.
func runHarnessLocalRouterRestartProof(parent context.Context, repoRoot string) (summary map[string]any, resultErr error) {
	ctx, cancel := context.WithTimeout(parent, time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-local-router-proof-")
	if err != nil {
		return nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, os.RemoveAll(root)) }()
	binary := filepath.Join(root, "router.test")
	build := commandTreeContext(ctx, "go", "test", "-c", "-tags=scenery_local_router_integration", "-o", binary, "./cmd/scenery")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build native local router proof: %w: %s", err, tailString(string(output), 4096))
	}
	const name = "TestNativeLocalPathRouterDashboardFollowsAgentRestart"
	run := commandTreeContext(ctx, binary, "-test.run=^"+name+"$", "-test.v", "-test.count=1")
	run.Dir = filepath.Join(repoRoot, "cmd/scenery")
	output, err := run.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("native local router proof: %w: %s", err, tailString(string(output), 4096))
	}
	if !strings.Contains(string(output), "--- PASS: "+name+" ") {
		return nil, fmt.Errorf("native local router journey missing: %s", tailString(string(output), 4096))
	}
	return map[string]any{"agent_health_backend_replacement": true, "dead_backend_bounded_retry_and_terse_502": true, "servers_and_router_closed": true, "journey": name}, nil
}
