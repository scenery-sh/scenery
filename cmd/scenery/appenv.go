package main

import (
	"fmt"
	"os"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
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
	if err := validateHeadlessPostgresEnv(cfg, baseEnv); err != nil {
		return nil, err
	}
	storageEnv, err := headlessStorageCapabilityEnv(cfg, baseEnv)
	if err != nil {
		return nil, err
	}
	overrides = append(overrides, storageEnv...)
	return envWithOverrides(baseEnv, overrides...), nil
}

func validateHeadlessPostgresEnv(cfg app.Config, baseEnv []string) error {
	for _, svc := range cfg.PostgresServices() {
		if value, _ := lookupEnvValue(baseEnv, svc.DatabaseURLEnv); value != "" {
			if _, err := postgresdb.ParseURL(value); err != nil {
				return fmt.Errorf("postgres service %q env %s is invalid: %w", svc.Name, svc.DatabaseURLEnv, err)
			}
			continue
		}
		return fmt.Errorf("postgres service %q requires %s for `scenery worker`; the managed shared Postgres server is a `scenery up` dev substrate only", svc.Name, svc.DatabaseURLEnv)
	}
	return nil
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
