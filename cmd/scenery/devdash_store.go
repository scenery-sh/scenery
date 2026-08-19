package main

import (
	"context"
	"path/filepath"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/devdash"
)

func openDevdashStore() (*devdash.Store, error) {
	return devdash.OpenStore(devdashCacheRoot())
}

func devdashCacheRoot() string {
	if root := devcache.EnvOrOverride(); root != "" {
		return root
	}
	if localagent.DisabledByEnv() {
		return ""
	}
	client, err := commandAgentClient()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := client.Ping(ctx); err != nil {
		return ""
	}
	paths, err := commandAgentPaths()
	if err != nil {
		return ""
	}
	return filepath.Join(paths.AgentDir, "dashboard")
}
