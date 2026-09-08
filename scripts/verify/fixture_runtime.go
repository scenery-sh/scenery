package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
)

// The basic fixture is copied from authored inputs only. Its generated Go and
// local state are deliberately absent until the prepared product creates them.
func copyHarnessBasicFixture(repoRoot, appRoot string) error {
	for _, name := range []string{".scenery.json", "app.scn", "go.mod", "go.sum", "service/api.go", "service/package.scn"} {
		content, err := os.ReadFile(filepath.Join(repoRoot, "testdata/apps/basic", name))
		if err != nil {
			return err
		}
		if name == "go.mod" {
			content = bytes.ReplaceAll(content, []byte("=> ../../.."), []byte("=> "+repoRoot))
		}
		path := filepath.Join(appRoot, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, content, 0o600); err != nil {
			return err
		}
	}
	return nil
}

func harnessAppEnv(home string) []string {
	return envWithOverrides(envWithoutKeys(envpolicy.Environ(), "DATABASE_URL", "SCENERY_APP_ROOT", "SCENERY_AGENT_SOCKET", "SCENERY_AGENT_ROUTER_ADDR", "SCENERY_DEV_DASHBOARD_ADDR", "SCENERY_DEV_CACHE_DIR", "GOWORK", "GOFLAGS", detachedDevChildEnv),
		"SCENERY_AGENT_HOME="+home, "SCENERY_DEV_VICTORIA=0", "SCENERY_DEV_VICTORIA_DOWNLOAD=0", "GOWORK=off")
}

func runHarnessAppCLI(ctx context.Context, repoRoot, appRoot, home string, args ...string) ([]byte, error) {
	return runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, harnessAppEnv(home), args...)
}

func runHarnessAppCLIWithEnv(ctx context.Context, repoRoot, appRoot string, env []string, args ...string) ([]byte, error) {
	cmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), append(args, "--app-root", appRoot)...)
	cmd.Dir, cmd.Env = appRoot, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		return stdout.Bytes(), productFailure(fmt.Errorf("product %v: %w: %s", args, err, tailString(stderr.String(), 8192)), stdout.Bytes())
	}
	return stdout.Bytes(), nil
}

func harnessLiveSession(ctx context.Context, home, appRoot string) (localagent.Session, error) {
	paths, err := localagent.PathsForWorktree(home, appRoot)
	if err != nil {
		return localagent.Session{}, err
	}
	client := localagent.NewClient(paths.Socket)
	defer client.CloseIdleConnections()
	health, err := client.Health(ctx)
	if err != nil {
		return localagent.Session{}, err
	}
	if err := localagent.ValidateWorktreeHealth(health, paths); err != nil {
		return localagent.Session{}, err
	}
	sessions, err := client.List(ctx, paths.AppRoot)
	if err != nil {
		return localagent.Session{}, err
	}
	if len(sessions) != 1 {
		return localagent.Session{}, fmt.Errorf("expected one owned worktree session, got %d", len(sessions))
	}
	return sessions[0], nil
}

func writeHarnessFixtureConfig(appRoot string, config any) error {
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(appRoot, ".scenery.json"), data, 0o600)
}
