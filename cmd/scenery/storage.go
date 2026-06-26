package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	storagebackend "scenery.sh/internal/storage"
	"scenery.sh/internal/storageconfig"
	publicstorage "scenery.sh/storage"
)

type storageCLIOptions struct {
	Command   string
	AppRoot   string
	JSON      bool
	Store     string
	Key       string
	File      string
	Output    string
	Prefix    string
	Cursor    string
	Limit     int
	Recursive bool
}

type storageStatusResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Storage       inspectStorageRecord   `json:"storage"`
	Stores        []inspectStorageStore  `json:"stores"`
	DevService    *inspectStorageService `json:"dev_service,omitempty"`
}

type storageWebUIResponse struct {
	SchemaVersion string `json:"schema_version"`
	Configured    bool   `json:"configured"`
	Ready         bool   `json:"ready"`
	URL           string `json:"url,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type storageObjectResponse struct {
	SchemaVersion string               `json:"schema_version"`
	Object        publicstorage.Object `json:"object"`
}

type storageListResponse struct {
	SchemaVersion string                 `json:"schema_version"`
	Store         string                 `json:"store"`
	Page          publicstorage.ListPage `json:"page"`
}

type storageDeleteResponse struct {
	SchemaVersion string `json:"schema_version"`
	Store         string `json:"store"`
	Key           string `json:"key,omitempty"`
	Prefix        string `json:"prefix,omitempty"`
	Deleted       bool   `json:"deleted"`
}

func storageCommand(args []string) error {
	return runStorageCommand(args, os.Stdout)
}

func runStorageCommand(args []string, stdout io.Writer) error {
	opts, err := parseStorageArgs(args)
	if err != nil {
		return err
	}
	if !opts.JSON {
		return fmt.Errorf("scenery storage %s currently requires --json", opts.Command)
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	appRoot, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		return err
	}
	switch opts.Command {
	case "status":
		inspect := buildInspectStorageResponse(context.Background(), appRoot, cfg)
		return writeStorageJSON(stdout, storageStatusResponse{
			SchemaVersion: "scenery.storage.status.v1",
			Storage:       inspect.Storage,
			Stores:        inspect.Stores,
			DevService:    inspect.DevService,
		})
	case "webui":
		return writeStorageJSON(stdout, buildStorageWebUIResponse(cfg))
	case "ls":
		store, err := storageStoreForCLI(cfg, opts.Store)
		if err != nil {
			return err
		}
		page, err := store.List(context.Background(), publicstorage.ListOptions{Prefix: opts.Prefix, Cursor: opts.Cursor, Limit: opts.Limit})
		if err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageListResponse{SchemaVersion: "scenery.storage.list.v1", Store: opts.Store, Page: *page})
	case "stat":
		store, err := storageStoreForCLI(cfg, opts.Store)
		if err != nil {
			return err
		}
		obj, err := store.Head(context.Background(), opts.Key)
		if err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageObjectResponse{SchemaVersion: "scenery.storage.object.v1", Object: *obj})
	case "put":
		store, err := storageStoreForCLI(cfg, opts.Store)
		if err != nil {
			return err
		}
		obj, err := store.PutFile(context.Background(), opts.Key, opts.File, publicstorage.PutOptions{})
		if err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageObjectResponse{SchemaVersion: "scenery.storage.object.v1", Object: *obj})
	case "get":
		if opts.Output == "" {
			return fmt.Errorf("scenery storage get requires --output when --json is used")
		}
		store, err := storageStoreForCLI(cfg, opts.Store)
		if err != nil {
			return err
		}
		body, obj, err := store.Get(context.Background(), opts.Key, publicstorage.GetOptions{})
		if err != nil {
			return err
		}
		defer body.Close()
		if err := os.MkdirAll(filepath.Dir(opts.Output), 0o755); err != nil {
			return err
		}
		out, err := os.Create(opts.Output)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, body); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageObjectResponse{SchemaVersion: "scenery.storage.object.v1", Object: *obj})
	case "rm":
		store, err := storageStoreForCLI(cfg, opts.Store)
		if err != nil {
			return err
		}
		if opts.Recursive {
			if err := store.DeletePrefix(context.Background(), opts.Key); err != nil {
				return err
			}
			return writeStorageJSON(stdout, storageDeleteResponse{SchemaVersion: "scenery.storage.delete.v1", Store: opts.Store, Prefix: opts.Key, Deleted: true})
		}
		if err := store.Delete(context.Background(), opts.Key); err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageDeleteResponse{SchemaVersion: "scenery.storage.delete.v1", Store: opts.Store, Key: opts.Key, Deleted: true})
	default:
		return fmt.Errorf("unknown storage command %q", opts.Command)
	}
}

func parseStorageArgs(args []string) (storageCLIOptions, error) {
	if len(args) == 0 {
		return storageCLIOptions{}, fmt.Errorf("missing storage command")
	}
	opts := storageCLIOptions{Command: args[0]}
	switch opts.Command {
	case "status", "webui":
	case "ls":
		if len(args) < 2 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage ls requires <store>")
		}
		opts.Store = args[1]
		args = append(args[:1], args[2:]...)
	case "stat":
		if len(args) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage stat requires <store> <key>")
		}
		opts.Store, opts.Key = args[1], args[2]
		args = append(args[:1], args[3:]...)
	case "put":
		if len(args) < 4 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage put requires <store> <key> <file>")
		}
		opts.Store, opts.Key, opts.File = args[1], args[2], args[3]
		args = append(args[:1], args[4:]...)
	case "get":
		if len(args) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage get requires <store> <key>")
		}
		opts.Store, opts.Key = args[1], args[2]
		args = append(args[:1], args[3:]...)
	case "rm":
		if len(args) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage rm requires <store> <key>")
		}
		opts.Store, opts.Key = args[1], args[2]
		args = append(args[:1], args[3:]...)
	default:
		return storageCLIOptions{}, fmt.Errorf("unknown storage command %q", opts.Command)
	}
	for i := 1; i < len(args); i++ {
		switch args[i] {
		case "--json":
			opts.JSON = true
		case "--app-root":
			i++
			if i >= len(args) {
				return storageCLIOptions{}, fmt.Errorf("missing value for --app-root")
			}
			opts.AppRoot = args[i]
		case "--prefix":
			i++
			if i >= len(args) {
				return storageCLIOptions{}, fmt.Errorf("missing value for --prefix")
			}
			opts.Prefix = args[i]
		case "--cursor":
			i++
			if i >= len(args) {
				return storageCLIOptions{}, fmt.Errorf("missing value for --cursor")
			}
			opts.Cursor = args[i]
		case "--limit":
			i++
			if i >= len(args) {
				return storageCLIOptions{}, fmt.Errorf("missing value for --limit")
			}
			limit, err := strconv.Atoi(args[i])
			if err != nil || limit <= 0 {
				return storageCLIOptions{}, fmt.Errorf("--limit must be a positive integer")
			}
			opts.Limit = limit
		case "--output":
			i++
			if i >= len(args) {
				return storageCLIOptions{}, fmt.Errorf("missing value for --output")
			}
			opts.Output = args[i]
		case "--recursive":
			opts.Recursive = true
		default:
			return storageCLIOptions{}, fmt.Errorf("unknown flag %q", args[i])
		}
	}
	return opts, nil
}

func storageStoreForCLI(cfg appcfg.Config, name string) (publicstorage.Store, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = strings.TrimSpace(cfg.Storage.Default)
	}
	if name == "" {
		return nil, fmt.Errorf("storage store name is required")
	}
	storeCfg, ok := cfg.Storage.Stores[name]
	if !ok {
		return nil, fmt.Errorf("storage store %q is not configured", name)
	}
	if strings.TrimSpace(storeCfg.Kind) != "zerofs" {
		return nil, fmt.Errorf("storage store %q kind %q is not supported", name, storeCfg.Kind)
	}
	plan, err := resolveManagedZeroFSPlan(cfg, &localagent.Session{SessionID: "cli", BaseAppID: cfg.AppID()}, nil, "")
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("storage is not configured")
	}
	return storagebackend.NewLocalStoreWithOptions(name, filepath.Join(plan.ObjectsDir, name), storagebackend.LocalStoreOptions{
		MaxObjectBytes: storeCfg.MaxObjectBytes,
	}), nil
}

func storageCapabilityEnv(cfg appcfg.Config, session *localagent.Session, baseEnv []string, agentHome string) ([]string, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	if session == nil {
		session = &localagent.Session{SessionID: "process", BaseAppID: cfg.AppID()}
	}
	plan, err := resolveManagedZeroFSPlan(cfg, session, baseEnv, agentHome)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	stores := make(map[string]storageconfig.RuntimeStoreConfig, len(cfg.Storage.Stores))
	proxySocket := storageProxySocketPath(session)
	for name, store := range cfg.Storage.Stores {
		root := filepath.Join(plan.ObjectsDir, name)
		storeRuntime := storageconfig.RuntimeStoreConfig{
			Access:         strings.TrimSpace(store.Access),
			TenantScoped:   store.TenantScoped,
			MaxObjectBytes: store.MaxObjectBytes,
		}
		if proxySocket != "" {
			storeRuntime.Kind = "proxy"
			storeRuntime.ProxySocket = proxySocket
		} else {
			if err := os.MkdirAll(root, 0o755); err != nil {
				return nil, err
			}
			storeRuntime.Kind = "local"
			storeRuntime.Root = root
		}
		stores[name] = storeRuntime
	}
	runtimeCfg := storageconfig.RuntimeConfig{
		SchemaVersion: storageconfig.RuntimeSchemaVersion,
		CellID:        plan.StorageCellID,
		Default:       strings.TrimSpace(cfg.Storage.Default),
		Stores:        stores,
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
	result := []string{
		"SCENERY_STORAGE_CELL_ID=" + plan.StorageCellID,
		storageconfig.RuntimeConfigEnv + "=" + string(data),
	}
	if session != nil {
		if webUIURL := strings.TrimSpace(session.Routes[plan.Route]); webUIURL != "" {
			result = append(result, "SCENERY_ZEROFS_WEBUI_URL="+webUIURL)
		}
	}
	return result, nil
}

func headlessStorageCapabilityEnv(cfg appcfg.Config, baseEnv []string) ([]string, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	if storageRuntimeConfigPresent(baseEnv) {
		return nil, nil
	}
	return nil, fmt.Errorf("storage is configured, but headless runtimes require explicit %s; run `scenery up` for managed dev ZeroFS or set %s to an operator-provided storage runtime config", storageconfig.RuntimeConfigEnv, storageconfig.RuntimeConfigEnv)
}

func storageRuntimeConfigPresent(env []string) bool {
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if ok && key == storageconfig.RuntimeConfigEnv && strings.TrimSpace(value) != "" {
			return true
		}
	}
	return false
}

func buildStorageWebUIResponse(cfg appcfg.Config) storageWebUIResponse {
	configured := len(cfg.Storage.Stores) > 0
	if !configured {
		return storageWebUIResponse{SchemaVersion: "scenery.storage.webui.v1", Configured: false, Ready: false, Reason: "storage is not configured"}
	}
	if _, _, ok := managedZeroFSDeclared(cfg); !ok {
		return storageWebUIResponse{SchemaVersion: "scenery.storage.webui.v1", Configured: true, Ready: false, Reason: "managed ZeroFS dev service is not configured"}
	}
	return storageWebUIResponse{SchemaVersion: "scenery.storage.webui.v1", Configured: true, Ready: false, Reason: "storage runtime startup has not attached a protected Web UI route yet"}
}

func writeStorageJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
