package main

import (
	"fmt"
	"os"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

func appProcessEnv(root string, cfg app.Config, logFormat string, envName string, extra ...string) ([]string, error) {
	envLoader := appEnvWithRequiredDotEnv
	if strings.EqualFold(strings.TrimSpace(envName), "production") {
		envLoader = appEnvWithDotEnv
	}
	baseEnv, err := envLoader(envpolicy.Environ(), root)
	if err != nil {
		return nil, err
	}
	overrides := []string{
		"SCENERY_APP_ID=" + cfg.AppID(),
		"SCENERY_APP_ROOT=" + root,
		"SCENERY_LOG_FORMAT=" + logFormat,
		"SCENERY_PARENT_MONITOR=1",
		fmt.Sprintf("SCENERY_PARENT_MONITOR_PID=%d", os.Getpid()),
	}
	overrides = append(overrides, extra...)
	if envName != "" {
		overrides = append(overrides, "SCENERY_ENV="+envName, "SCENERY_RUNTIME_ENV="+envName)
	}
	storageEnv, err := runtimeStorageCapabilityEnv(cfg, baseEnv)
	if err != nil {
		return nil, err
	}
	overrides = append(overrides, storageEnv...)
	return envWithOverrides(baseEnv, overrides...), nil
}

func envWithOverrides(base []string, overrides ...string) []string {
	keys := make(map[string]struct{}, len(overrides))
	for _, item := range overrides {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			keys[key] = struct{}{}
		}
	}
	env := make([]string, 0, len(base)+len(overrides))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if ok {
			if _, replace := keys[key]; replace {
				continue
			}
		}
		env = append(env, item)
	}
	return append(env, overrides...)
}
