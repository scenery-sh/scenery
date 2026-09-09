package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/storagefs"
	publicstorage "scenery.sh/storage"
	"strings"
	"syscall"
)

type storageCLIOptions struct {
	Command, AppRoot, Store, Tenant, Key, File, Output, Prefix, Delimiter, Cursor string
	ContentType, Metadata, IfMatch, ExpectRevision                                string
	Limit                                                                         int
	JSON, Recursive, Yes, DryRun, Purge, IfAbsent                                 bool
}
type storageResponseScope struct {
	AppID       string  `json:"app_id"`
	AppRoot     string  `json:"app_root"`
	WorktreeKey string  `json:"worktree_key"`
	Incarnation *string `json:"incarnation"`
	Generation  *string `json:"generation"`
	Store       string  `json:"store,omitempty"`
	Tenant      string  `json:"tenant,omitempty"`
}
type storageObjectResponse struct {
	cliPayloadIdentity
	Scope  storageResponseScope `json:"scope"`
	Object publicstorage.Object `json:"object"`
}
type storageListResponse struct {
	cliPayloadIdentity
	Scope storageResponseScope   `json:"scope"`
	Page  publicstorage.ListPage `json:"page"`
}
type storageDeleteResponse struct {
	cliPayloadIdentity
	Scope   storageResponseScope     `json:"scope"`
	Key     string                   `json:"key,omitempty"`
	Prefix  string                   `json:"prefix,omitempty"`
	DryRun  bool                     `json:"dry_run"`
	Deleted bool                     `json:"deleted"`
	Preview *storagefs.DeletePreview `json:"preview,omitempty"`
	Result  *storagefs.DeleteResult  `json:"result,omitempty"`
}
type storageCleanupResponse struct {
	cliPayloadIdentity
	Scope        storageResponseScope      `json:"scope"`
	Purge        bool                      `json:"purge"`
	DryRun       bool                      `json:"dry_run"`
	Preview      *storagefs.ReclaimPreview `json:"preview,omitempty"`
	Result       *storagefs.ReclaimResult  `json:"result,omitempty"`
	PurgePreview *storagefs.PurgePreview   `json:"purge_preview,omitempty"`
	PurgeResult  *storagefs.PurgeResult    `json:"purge_result,omitempty"`
}

