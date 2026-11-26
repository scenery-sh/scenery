package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

type worktreePreparedDatabase struct {
	Name        string `json:"name"`
	Template    string `json:"template"`
	TemplateHit bool   `json:"template_hit"`
	Cloned      bool   `json:"cloned"`
	CloneMS     int64  `json:"clone_ms"`
}

func templateDatabaseSignature(root string, cfg appcfg.Config, setup devDatabaseSetup, cfgs []compiler.SQLBinding) (string, error) {
	schemaParts := make([]string, 0, len(cfgs)+2)
	schemaParts = append(schemaParts, "setup:"+setup.Fingerprint)
	if strings.TrimSpace(cfg.Database.Apply.Command) != "" {
		snapshot, err := scanWatchedFiles(root)
		if err != nil {
			return "", fmt.Errorf("fingerprint database apply sources: %w", err)
		}
		schemaParts = append(schemaParts, "apply-sources:"+snapshotFingerprint(snapshot))
	}
	for _, svc := range cfgs {
		schemaParts = append(schemaParts, "schema:"+svc.Name+":"+svc.Schema)
	}
	slices.Sort(schemaParts)
	return strings.Join(schemaParts, "\n"), nil
}

func ensureAppTemplateDatabase(
	ctx context.Context,
	root string,
	cfg appcfg.Config,
	contract *compiler.Result,
	admin *sql.DB,
	adminServerURL string,
	baseEnv []string,
) (string, bool, time.Duration, []compiler.SQLBinding, func() error, error) {
	cfgs, err := resolveSQLSupply(contract.SQLRequirements, baseEnv, true)
	if err != nil {
		return "", false, 0, nil, nil, err
	}
	if len(cfgs) == 0 {
		return "", false, 0, nil, nil, nil
	}
	setup, hasWork, err := buildDevDatabaseSetup(root, cfg, contract)
	if err != nil {
		return "", false, 0, nil, nil, err
	}
	signature, err := templateDatabaseSignature(root, cfg, setup, cfgs)
	if err != nil {
		return "", false, 0, nil, nil, err
	}
	templateName := postgresdb.TemplateDatabaseName(cfg.AppID(), signature)

	started := time.Now()
	hit, releaseLease, err := postgresdb.HoldTemplateDatabase(ctx, admin, adminServerURL, cfg.AppID(), templateName, func(ctx context.Context, templateURL string) error {
		tplDB, err := openPostgresDatabase(ctx, templateURL)
		if err != nil {
			return fmt.Errorf("connect staging template database %s: %w", templateName, err)
		}
		if err := postgresdb.EnsureSchema(ctx, tplDB, "scenery"); err != nil {
			_ = tplDB.Close()
			return err
		}
		services := make([]postgresdb.Service, 0, len(cfgs))
		for _, svc := range cfgs {
			if err := postgresdb.EnsureSchema(ctx, tplDB, svc.Schema); err != nil {
				_ = tplDB.Close()
				return err
			}
			serviceURL, err := postgresdb.ServiceURL(templateURL, svc.Schema)
			if err != nil {
				_ = tplDB.Close()
				return err
			}
			services = append(services, postgresdb.Service{Name: svc.Name, Schema: svc.Schema, URL: serviceURL})
		}
		_ = tplDB.Close()
		if !hasWork {
			return nil
		}
		database := postgresdb.Database{
			Database: templateName,
			URL:      templateURL,
			Source:   postgresdb.SourceManaged,
			Schemas:  services,
			AppRoot:  root,
		}
		managed := postgresdb.Env(database)
		tplEnv := append([]string{}, baseEnv...)
		tplEnv = append(tplEnv, managed...)
		tplEnv = append(tplEnv, managedDatabaseSetupEnv(contract.SQLRequirements, managed)...)
		connections := newDBSetupConnections()
		defer func() { _ = connections.Close() }()
		var setupLog bytes.Buffer
		_, err = declaredDatabaseSetup{
			root:     root,
			cfg:      cfg,
			contract: contract,
			env:      tplEnv,
			stdout:   &setupLog,
			stderr:   &setupLog,
		}.apply(ctx, setup, connections)
		if err != nil && setupLog.Len() > 0 {
			return fmt.Errorf("%w\n%s", err, strings.TrimSpace(setupLog.String()))
		}
		return err
	})
	if err != nil {
		return "", false, 0, nil, nil, err
	}
	return templateName, hit, time.Since(started), cfgs, releaseLease, nil
}

