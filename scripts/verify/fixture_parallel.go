package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/app"
)

func prepareHarnessParallelSession(ctx context.Context, repoRoot, home, root string, cfg app.Config) (*localagent.Session, error) {
	if err := copyHarnessBasicFixture(repoRoot, root); err != nil {
		return nil, err
	}
	if err := writeHarnessFixtureConfig(root, cfg); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(root, "apps", "web"), 0o755); err != nil {
		return nil, err
	}
	if _, err := runHarnessAppCLI(ctx, repoRoot, root, home, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
		return nil, err
	}
	session, err := harnessLiveSession(ctx, home, root)
	if err != nil {
		return nil, err
	}
	if backend := session.Backends[localagent.RouteAPI]; backend.Network == "" || backend.Addr == "" {
		return nil, fmt.Errorf("agent session API backend = %+v", backend)
	}
	if backend := session.Backends["web"]; backend.Network != "tcp" || backend.Addr == "" {
		return nil, fmt.Errorf("agent session frontend backend = %+v", backend)
	}
	return &session, nil
}