func storageCommand(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runStorageCommandContext(ctx, args, os.Stdin, os.Stdout)
}
func runStorageCommand(args []string, stdout io.Writer) error {
	return runStorageCommandContext(context.Background(), args, os.Stdin, stdout)
}
func runStorageCommandContext(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	opts, err := parseStorageArgs(args)
	if err != nil {
		return fmt.Errorf("%w: %v", storagefs.ErrInvalid, err)
	}
	if !opts.JSON {
		return fmt.Errorf("%w: scenery storage %s requires -o json", storagefs.ErrInvalid, opts.Command)
	}
	start, err := resolveAppRoot(opts.AppRoot)
	if err != nil {
		return err
	}
	root, cfg, err := appcfg.DiscoverRoot(start)
	if err != nil {
		if opts.Command == "cleanup" && filepath.IsAbs(opts.AppRoot) {
			plan, retainedErr := retainedStorageNamespacePlan(opts.AppRoot)
			if retainedErr != nil {
				return retainedErr
			}
			return runStorageCleanup(ctx, stdout, plan, opts)
		}
		return err
	}
	plan, err := resolveStorageNamespacePlan(cfg, root, "")
	if err != nil {
		return err
	}
	if len(cfg.Storage.Stores) == 0 && opts.Command != "cleanup" {
		return &publicstorage.NotConfiguredError{}
	}
	if opts.Command == "cleanup" {
		return runStorageCleanup(ctx, stdout, plan, opts)
	}
	if opts.Command == "put" {
		return runStoragePut(ctx, stdin, stdout, cfg, plan, opts)
	}
	store, owner, err := storageStoreForCLI(ctx, cfg, plan, opts, false)
	scope := storageScope(plan, owner, opts)
	if errors.Is(err, storagefs.ErrUninitialized) {
		if opts.Cursor != "" {
			return fmt.Errorf("%w: cursor is not valid for an uninitialized namespace", storagefs.ErrInvalid)
		}
		if opts.Command == "rm" && opts.IfMatch != "" {
			return storagefs.ErrPrecondition
		}
		if opts.Command == "ls" && opts.Cursor == "" {
			return writeStorageJSON(stdout, storageListResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.list"), Scope: scope, Page: publicstorage.ListPage{Objects: []publicstorage.Object{}}})
		}
		if opts.Command == "rm" && !opts.Recursive && opts.IfMatch == "" {
			return writeStorageJSON(stdout, storageDeleteResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.delete"), Scope: scope, Key: opts.Key, Deleted: true})
		}
		return &publicstorage.NotFoundError{Store: opts.Store, Key: opts.Key}
	}
	if err != nil {
		return err
	}
	switch opts.Command {
	case "ls":
		page, err := store.List(ctx, publicstorage.ListOptions{Prefix: opts.Prefix, Delimiter: opts.Delimiter, Cursor: opts.Cursor, Limit: opts.Limit})
		if err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageListResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.list"), Scope: scope, Page: *page})
	case "stat":
		obj, err := store.Head(ctx, opts.Key)
		if err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageObjectResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.object"), Scope: scope, Object: *obj})
	case "get":
		body, obj, err := store.Get(ctx, opts.Key, publicstorage.GetOptions{})
		if err != nil {
			return err
		}
		defer func() { _ = body.Close() }()
		if err := writeStorageDownload(ctx, plan, opts.Output, body, obj.SizeBytes); err != nil {
			return err
		}
		return writeStorageJSON(stdout, storageObjectResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.object"), Scope: scope, Object: *obj})
	case "rm":
		response := storageDeleteResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.delete"), Scope: scope}
		if opts.Recursive {
			response.Prefix = opts.Key
			response.DryRun = !opts.Yes
			if !opts.Yes {
				preview, err := store.PreviewDelete(ctx, opts.Key)
				if err != nil {
					return err
				}
				response.Preview = &preview
			} else {
				result, err := store.ApplyDelete(ctx, opts.Key, opts.ExpectRevision)
				if err != nil {
					return err
				}
				response.Result = &result
				response.Deleted = result.Completion == "complete"
			}
		} else {
			if err := store.Delete(ctx, opts.Key, publicstorage.DeleteOptions{IfMatch: opts.IfMatch}); err != nil {
				return err
			}
			response.Key = opts.Key
			response.Deleted = true
		}
		return writeStorageJSON(stdout, response)
	}
	return fmt.Errorf("unknown storage command %q", opts.Command)
}

func storageScope(plan *storageNamespacePlan, owner storagefs.Owner, opts storageCLIOptions) storageResponseScope {
	result := storageResponseScope{AppID: plan.Binding.AppID, AppRoot: plan.Binding.AppRoot, WorktreeKey: plan.Binding.WorktreeKey, Store: opts.Store, Tenant: opts.Tenant}
	if owner.Incarnation != "" {
		result.Incarnation = &owner.Incarnation
		result.Generation = &owner.Generation
	}
	return result
}

func storageStoreForCLI(ctx context.Context, cfg appcfg.Config, plan *storageNamespacePlan, opts storageCLIOptions, allocate bool) (*storagefs.Store, storagefs.Owner, error) {
	policy, ok := cfg.Storage.Stores[opts.Store]
	if !ok {
		return nil, storagefs.Owner{}, &publicstorage.NotConfiguredError{Store: opts.Store}
	}
	if policy.TenantScoped && opts.Tenant == "" {
		return nil, storagefs.Owner{}, &publicstorage.TenantRequiredError{Store: opts.Store}
	}
	if !policy.TenantScoped && opts.Tenant != "" {
		return nil, storagefs.Owner{}, fmt.Errorf("%w: store %q is not tenant-scoped", storagefs.ErrInvalid, opts.Store)
	}
	var namespace *storagefs.Namespace
	var err error
	if allocate {
		namespace, err = plan.allocate(ctx)
		if err != nil {
			return nil, storagefs.Owner{}, err
		}
	}
	owner, err := plan.discover(ctx)
	if err != nil {
		return nil, storagefs.Owner{}, err
	}
	if namespace == nil {
		namespace, err = storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
		if err != nil {
			return nil, owner, err
		}
	}
	store, err := namespace.Store(storagefs.Scope{Store: opts.Store, Tenant: opts.Tenant}, policy.MaxObjectBytes)
	return store, owner, err
}

func runStoragePut(ctx context.Context, stdin io.Reader, stdout io.Writer, cfg appcfg.Config, plan *storageNamespacePlan, opts storageCLIOptions) error {
	var metadata map[string]string
	if opts.Metadata != "" {
		file, err := os.Open(opts.Metadata)
		if err != nil {
			return err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (16<<10)+1))
		closeErr := file.Close()
		if readErr != nil {
			return readErr
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > 16<<10 {
			return fmt.Errorf("%w: metadata exceeds 16 KiB", storagefs.ErrInvalid)
		}
		if _, err := contract.DecodeJSONObject(data); err != nil {
			return fmt.Errorf("%w: metadata must be one exact JSON string map: %v", storagefs.ErrInvalid, err)
		}
		if err := json.Unmarshal(data, &metadata); err != nil {
			return fmt.Errorf("%w: metadata must be one JSON string map: %v", storagefs.ErrInvalid, err)
		}
	}
	putOptions := publicstorage.PutOptions{ContentType: opts.ContentType, Metadata: metadata, IfNoneMatch: opts.IfAbsent, IfMatch: opts.IfMatch}
	if err := storagefs.ValidatePutOptions(putOptions); err != nil {
		return err
	}
	body := stdin
	if opts.File != "-" {
		file, err := os.Open(opts.File)
		if err != nil {
			return err
		}
		defer func() { _ = file.Close() }()
		body = file
	}
	store, owner, err := storageStoreForCLI(ctx, cfg, plan, opts, true)
	if err != nil {
		return err
	}
	obj, err := store.Put(ctx, opts.Key, body, putOptions)
	if err != nil {
		return err
	}
	return writeStorageJSON(stdout, storageObjectResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.object"), Scope: storageScope(plan, owner, opts), Object: *obj})
}

func parseStorageArgs(args []string) (storageCLIOptions, error) {
	opts := storageCLIOptions{}
	flags := newCLIFlagSet("storage")
	registerJSONOutput(flags, &opts.JSON)
	flags.StringVar(&opts.AppRoot, "app-root", "", "")
	flags.StringVar(&opts.Tenant, "tenant", "", "")
	flags.StringVar(&opts.Prefix, "prefix", "", "")
	flags.StringVar(&opts.Delimiter, "delimiter", "", "")
	flags.StringVar(&opts.Cursor, "cursor", "", "")
	flags.IntVar(&opts.Limit, "limit", 0, "")
	flags.StringVar(&opts.Output, "output", "", "")
	flags.StringVar(&opts.ContentType, "content-type", "", "")
	flags.StringVar(&opts.Metadata, "metadata", "", "")
	flags.StringVar(&opts.IfMatch, "if-match", "", "")
	flags.StringVar(&opts.ExpectRevision, "expect-revision", "", "")
	flags.BoolVar(&opts.IfAbsent, "if-absent", false, "")
	flags.BoolVar(&opts.Recursive, "recursive", false, "")
	flags.BoolVar(&opts.Yes, "yes", false, "")
	flags.BoolVar(&opts.DryRun, "dry-run", false, "")
	flags.BoolVar(&opts.Purge, "purge", false, "")
	positional, err := parseCLIFlags(flags, args)
	if err != nil {
		return opts, err
	}
	if len(positional) == 0 {
		return opts, fmt.Errorf("missing storage command")
	}
	opts.Command = positional[0]
	count := map[string]int{"cleanup": 1, "ls": 2, "stat": 3, "get": 3, "rm": 3, "put": 4}[opts.Command]
	if count == 0 {
		return opts, fmt.Errorf("unknown storage command %q; use inspect storage for discovery", opts.Command)
	}
	if len(positional) != count {
		return opts, fmt.Errorf("storage %s expects %d positional arguments after the command", opts.Command, count-1)
	}
	if count >= 2 {
		opts.Store = strings.TrimSpace(positional[1])
	}
	if count >= 3 {
		opts.Key = positional[2]
	}
	if count == 4 {
		opts.File = positional[3]
	}
	opts.Tenant = strings.TrimSpace(opts.Tenant)
	if opts.Store == "" && count >= 2 {
		return opts, fmt.Errorf("storage store is required")
	}
	if opts.Command == "get" && opts.Output == "" {
		return opts, fmt.Errorf("storage get requires --output")
	}
	if opts.IfAbsent && opts.IfMatch != "" {
		return opts, fmt.Errorf("--if-absent and --if-match are mutually exclusive")
	}
	if opts.Yes && opts.DryRun {
		return opts, fmt.Errorf("--yes and --dry-run are mutually exclusive")
	}
	if opts.Recursive && opts.Command != "rm" {
		return opts, fmt.Errorf("--recursive requires storage rm")
	}
	if opts.Purge && opts.Command != "cleanup" {
		return opts, fmt.Errorf("--purge requires storage cleanup")
	}
	destructive := opts.Command == "cleanup" || (opts.Command == "rm" && opts.Recursive)
	if (opts.Yes || opts.DryRun || opts.ExpectRevision != "") && !destructive {
		return opts, fmt.Errorf("preview/apply flags require cleanup or recursive rm")
	}
	if destructive && opts.Yes && opts.ExpectRevision == "" {
		return opts, fmt.Errorf("--yes requires --expect-revision from a fresh preview")
	}
	if opts.ExpectRevision != "" && !opts.Yes {
		return opts, fmt.Errorf("--expect-revision requires --yes")
	}
	if opts.Command != "put" && cliFlagSet(flags, "if-absent", "content-type", "metadata") {
		return opts, fmt.Errorf("upload flags require storage put")
	}
	if opts.IfMatch != "" && opts.Command != "put" && opts.Command != "rm" {
		return opts, fmt.Errorf("--if-match requires put or non-recursive rm")
	}
	if opts.IfMatch != "" && opts.Recursive {
		return opts, fmt.Errorf("recursive rm uses --expect-revision, not --if-match")
	}
	if opts.Command != "ls" && cliFlagSet(flags, "prefix", "delimiter", "cursor", "limit") {
		return opts, fmt.Errorf("list flags require storage ls")
	}
	if opts.Command != "get" && cliFlagSet(flags, "output") {
		return opts, fmt.Errorf("--output requires storage get")
	}
	if opts.Command == "cleanup" && cliFlagSet(flags, "tenant") {
		return opts, fmt.Errorf("cleanup selects a complete namespace, not a tenant")
	}
	if cliFlagSet(flags, "limit") && opts.Limit <= 0 {
		return opts, fmt.Errorf("--limit must be positive")
	}
	if opts.Recursive {
		if opts.Key == "" {
			return opts, fmt.Errorf("recursive prefix must be nonempty; namespace destruction uses cleanup --purge")
		}
		if err := publicstorage.ValidatePrefix(opts.Key); err != nil {
			return opts, err
		}
	} else if count >= 3 {
		if err := publicstorage.ValidateKey(opts.Key); err != nil {
			return opts, err
		}
	}
	if opts.Command == "ls" {
		if _, err := publicstorage.NormalizeListOptions(publicstorage.ListOptions{Prefix: opts.Prefix, Delimiter: opts.Delimiter, Limit: opts.Limit}); err != nil {
			return opts, err
		}
	}
	return opts, nil
}

func writeStorageJSON(w io.Writer, payload any) error { return writeCLIJSON(w, payload) }
