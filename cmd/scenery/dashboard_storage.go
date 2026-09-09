package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/contract"
	"scenery.sh/internal/storagefs"
)

// Dashboard selectors name a registered app, never a caller-supplied root.
// Scope pins are freshness checks, not credentials. The maintenance lease keeps
// the checked generation stable through each request or streamed transfer.
type dashboardStorageRequest struct {
	AppID             string `json:"app_id"`
	WorktreeKey       string `json:"worktree_key"`
	Incarnation       string `json:"incarnation"`
	Generation        string `json:"generation"`
	Store             string `json:"store"`
	Tenant            string `json:"tenant"`
	Key               string `json:"key"`
	Prefix            string `json:"prefix"`
	Cursor            string `json:"cursor"`
	Limit             int    `json:"limit"`
	Stats             bool   `json:"stats"`
	IfMatch           string `json:"if_match"`
	SelectionRevision string `json:"selection_revision"`
}

type dashboardStorageFailure struct{ storagefs.Failure }

func (e *dashboardStorageFailure) Error() string { return e.Diagnostic + ": " + e.Message }

func dashboardStorageError(err error) error {
	failure, ok := storagefs.DescribeError(err)
	if !ok {
		failure = storagefs.InternalFailure()
	}
	return &dashboardStorageFailure{failure}
}

func (s *dashboardServer) dashboardStoragePlan(ctx context.Context, appID string) (*storageNamespacePlan, appcfg.Config, error) {
	if appID == "" {
		return nil, appcfg.Config{}, fmt.Errorf("%w: select a registered app", storagefs.ErrInvalid)
	}
	status, err := s.dashboardStatusFor(ctx, appID)
	if err != nil || !filepath.IsAbs(status.AppRoot) {
		return nil, appcfg.Config{}, fmt.Errorf("%w: selected dashboard app is unavailable", storagefs.ErrOwnership)
	}
	root, cfg, err := appcfg.DiscoverRoot(status.AppRoot)
	if err != nil {
		return nil, cfg, err
	}
	if status.BaseAppID != "" && status.BaseAppID != cfg.AppID() {
		return nil, cfg, storagefs.ErrOwnership
	}
	plan, err := resolveStorageNamespacePlan(cfg, root, "")
	if err == nil {
		expected, pathErr := commandWorktreePaths(status.AppRoot)
		if pathErr != nil {
			return nil, cfg, pathErr
		}
		if expected.Key != plan.Worktree.Key {
			return nil, cfg, storagefs.ErrOwnership
		}
	}
	return plan, cfg, err
}

func checkDashboardStoragePin(plan *storageNamespacePlan, owner storagefs.Owner, req dashboardStorageRequest) error {
	if req.WorktreeKey != plan.Worktree.Key || req.Incarnation != owner.Incarnation || req.Generation != owner.Generation {
		return fmt.Errorf("%w: storage scope changed; refresh the selected worktree", storagefs.ErrPrecondition)
	}
	return nil
}

func (s *dashboardServer) openDashboardStorage(ctx context.Context, req dashboardStorageRequest) (*storagefs.Store, storageResponseScope, io.Closer, error) {
	plan, cfg, err := s.dashboardStoragePlan(ctx, req.AppID)
	if err != nil {
		return nil, storageResponseScope{}, nil, err
	}
	opts := storageCLIOptions{Store: req.Store, Tenant: req.Tenant}
	store, owner, err := storageStoreForCLI(ctx, cfg, plan, opts, false)
	if err != nil {
		return nil, storageResponseScope{}, nil, err
	}
	n, err := storagefs.Bind(plan.Root, plan.Binding, owner.Incarnation)
	if err != nil {
		return nil, storageResponseScope{}, nil, err
	}
	lease, err := n.HoldTask(ctx)
	if err != nil {
		return nil, storageResponseScope{}, nil, err
	}
	owner, err = plan.discover(ctx)
	if err == nil {
		err = checkDashboardStoragePin(plan, owner, req)
	}
	if err != nil {
		return nil, storageResponseScope{}, nil, errors.Join(err, lease.Close())
	}
	return store, storageScope(plan, owner, opts), lease, nil
}

func (s *dashboardServer) storageRPC(ctx context.Context, method string, raw json.RawMessage) (_ any, returnErr error) {
	defer func() {
		if returnErr != nil {
			returnErr = dashboardStorageError(returnErr)
		}
	}()
	if len(raw) > 64<<10 {
		return nil, fmt.Errorf("%w: storage request exceeds 64 KiB", storagefs.ErrInvalid)
	}
	if _, err := contract.DecodeJSONObject(raw); err != nil {
		return nil, fmt.Errorf("%w: malformed storage request", storagefs.ErrInvalid)
	}
	var req dashboardStorageRequest
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		return nil, fmt.Errorf("%w: malformed storage request", storagefs.ErrInvalid)
	}
	if method == "storage/inspect" {
		plan, cfg, err := s.dashboardStoragePlan(ctx, req.AppID)
		if err != nil {
			return nil, err
		}
		return buildInspectStorageResponse(ctx, plan, cfg, req.Stats)
	}
	store, scope, lease, err := s.openDashboardStorage(ctx, req)
	if err != nil {
		return nil, err
	}
	defer func() { returnErr = errors.Join(returnErr, lease.Close()) }()
	switch method {
	case "storage/list":
		page, err := store.List(ctx, storagefs.ListOptions{Prefix: req.Prefix, Cursor: req.Cursor, Limit: req.Limit})
		if err != nil {
			return nil, err
		}
		return storageListResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.list"), Scope: scope, Page: *page}, nil
	case "storage/stat":
		object, err := store.Head(ctx, req.Key)
		if err != nil {
			return nil, err
		}
		return storageObjectResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.object"), Scope: scope, Object: *object}, nil
	case "storage/delete":
		if req.IfMatch == "" {
			return nil, fmt.Errorf("%w: deletion requires the displayed object version", storagefs.ErrPrecondition)
		}
		err := store.Delete(ctx, req.Key, storagefs.DeleteOptions{IfMatch: req.IfMatch})
		return storageDeleteResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.delete"), Scope: scope, Key: req.Key, Deleted: err == nil}, err
	case "storage/delete-preview":
		preview, err := store.PreviewDelete(ctx, req.Prefix)
		return storageDeleteResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.delete"), Scope: scope, Prefix: req.Prefix, DryRun: true, Preview: &preview}, err
	case "storage/delete-selection":
		result, err := store.ApplyDelete(ctx, req.Prefix, req.SelectionRevision)
		return storageDeleteResponse{cliPayloadIdentity: newCLIPayloadIdentity("scenery.storage.delete"), Scope: scope, Prefix: req.Prefix, Result: &result}, err
	default:
		return nil, fmt.Errorf("%w: unknown storage method", storagefs.ErrInvalid)
	}
}
