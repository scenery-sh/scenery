package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/sqlitedb"
)

const sqliteBranchProviderName = "sqlite"

type sqliteBranchProvider struct {
	cfg appcfg.Config
}

func (p sqliteBranchProvider) EnsureBranch(ctx context.Context, pin worktreeDBPin) (dbBranchBackendStatus, error) {
	if err := validateSQLiteBranchLeaseWritable(pin); err != nil {
		return dbBranchBackendStatus{}, err
	}
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{
		AppRoot:  pin.WorktreeRoot,
		Config:   p.cfg,
		Mode:     sqlitedb.ModeBranch,
		BranchID: pin.BranchID,
	})
	if err != nil {
		return dbBranchBackendStatus{}, err
	}
	parent, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{
		AppRoot:  pin.WorktreeRoot,
		Config:   p.cfg,
		Mode:     sqlitedb.ModeBranch,
		BranchID: "main",
	})
	if err != nil {
		return dbBranchBackendStatus{}, err
	}
	for i, svc := range services {
		if _, err := os.Stat(svc.Path); err == nil {
			continue
		}
		if _, err := os.Stat(parent[i].Path); err == nil {
			if err := sqlitedb.Backup(ctx, parent[i].Path, svc.Path); err != nil {
				return dbBranchBackendStatus{}, err
			}
			continue
		}
		if err := sqlitedb.EnsureFiles(ctx, []sqlitedb.Service{svc}); err != nil {
			return dbBranchBackendStatus{}, err
		}
	}
	if err := upsertSQLiteBranchLease(pin, nil, "ready"); err != nil {
		return dbBranchBackendStatus{}, err
	}
	return p.InspectBranch(ctx, pin), nil
}

func (p sqliteBranchProvider) InspectBranch(ctx context.Context, pin worktreeDBPin) dbBranchBackendStatus {
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{
		AppRoot:  pin.WorktreeRoot,
		Config:   p.cfg,
		Mode:     sqlitedb.ModeBranch,
		BranchID: pin.BranchID,
	})
	if err != nil {
		return dbBranchBackendStatus{Status: "missing", Message: err.Error()}
	}
	for _, svc := range services {
		if _, err := os.Stat(svc.Path); err != nil {
			return dbBranchBackendStatus{Status: "missing", Message: err.Error()}
		}
	}
	endpoint := &dbBranchEndpoint{Database: pin.Database, Source: "sqlite"}
	return dbBranchBackendStatus{Status: "ready", Endpoint: endpoint}
}

func (p sqliteBranchProvider) Connection(ctx context.Context, pin worktreeDBPin) (dbBranchConnectionInfo, error) {
	status := p.InspectBranch(ctx, pin)
	if status.Status != "ready" {
		return dbBranchConnectionInfo{}, fmt.Errorf("sqlite branch %q is not ready: %s", pin.Branch, status.Message)
	}
	service, err := sqlitedb.ResolveService(sqlitedb.ResolveRequest{
		AppRoot:  pin.WorktreeRoot,
		Config:   p.cfg,
		Mode:     sqlitedb.ModeBranch,
		BranchID: pin.BranchID,
	}, "")
	if err != nil {
		return dbBranchConnectionInfo{}, err
	}
	return dbBranchConnectionInfo{
		DatabaseURL:  service.URL,
		DatabaseName: pin.Database,
		Endpoint:     *status.Endpoint,
	}, nil
}

func (p sqliteBranchProvider) ResetBranch(ctx context.Context, pin worktreeDBPin, _ dbBranchOptions) error {
	if isProtectedDBParentBranch(pin) {
		return fmt.Errorf("refusing to reset protected parent branch %q", pin.Branch)
	}
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{AppRoot: pin.WorktreeRoot, Config: p.cfg, Mode: sqlitedb.ModeBranch, BranchID: pin.BranchID})
	if err != nil {
		return err
	}
	parent, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{AppRoot: pin.WorktreeRoot, Config: p.cfg, Mode: sqlitedb.ModeBranch, BranchID: "main"})
	if err != nil {
		return err
	}
	for i, svc := range services {
		if _, err := os.Stat(parent[i].Path); err == nil {
			if err := sqlitedb.Backup(ctx, parent[i].Path, svc.Path); err != nil {
				return err
			}
			continue
		}
		_ = os.Remove(svc.Path)
		if err := sqlitedb.EnsureFiles(ctx, []sqlitedb.Service{svc}); err != nil {
			return err
		}
	}
	return upsertSQLiteBranchLease(pin, nil, "ready")
}

func (p sqliteBranchProvider) DeleteBranch(ctx context.Context, current worktreeDBPin, branch string, _ dbBranchOptions) error {
	target := current
	target.Branch = normalizeDBBranchName(branch)
	target.BranchID = dbLocalBranchID(target.Project, target.Branch)
	services, err := sqlitedb.ResolveServices(sqlitedb.ResolveRequest{AppRoot: target.WorktreeRoot, Config: p.cfg, Mode: sqlitedb.ModeBranch, BranchID: target.BranchID})
	if err != nil {
		return err
	}
	for _, svc := range services {
		if err := os.Remove(svc.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_ = os.Remove(filepath.Dir(services[0].Path))
	return deleteSQLiteBranchLease(target)
}

func (p sqliteBranchProvider) RestoreBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) (dbBranchRestorePoint, error) {
	if err := p.ResetBranch(ctx, pin, opts); err != nil {
		return dbBranchRestorePoint{}, err
	}
	return dbBranchRestorePoint{Ref: firstNonEmpty(opts.At, "main"), Source: "sqlite-template", Branch: pin.Branch, BranchID: pin.BranchID, Project: pin.Project, DatabaseName: pin.Database, CreatedAt: time.Now().UTC()}, nil
}

func (p sqliteBranchProvider) DiffBranch(ctx context.Context, pin worktreeDBPin, target string, _ dbBranchOptions) (string, error) {
	current, err := sqlitedb.ResolveService(sqlitedb.ResolveRequest{AppRoot: pin.WorktreeRoot, Config: p.cfg, Mode: sqlitedb.ModeBranch, BranchID: pin.BranchID}, "")
	if err != nil {
		return "", err
	}
	targetPin := pin
	targetPin.Branch = normalizeDBBranchName(target)
	targetPin.BranchID = dbLocalBranchID(pin.Project, targetPin.Branch)
	other, err := sqlitedb.ResolveService(sqlitedb.ResolveRequest{AppRoot: pin.WorktreeRoot, Config: p.cfg, Mode: sqlitedb.ModeBranch, BranchID: targetPin.BranchID}, "")
	if err != nil {
		return "", err
	}
	a, err := sqlitedb.DumpSchema(ctx, current.Path)
	if err != nil {
		return "", err
	}
	b, err := sqlitedb.DumpSchema(ctx, other.Path)
	if err != nil {
		return "", err
	}
	if a == b {
		return "schemas match\n", nil
	}
	return strings.Join([]string{"--- current", "+++ " + targetPin.Branch, a, b}, "\n"), nil
}

func validateSQLiteBranchLeaseWritable(pin worktreeDBPin) error {
	if pin.Provider != sqliteBranchProviderName || pin.CreatedBy != "scenery" {
		return fmt.Errorf("refusing to mutate non-scenery sqlite branch lease")
	}
	if strings.TrimSpace(pin.WorktreeRoot) == "" {
		return fmt.Errorf("sqlite branch lease missing worktree root")
	}
	return nil
}
