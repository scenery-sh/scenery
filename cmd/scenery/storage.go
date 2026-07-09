package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
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
	Yes       bool
}

type storageStatusResponse struct {
	SchemaVersion string                `json:"schema_version"`
	Storage       inspectStorageRecord  `json:"storage"`
	Stores        []inspectStorageStore `json:"stores"`
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

type storageCleanupResponse struct {
	SchemaVersion string `json:"schema_version"`
	StorageCellID string `json:"storage_cell_id"`
	CellRoot      string `json:"cell_root"`
	Exists        bool   `json:"exists"`
	DryRun        bool   `json:"dry_run"`
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
		})
	case "webui":
		return writeStorageJSON(stdout, buildStorageWebUIResponse(cfg))
	case "cleanup":
		return runStorageCleanup(context.Background(), stdout, cfg, opts)
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
	opts := storageCLIOptions{}
	flags := newCLIFlagSet("storage")
	flags.BoolVar(&opts.JSON, "json", false, "")
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Prefix, "prefix", "", "")
	flags.StringVar(&opts.Cursor, "cursor", "", "")
	flags.IntVar(&opts.Limit, "limit", 0, "")
	flags.StringVar(&opts.Output, "output", "", "")
	flags.BoolVar(&opts.Recursive, "recursive", false, "")
	flags.BoolVar(&opts.Yes, "yes", false, "")
	positionals, err := parseCLIFlags(flags, args)
	if err != nil {
		return storageCLIOptions{}, err
	}
	if len(positionals) == 0 {
		return storageCLIOptions{}, fmt.Errorf("missing storage command")
	}
	opts.Command = positionals[0]
	expectedPositionals := 1
	switch opts.Command {
	case "status", "webui", "cleanup":
	case "ls":
		expectedPositionals = 2
		if len(positionals) < 2 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage ls requires <store>")
		}
		opts.Store = positionals[1]
	case "stat":
		expectedPositionals = 3
		if len(positionals) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage stat requires <store> <key>")
		}
		opts.Store, opts.Key = positionals[1], positionals[2]
	case "put":
		expectedPositionals = 4
		if len(positionals) < 4 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage put requires <store> <key> <file>")
		}
		opts.Store, opts.Key, opts.File = positionals[1], positionals[2], positionals[3]
	case "get":
		expectedPositionals = 3
		if len(positionals) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage get requires <store> <key>")
		}
		opts.Store, opts.Key = positionals[1], positionals[2]
	case "rm":
		expectedPositionals = 3
		if len(positionals) < 3 {
			return storageCLIOptions{}, fmt.Errorf("scenery storage rm requires <store> <key>")
		}
		opts.Store, opts.Key = positionals[1], positionals[2]
	default:
		return storageCLIOptions{}, fmt.Errorf("unknown storage command %q", opts.Command)
	}
	if len(positionals) > expectedPositionals {
		return storageCLIOptions{}, fmt.Errorf("unexpected argument %q", positionals[expectedPositionals])
	}
	if cliFlagSet(flags, "limit") && opts.Limit <= 0 {
		return storageCLIOptions{}, fmt.Errorf("--limit must be a positive integer")
	}
	return opts, nil
}

func runStorageCleanup(ctx context.Context, stdout io.Writer, cfg appcfg.Config, opts storageCLIOptions) error {
	plan, err := resolveStorageCellPlan(cfg, "")
	if err != nil {
		return err
	}
	if plan == nil {
		return fmt.Errorf("storage is not configured")
	}
	_, statErr := os.Stat(plan.CellRoot)
	exists := statErr == nil
	if statErr != nil && !os.IsNotExist(statErr) {
		return statErr
	}
	deleted := false
	if opts.Yes && exists {
		if err := os.RemoveAll(plan.CellRoot); err != nil {
			return err
		}
		deleted = true
		exists = false
	}
	return writeStorageJSON(stdout, storageCleanupResponse{
		SchemaVersion: "scenery.storage.cleanup.v1",
		StorageCellID: plan.StorageCellID,
		CellRoot:      plan.CellRoot,
		Exists:        exists,
		DryRun:        !opts.Yes,
		Deleted:       deleted,
	})
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
	plan, err := resolveStorageCellPlan(cfg, "")
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, fmt.Errorf("storage is not configured")
	}
	return storagebackend.NewLocalStoreWithOptions(name, plan.storageStoreObjectsDir(name), storagebackend.LocalStoreOptions{
		MaxObjectBytes: storeCfg.MaxObjectBytes,
	}), nil
}

func storageCapabilityEnv(cfg appcfg.Config, session *localagent.Session, baseEnv []string, agentHome string) ([]string, error) {
	if len(cfg.Storage.Stores) == 0 {
		return nil, nil
	}
	plan, err := resolveStorageCellPlan(cfg, agentHome)
	if err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, nil
	}
	stores := make(map[string]storageconfig.RuntimeStoreConfig, len(cfg.Storage.Stores))
	proxySocket := storageProxySocketPath(session)
	for name, store := range cfg.Storage.Stores {
		root := plan.storageStoreObjectsDir(name)
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
	return []string{
		"SCENERY_STORAGE_CELL_ID=" + plan.StorageCellID,
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
			if strings.TrimSpace(store.ProxySocket) == "" {
				return fmt.Errorf("headless storage store %q must set proxy_socket when kind is \"proxy\"", name)
			}
		default:
			return fmt.Errorf("headless storage store %q kind %q is not supported; use \"local\" (with an absolute root) or \"proxy\" (with proxy_socket)", name, strings.TrimSpace(store.Kind))
		}
	}
	return nil
}

func buildStorageWebUIResponse(cfg appcfg.Config) storageWebUIResponse {
	configured := len(cfg.Storage.Stores) > 0
	if !configured {
		return storageWebUIResponse{SchemaVersion: "scenery.storage.webui.v1", Configured: false, Ready: false, Reason: "storage is not configured"}
	}
	return storageWebUIResponse{SchemaVersion: "scenery.storage.webui.v1", Configured: true, Ready: false, Reason: "local storage has no managed Web UI; use `scenery storage ls/stat` or `scenery inspect storage`"}
}

func writeStorageJSON(w io.Writer, payload any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(payload)
}
