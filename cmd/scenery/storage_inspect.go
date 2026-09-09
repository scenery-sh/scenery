package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/storagefs"
)

func runInspectStorage(ctx context.Context, stdout io.Writer, plan *storageNamespacePlan, cfg appcfg.Config, opts inspectOptions) error {
	response, err := buildInspectStorageResponse(ctx, plan, cfg, opts.StorageStats)
	if err != nil {
		return err
	}
	if opts.JSON {
		return writeInspectJSON(stdout, response)
	}
	_, err = fmt.Fprintf(stdout, "%s\t%s\t%s\n", response.Storage.Scope.AppRoot, response.Storage.Readiness, response.Storage.Scope.WorktreeKey)
	if err != nil {
		return err
	}
	for _, store := range response.Stores {
		totals := "totals unknown (use --stats)"
		if store.ObjectCount != nil {
			totals = fmt.Sprintf("%d objects, %d bytes", *store.ObjectCount, *store.TotalBytes)
		}
		if _, err := fmt.Fprintf(stdout, "%s\t%s\ttenant_scoped=%t\t%s\n", store.Name, store.Access, store.TenantScoped, totals); err != nil {
			return err
		}
	}
	if response.Storage.BrowserURL != "" {
		_, err = fmt.Fprintln(stdout, response.Storage.BrowserURL)
	}
	return err
}

func buildInspectStorageResponse(ctx context.Context, plan *storageNamespacePlan, cfg appcfg.Config, withStats bool) (inspectStorageResponse, error) {
	declared := len(cfg.Storage.Stores) > 0
	storage := inspectStorageRecord{Configured: declared, Declared: declared, Default: strings.TrimSpace(cfg.Storage.Default), Readiness: "not_configured", Scope: storageScope(plan, storagefs.Owner{}, storageCLIOptions{})}
	if declared {
		storage.Readiness = "uninitialized"
	}
	stores := make([]inspectStorageStore, 0, len(cfg.Storage.Stores))
	names := make([]string, 0, len(cfg.Storage.Stores))
	for name, policy := range cfg.Storage.Stores {
		names = append(names, name)
		stores = append(stores, inspectStorageStore{Name: name, Kind: firstNonEmpty(policy.Kind, "local"), Access: firstNonEmpty(policy.Access, "auth"), TenantScoped: policy.TenantScoped, MaxObjectBytes: policy.MaxObjectBytes})
	}
	sort.Slice(stores, func(i, j int) bool { return stores[i].Name < stores[j].Name })
	owner, err := plan.discover(ctx)
	if err != nil && !errors.Is(err, storagefs.ErrUninitialized) {
		return inspectStorageResponse{}, err
	}
	if err == nil {
		storage.Configured = true
		storage.Readiness = owner.State
		storage.Scope = storageScope(plan, owner, storageCLIOptions{})
		n, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
		if err != nil {
			return inspectStorageResponse{}, err
		}
		pending, err := n.Pending(ctx)
		if err != nil {
			return inspectStorageResponse{}, err
		}
		storage.Recovery = pending.Recovery()
		if storage.Recovery != nil {
			storage.Readiness = "recovery_required"
		}
		if withStats {
			stats, err := n.Statistics(ctx, names)
			if err != nil {
				return inspectStorageResponse{}, err
			}
			storage.Totals = &stats.Total
			for i := range stores {
				total := stats.Stores[stores[i].Name]
				stores[i].ObjectCount = &total.Objects
				stores[i].TotalBytes = &total.Bytes
			}
		}
	} else if withStats {
		storage.Totals = &storagefs.Stats{}
		for i := range stores {
			zeroCount, zeroBytes := int64(0), int64(0)
			stores[i].ObjectCount = &zeroCount
			stores[i].TotalBytes = &zeroBytes
		}
	}
	storage.BrowserURL = storageBrowserURL(plan)
	return inspectStorageResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.inspect"), App: inspectAppInfo(plan.Binding.AppRoot, cfg, nil), Storage: storage, Stores: stores}, nil
}

func storageBrowserURL(plan *storageNamespacePlan) string {
	live, err := plan.Worktree.ProbeLiveLock()
	if err != nil || !live {
		return ""
	}
	record, err := plan.Worktree.LoadRecord(plan.Binding.AppID)
	if err != nil {
		return ""
	}
	registry, err := plan.Worktree.OpenRegistry(record.RouterAddress)
	if err != nil {
		return ""
	}
	for _, session := range registry.List() {
		if session.AppRoot == plan.Binding.AppRoot && session.BaseAppID == plan.Binding.AppID {
			browser, err := url.Parse(session.RouteManifest.Routes[localagent.RouteDashboard].URL)
			if err != nil || browser.Host == "" {
				return ""
			}
			query := browser.Query()
			query.Set("page", "Storage")
			query.Set("app", session.SessionID)
			browser.RawQuery = query.Encode()
			return browser.String()
		}
	}
	return ""
}
