package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
)

func appProcessEnv(root string, cfg app.Config, logFormat string, envName string, extra ...string) ([]string, error) {
	resolved, err := cfg.ResolveEnv(envName)
	if err != nil {
		return nil, err
	}
	envLoader := appEnvWithRequiredDotEnv
	if resolved.Deployable() {
		envLoader = appEnvWithDotEnv
	}
	baseEnv, err := envLoader(envpolicy.Environ(), root, resolved.DotEnvFiles()...)
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
	overrides = append(overrides, "SCENERY_ENV="+resolved.Name, "SCENERY_RUNTIME_ENV="+resolved.Name)
	libraryNames := make([]string, 0, len(resolved.Libraries))
	for name := range resolved.Libraries {
		libraryNames = append(libraryNames, name)
	}
	sort.Strings(libraryNames)
	for _, name := range libraryNames {
		library := resolved.Libraries[name]
		prefix := libraryEnvironmentPrefix(name)
		overrides = append(overrides, prefix+"_LINKAGE="+library.Linkage)
		if library.Manifest != "" {
			overrides = append(overrides, prefix+"_MANIFEST="+filepath.Join(root, filepath.FromSlash(library.Manifest)))
		}
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

func libraryEnvironmentPrefix(name string) string {
	var value strings.Builder
	for _, r := range strings.ToUpper(name) {
		if r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' {
			value.WriteRune(r)
		} else {
			value.WriteByte('_')
		}
	}
	return "SCENERY_LIBRARY_" + value.String()
}

func validateHeadlessPostgresEnv(cfg app.Config, baseEnv []string) error {
	if len(cfg.DatabaseServices()) == 0 {
		return nil
	}
	envName := appDatabaseURLEnv
	if value, _ := lookupEnvValue(baseEnv, envName); value != "" {
		if _, err := postgresdb.ParseURL(value); err != nil {
			return fmt.Errorf("app database env %s is invalid for plan 0097: %w", envName, err)
		}
		return nil
	}
	return fmt.Errorf("app database requires %s for `scenery worker`; the managed shared Postgres server is a `scenery up` dev substrate only", envName)
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
