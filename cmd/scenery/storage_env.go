package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storageconfig"
	"strings"
)

func storageCapabilityEnv(ctx context.Context, appRoot string, cfg appcfg.Config, session *localagent.Session, baseEnv []string, agentHome string) ([]string, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	plan, err := resolveStorageNamespacePlan(cfg, appRoot, agentHome)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	namespace, err := plan.allocate(ctx)
	if err != nil {
		return nil, err
	}
	stores := make(map[string]storageconfig.RuntimeStoreConfig, len(cfg.Storage.Stores))
	proxySocket := storageProxySocketPath(session)
	for name, store := range cfg.Storage.Stores {
		storeRuntime := storageconfig.RuntimeStoreConfig{
			Access:         strings.TrimSpace(store.Access),
			TenantScoped:   store.TenantScoped,
			MaxObjectBytes: store.MaxObjectBytes,
		}
		if proxySocket != "" {
			storeRuntime.Kind = "proxy"
			storeRuntime.ProxySocket = proxySocket
		} else {
			storeRuntime.Kind = "local"
			storeRuntime.Root = plan.Root
		}
		stores[name] = storeRuntime
	}
	runtimeCfg := storageconfig.RuntimeConfig{
		ArtifactIdentity: storageconfig.NewRuntimeIdentity(),
		Namespace:        &storageconfig.Namespace{Root: plan.Root, Binding: plan.Binding, Incarnation: namespace.Incarnation},
		Default:          strings.TrimSpace(cfg.Storage.Default),
		Stores:           stores,
	}
	if runtimeCfg.Default == "" && len(stores) == 1 {
		for name := range stores {
			runtimeCfg.Default = name
		}
	}
	data, err := json.Marshal(runtimeCfg)
	if err != nil {
		return nil, err
	}
	return []string{
		storageconfig.RuntimeConfigEnv + "=" + string(data),
	}, nil
}

func headlessStorageCapabilityEnv(cfg appcfg.Config, baseEnv []string) ([]string, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	if raw, ok := storageRuntimeConfigValue(baseEnv); ok {
		if err := validateHeadlessStorageRuntimeConfig(raw); err != nil {
			return nil, err
		}
		return nil, nil
	}
	return nil, fmt.Errorf("storage is configured, but headless runtimes require explicit %s; run `scenery up` for managed local dev storage or set %s to a production storage runtime config", storageconfig.RuntimeConfigEnv, storageconfig.RuntimeConfigEnv)
}

func storageRuntimeConfigValue(env []string) (string, bool) {
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == storageconfig.RuntimeConfigEnv && strings.TrimSpace(value) != "" {
			return value, true
		}
	}
	return "", false
}

func validateHeadlessStorageRuntimeConfig(raw string) error {
	cfg, ok, err := storageconfig.LoadRuntimeConfigValue(raw)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%s must define at least one store for headless storage runtimes", storageconfig.RuntimeConfigEnv)
	}
	if cfg.Namespace != nil && cfg.Namespace.Binding.Managed {
		return fmt.Errorf("headless storage cannot acquire managed worktree authority; use scenery up or an explicit external root")
	}
	for name, store := range cfg.Stores {
		switch strings.TrimSpace(store.Kind) {
		case "local":
			root := strings.TrimSpace(store.Root)
			if root == "" {
				return fmt.Errorf("headless storage store %q must set root when kind is \"local\"", name)
			}
			if !filepath.IsAbs(root) {
				return fmt.Errorf("headless storage store %q root %q must be an absolute path", name, root)
			}
		case "proxy":
			if cfg.Namespace == nil {
				return fmt.Errorf("headless proxy storage requires an explicit external namespace binding")
			}
			if strings.TrimSpace(store.ProxySocket) == "" {
				return fmt.Errorf("headless storage store %q must set proxy_socket when kind is \"proxy\"", name)
			}
		default:
			return fmt.Errorf("headless storage store %q kind %q is not supported; use \"local\" (with an absolute root) or \"proxy\" (with proxy_socket)", name, strings.TrimSpace(store.Kind))
		}
	}
	return nil
}
