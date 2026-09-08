package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/toolchain"
)

func harnessAppEnvWithVictoria(ctx context.Context, repoRoot, home string) ([]string, error) {
	env := envWithOverrides(harnessAppEnv(home), "SCENERY_TOOLCHAIN_DIR="+filepath.Join(repoRoot, ".scenery/harness/worktree-runtime/victoria-toolchain"))
	for name, envName := range map[string]string{"metrics": "SCENERY_VICTORIA_METRICS_BIN", "logs": "SCENERY_VICTORIA_LOGS_BIN", "traces": "SCENERY_VICTORIA_TRACES_BIN"} {
		cmd := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "system", "toolchain", "sync", "--tool", "victoria-"+name, "-o", "json")
		cmd.Dir, cmd.Env = repoRoot, env
		var output, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &output, &stderr
		if err := cmd.Run(); err != nil {
			return nil, fmt.Errorf("prepare managed Victoria %s: %w: %s", name, err, tailString(stderr.String(), 8192))
		}
		var status struct {
			Artifacts []struct {
				ManagedPath string `json:"managed_path"`
			} `json:"artifacts"`
		}
		if err := decodeCLIJSON(output.Bytes(), &status); err != nil {
			return nil, err
		}
		if len(status.Artifacts) != 1 || status.Artifacts[0].ManagedPath == "" {
			return nil, fmt.Errorf("managed Victoria %s did not identify one binary", name)
		}
		env = envWithOverrides(env, envName+"="+status.Artifacts[0].ManagedPath)
	}
	return envWithOverrides(env, "SCENERY_DEV_VICTORIA=1"), nil
}

// Release fixtures consume the managed artifact store directly. They never
// select an unrelated Caddy from PATH or start the machine edge service.
func harnessCaddyBinary(ctx context.Context, download bool) (string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(paths.Home, "toolchain")
	if strings.TrimSpace(envpolicy.Get("SCENERY_TOOLCHAIN_DIR")) != "" {
		dir = toolchain.DefaultStoreDir("")
	}
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", err
	}
	store, err := toolchain.NewStore(dir, manifest)
	if err != nil {
		return "", err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	if download {
		if _, err := store.Sync(ctx, toolchain.Options{Tool: "caddy"}); err != nil {
			return "", err
		}
	}
	status, err := store.Path(ctx, "caddy", toolchain.CurrentPlatform())
	if err != nil {
		return "", err
	}
	info, err := os.Stat(status.ManagedPath)
	if err != nil || info.IsDir() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("managed Caddy is not executable in %s", dir)
	}
	return status.ManagedPath, nil
}