func waitForPostgresOperation(ctx context.Context, resolver worktreePostgresResolver) (worktreePostgresOperation, error) {
	for {
		op, err := resolver.beginOperation()
		if !errors.Is(err, localagent.ErrProcessLocked) {
			return op, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// acquireManagedPostgresHarness holds a shared lease on the worktree's managed
// PostgreSQL server for the duration of a test or worktree preparation run. If
// the server was initially stopped, only the last releasing harness stops it,
// and never when a live worktree owner (such as scenery up) has acquired the
// worktree live lock in the meantime.
func acquireManagedPostgresHarness(ctx context.Context, root, appID string) (string, string, func() error, error) {
	resolver, err := newWorktreePostgresResolver(ctx, root, appID)
	if err != nil {
		return "", "", nil, err
	}
	return acquireManagedPostgresHarnessWithResolver(ctx, resolver)
}

func acquireManagedPostgresHarnessWithResolver(ctx context.Context, resolver worktreePostgresResolver) (string, string, func() error, error) {
	if err := resolver.paths.Prepare(); err != nil {
		return "", "", nil, err
	}
	lockPath := filepath.Join(resolver.paths.Directory, "postgres-harness.lock")
	autostopPath := filepath.Join(resolver.paths.Directory, "postgres-harness.autostop")

	op, err := waitForPostgresOperation(ctx, resolver)
	if err != nil {
		return "", "", nil, err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		_ = op.Close()
		return "", "", nil, err
	}
	if err := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_SH); err != nil {
		_ = lockFile.Close()
		_ = op.Close()
		return "", "", nil, err
	}
	wasRunning := false
	if record, loadErr := resolver.load(); loadErr == nil {
		if _, container, inspErr := resolver.inspect(ctx, record); inspErr == nil && container != nil && container.Running {
			wasRunning = true
		}
	}
	if !wasRunning {
		if err := os.WriteFile(autostopPath, []byte("1\n"), 0o600); err != nil {
			_ = lockFile.Close()
			_ = op.Close()
			return "", "", nil, err
		}
	}
	server, err := resolver.ensureWithOperation(ctx, op, true)
	closeOpErr := op.Close()
	if err := errors.Join(err, closeOpErr); err != nil {
		_ = lockFile.Close()
		return "", "", nil, err
	}

	release := func() error {
		stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		relOp, opErr := waitForPostgresOperation(stopCtx, resolver)
		if opErr != nil {
			_ = lockFile.Close()
			return opErr
		}
		if flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); flockErr != nil {
			// Another harness still holds a shared lease. Close our descriptor
			// before releasing relOp so the remaining holder becomes sole owner.
			closeErr := lockFile.Close()
			return errors.Join(closeErr, relOp.Close())
		}
		if _, statErr := os.Lstat(autostopPath); errors.Is(statErr, os.ErrNotExist) {
			return errors.Join(lockFile.Close(), relOp.Close())
		}
		_ = os.Remove(autostopPath)
		live, liveErr := resolver.paths.AcquireLiveLock()
		if liveErr != nil {
			// A live supervisor or database mutation now owns the server.
			return errors.Join(lockFile.Close(), relOp.Close())
		}
		var stopErr error
		if record, loadErr := resolver.load(); loadErr == nil {
			if _, container, inspErr := resolver.inspect(stopCtx, record); inspErr == nil && container != nil && container.Running {
				if err := resolver.docker.Stop(stopCtx, record, container.ID); err != nil {
					stopErr = err
				} else {
					stopErr = relOp.SaveRecord(record)
				}
			}
		} else if !errors.Is(loadErr, os.ErrNotExist) {
			stopErr = loadErr
		}
		return errors.Join(stopErr, live.Release(), lockFile.Close(), relOp.Close())
	}

	return resolver.paths.AppRoot, worktreePostgresURL(server, "postgres"), release, nil
}

func prepareWorktreeDatabase(ctx context.Context, createdRoot string, cfg appcfg.Config) (_ *worktreePreparedDatabase, returnErr error) {
	contract, err := compileSQLContract(createdRoot)
	if err != nil {
		return nil, err
	}
	if len(contract.SQLRequirements) == 0 {
		return nil, nil
	}
	baseEnv := envpolicy.Environ()
	var adminURL string
	canonicalRoot := createdRoot
	var stopAfter func() error

	if externalURL := lookupEnvValue(baseEnv, appDatabaseURLEnv); externalURL != "" {
		if err := validateAppPostgresURL(externalURL); err != nil {
			return nil, err
		}
		adminURL, err = postgresdb.DatabaseURLWithName(externalURL, "postgres")
		if err != nil {
			return nil, err
		}
	} else {
		canonicalRoot, adminURL, stopAfter, err = acquireManagedPostgresHarness(ctx, createdRoot, cfg.AppID())
		if err != nil {
			return nil, err
		}
	}
	if stopAfter != nil {
		defer func() { returnErr = errors.Join(returnErr, stopAfter()) }()
	}

	admin, err := openPostgresAdmin(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres admin for worktree database preparation: %w", err)
	}
	defer func() { _ = admin.Close() }()

	templateName, hit, _, _, releaseTemplate, err := ensureAppTemplateDatabase(ctx, canonicalRoot, cfg, contract, admin, adminURL, baseEnv)
	if err != nil {
		return nil, err
	}
	if releaseTemplate != nil {
		defer func() { _ = releaseTemplate() }()
	}
	if templateName == "" {
		return nil, nil
	}
	targetName := postgresname.DatabaseNameFor(cfg.AppID(), canonicalRoot)
	cloned, cloneDur, err := postgresdb.CloneDatabaseFromTemplate(ctx, admin, targetName, templateName, false)
	if err != nil {
		return nil, err
	}
	return &worktreePreparedDatabase{
		Name:        targetName,
		Template:    templateName,
		TemplateHit: hit,
		Cloned:      cloned,
		CloneMS:     cloneDur.Milliseconds(),
	}, nil
}
