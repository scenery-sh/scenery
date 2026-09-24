package main

import (
	"fmt"
	"os"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

func appProcessEnv(root string, cfg app.Config, requirements compiler.SQLRequirements, logFormat string, envName string, extra ...string) ([]string, error) {
	resolved, err := cfg.ResolveEnv(envName)
	if err != nil {
		return nil, &codedCLIError{err: err, code: 3}
	}
	baseEnv := envpolicy.Environ()
	overrides := []string{
		"SCENERY_APP_ID=" + cfg.AppID(),
		"SCENERY_APP_ROOT=" + root,
		"SCENERY_LOG_FORMAT=" + logFormat,
		"SCENERY_PARENT_MONITOR=1",
		fmt.Sprintf("SCENERY_PARENT_MONITOR_PID=%d", os.Getpid()),
	}
	overrides = append(overrides, extra...)
	overrides = append(overrides, "SCENERY_ENV="+resolved.Name, "SCENERY_RUNTIME_ENV="+resolved.Name)
	if err := validateHeadlessPostgresEnv(requirements, envWithOverrides(baseEnv, overrides...)); err != nil {
		return nil, err
	}
	storageEnv, err := headlessStorageCapabilityEnv(cfg, baseEnv)
	if err != nil {
		return nil, err
	}
	overrides = append(overrides, storageEnv...)
	return envWithOverrides(baseEnv, overrides...), nil
}

func validateHeadlessPostgresEnv(requirements compiler.SQLRequirements, baseEnv []string) error {
	_, err := resolveSQLSupply(requirements, baseEnv, false)
	return err
}

// URL parser errors can contain the original URL, including credentials.
func validateAppPostgresURL(value string) error {
	if _, err := postgresdb.ParseURL(value); err != nil {
		return &codedCLIError{code: 3, err: fmt.Errorf("%s must be a postgres:// or postgresql:// URL with a host and database name; check its syntax without printing credentials", appDatabaseURLEnv)}
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

func envValueFromList(env []string, key string) string {
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	return ""
}
