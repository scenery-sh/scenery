package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

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

func ensureAppTemplateDatabase(
	ctx context.Context,
	root string,
	cfg appcfg.Config,
	contract *compiler.Result,
	admin *sql.DB,
	adminServerURL string,
	baseEnv []string,
) (string, bool, time.Duration, []compiler.SQLBinding, error) {
	cfgs, err := resolveSQLSupply(contract.SQLRequirements, baseEnv, true)
	if err != nil {
		return "", false, 0, nil, err
	}
	if len(cfgs) == 0 {
		return "", false, 0, nil, nil
	}
	setup, hasWork, err := buildDevDatabaseSetup(root, cfg, contract)
	if err != nil {
		return "", false, 0, nil, err
	}
	schemaParts := make([]string, 0, len(cfgs)+1)
	schemaParts = append(schemaParts, "setup:"+setup.Fingerprint)
	for _, svc := range cfgs {
		schemaParts = append(schemaParts, "schema:"+svc.Name+":"+svc.Schema)
	}
	slices.Sort(schemaParts)
	templateName := postgresdb.TemplateDatabaseName(cfg.AppID(), strings.Join(schemaParts, "\n"))

	started := time.Now()
	hit, err := postgresdb.EnsureTemplateDatabase(ctx, admin, adminServerURL, cfg.AppID(), templateName, func(ctx context.Context, templateURL string) error {
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
		_, err = declaredDatabaseSetup{
			root:     root,
			cfg:      cfg,
			contract: contract,
			env:      tplEnv,
		}.apply(ctx, setup, connections)
		return err
	})
	if err != nil {
		return "", false, 0, nil, err
	}
	return templateName, hit, time.Since(started), cfgs, nil
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
	var canonicalRoot = createdRoot
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
		resolver, err := newWorktreePostgresResolver(ctx, createdRoot, cfg.AppID())
		if err != nil {
			return nil, err
		}
		canonicalRoot = resolver.paths.AppRoot
		wasRunning := false
		if record, loadErr := resolver.load(); loadErr == nil {
			if _, container, inspErr := resolver.inspect(ctx, record); inspErr == nil && container != nil && container.Running {
				wasRunning = true
			}
		}
		server, err := resolver.ensure(ctx)
		if err != nil {
			return nil, err
		}
		if !wasRunning {
			stopAfter = func() error {
				stopCtx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				return resolver.stop(stopCtx)
			}
		}
		adminURL = worktreePostgresURL(server, "postgres")
	}
	if stopAfter != nil {
		defer func() { returnErr = errors.Join(returnErr, stopAfter()) }()
	}

	admin, err := openPostgresAdmin(ctx, adminURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres admin for worktree database preparation: %w", err)
	}
	defer func() { _ = admin.Close() }()

	templateName, hit, _, _, err := ensureAppTemplateDatabase(ctx, canonicalRoot, cfg, contract, admin, adminURL, baseEnv)
	if err != nil {
		return nil, err
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
