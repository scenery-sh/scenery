package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lib/pq"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/envpolicy"
)

const (
	postgresBranchProviderName          = "postgres"
	postgresBranchRegistrySchemaVersion = "scenery.db.branch.registry.v2"
	postgresDefaultMode                 = "local"
	postgresDefaultBranchStrategy       = "template_database"
)

type postgresBranchProvider struct {
	cfg appcfg.Config
}

func postgresSubstrateRoot() (string, error) {
	paths, err := localagent.DefaultPaths()
	if err != nil {
		return "", err
	}
	return filepath.Join(paths.AgentDir, "postgres"), nil
}

func readPostgresBranchRegistryForDefaultRoot() (dbBranchRegistry, string, error) {
	root, err := postgresSubstrateRoot()
	if err != nil {
		return dbBranchRegistry{}, "", err
	}
	registry, err := readPostgresBranchRegistry(root)
	if err != nil {
		return dbBranchRegistry{}, "", err
	}
	return registry, dbBranchRegistryPath(root), nil
}

func readPostgresBranchRegistry(root string) (dbBranchRegistry, error) {
	path := dbBranchRegistryPath(root)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return dbBranchRegistry{
			SchemaVersion: postgresBranchRegistrySchemaVersion,
			Provider:      postgresBranchProviderName,
			Leases:        []dbBranchLease{},
		}, nil
	}
	if err != nil {
		return dbBranchRegistry{}, err
	}
	registry, err := decodeBranchRegistry(path, data)
	if err != nil {
		return dbBranchRegistry{}, err
	}
	if registry.SchemaVersion != postgresBranchRegistrySchemaVersion {
		return dbBranchRegistry{}, fmt.Errorf("%s has unsupported schema_version %q", path, registry.SchemaVersion)
	}
	if registry.Provider != postgresBranchProviderName {
		return dbBranchRegistry{}, fmt.Errorf("%s has unsupported provider %q", path, registry.Provider)
	}
	if registry.Leases == nil {
		registry.Leases = []dbBranchLease{}
	}
	return registry, nil
}

func writePostgresBranchRegistry(root string, registry dbBranchRegistry) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	registry.SchemaVersion = postgresBranchRegistrySchemaVersion
	registry.Provider = postgresBranchProviderName
	if registry.Leases == nil {
		registry.Leases = []dbBranchLease{}
	}
	return writeBranchRegistryFile(dbBranchRegistryPath(root), registry)
}

func mutatePostgresBranchRegistry(root string, mutate func(*dbBranchRegistry) error) error {
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		return err
	}
	defer unlock()
	registry, err := readPostgresBranchRegistry(root)
	if err != nil {
		return err
	}
	if err := mutate(&registry); err != nil {
		return err
	}
	return writePostgresBranchRegistry(root, registry)
}

func upsertPostgresBranchLease(pin worktreeDBPin, endpoint *dbBranchEndpoint, status string) error {
	root, err := postgresSubstrateRoot()
	if err != nil {
		return err
	}
	return mutatePostgresBranchRegistry(root, func(registry *dbBranchRegistry) error {
		now := time.Now().UTC()
		nowText := now.Format(time.RFC3339)
		expiresAt := dbBranchLeaseExpiresAt(now, pin.TTL)
		status = firstNonEmpty(strings.TrimSpace(status), "pending")
		for i := range registry.Leases {
			if sameDBBranchLease(registry.Leases[i].Pin, pin) || sameDBBranch(registry.Leases[i].Pin, pin) {
				if !isSceneryOwnedDBLease(registry.Leases[i]) {
					return fmt.Errorf("refusing to reuse foreign local Postgres branch lease %q; remove or rename that lease before checkout", pin.Branch)
				}
				createdAt := registry.Leases[i].CreatedAt
				if createdAt == "" {
					createdAt = nowText
				}
				if endpoint == nil {
					endpoint = registry.Leases[i].Endpoint
				}
				registry.Leases[i] = dbBranchLease{
					Pin:       pin,
					Status:    status,
					Endpoint:  endpoint,
					CreatedAt: createdAt,
					UpdatedAt: nowText,
					ExpiresAt: expiresAt,
				}
				registry.UpdatedAt = nowText
				return nil
			}
		}
		registry.Leases = append(registry.Leases, dbBranchLease{
			Pin:       pin,
			Status:    status,
			Endpoint:  endpoint,
			CreatedAt: nowText,
			UpdatedAt: nowText,
			ExpiresAt: expiresAt,
		})
		registry.UpdatedAt = nowText
		return nil
	})
}

func findPostgresBranchLease(pin worktreeDBPin) (dbBranchLease, bool, error) {
	root, err := postgresSubstrateRoot()
	if err != nil {
		return dbBranchLease{}, false, err
	}
	registry, err := readPostgresBranchRegistry(root)
	if err != nil {
		return dbBranchLease{}, false, err
	}
	for _, lease := range registry.Leases {
		if !isSceneryOwnedDBLease(lease) {
			continue
		}
		if sameDBBranchLease(lease.Pin, pin) || sameDBBranch(lease.Pin, pin) {
			return lease, true, nil
		}
	}
	return dbBranchLease{}, false, nil
}

func validatePostgresBranchLeaseWritable(pin worktreeDBPin) error {
	root, err := postgresSubstrateRoot()
	if err != nil {
		return err
	}
	registry, err := readPostgresBranchRegistry(root)
	if err != nil {
		return err
	}
	for _, lease := range registry.Leases {
		if sameDBBranchLease(lease.Pin, pin) || sameDBBranch(lease.Pin, pin) {
			if !isSceneryOwnedDBLease(lease) {
				return fmt.Errorf("refusing to reuse foreign local Postgres branch lease %q; remove or rename that lease before checkout", pin.Branch)
			}
		}
	}
	return nil
}

func (p postgresBranchProvider) EnsureBranch(ctx context.Context, pin worktreeDBPin) (dbBranchBackendStatus, error) {
	if isProtectedDBParentBranch(pin) {
		if err := upsertPostgresBranchLease(pin, nil, "protected"); err != nil {
			return dbBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
		}
		return dbBranchBackendStatus{
			Status:  "protected",
			Message: fmt.Sprintf("Postgres branch lease %q is the protected parent database.", pin.Branch),
		}, nil
	}
	adminURL, err := p.adminURL(ctx)
	if err != nil {
		_ = upsertPostgresBranchLease(pin, nil, "pending")
		return dbBranchBackendStatus{Status: "pending", Message: err.Error()}, err
	}
	root, err := postgresSubstrateRoot()
	if err != nil {
		_ = upsertPostgresBranchLease(pin, nil, "pending")
		return dbBranchBackendStatus{Status: "pending", Message: err.Error()}, err
	}
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		_ = upsertPostgresBranchLease(pin, nil, "pending")
		return dbBranchBackendStatus{Status: "pending", Message: err.Error()}, err
	}
	if err := p.ensureTemplateDatabaseBranch(ctx, adminURL, pin); err != nil {
		unlock()
		_ = upsertPostgresBranchLease(pin, nil, "pending")
		return dbBranchBackendStatus{Status: "pending", Message: err.Error()}, err
	}
	unlock()
	endpoint, err := postgresBranchEndpoint(adminURL, pin)
	if err != nil {
		return dbBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
	}
	if err := upsertPostgresBranchLease(pin, &endpoint, "ready"); err != nil {
		return dbBranchBackendStatus{Status: "unknown", Message: err.Error()}, err
	}
	return dbBranchBackendStatus{
		Status:   "ready",
		Message:  "Postgres branch database is ready.",
		Endpoint: &endpoint,
	}, nil
}

func (p postgresBranchProvider) InspectBranch(ctx context.Context, pin worktreeDBPin) dbBranchBackendStatus {
	lease, ok, err := findPostgresBranchLease(pin)
	if err != nil {
		return dbBranchBackendStatus{Status: "unknown", Message: err.Error()}
	}
	if !ok {
		return dbBranchBackendStatus{Status: "missing", Message: "Local branch pin exists, but no Scenery-owned Postgres branch lease was found."}
	}
	if dbBranchLeaseExpired(lease, time.Now().UTC()) {
		return dbBranchBackendStatus{Status: "expired", Message: "Local Postgres branch lease is expired."}
	}
	if isProtectedDBParentBranch(pin) || lease.Status == "protected" {
		return dbBranchBackendStatus{Status: "protected", Message: fmt.Sprintf("Postgres branch lease %q is the protected parent database.", pin.Branch)}
	}
	if lease.Status != "ready" {
		return dbBranchBackendStatus{Status: firstNonEmpty(lease.Status, "pending"), Message: "Postgres branch lease is not ready yet."}
	}
	if lease.Endpoint == nil {
		return dbBranchBackendStatus{Status: "missing", Message: "Postgres branch lease is marked ready, but endpoint metadata is missing."}
	}
	endpoint := normalizedDBBranchEndpoint(*lease.Endpoint, pin)
	return dbBranchBackendStatus{Status: "ready", Message: "Postgres branch database is ready.", Endpoint: &endpoint}
}

func (p postgresBranchProvider) Connection(ctx context.Context, pin worktreeDBPin) (dbBranchConnectionInfo, error) {
	if isProtectedDBParentBranch(pin) {
		return dbBranchConnectionInfo{}, fmt.Errorf("Postgres branch lease %q is the protected parent database; refusing to expose it as an app-session database connection", pin.Branch)
	}
	lease, ok, err := findPostgresBranchLease(pin)
	if err != nil {
		return dbBranchConnectionInfo{}, err
	}
	if !ok || lease.Status != "ready" || lease.Endpoint == nil {
		if _, err := p.EnsureBranch(ctx, pin); err != nil {
			return dbBranchConnectionInfo{}, err
		}
		lease, ok, err = findPostgresBranchLease(pin)
		if err != nil {
			return dbBranchConnectionInfo{}, err
		}
	}
	if !ok || lease.Status != "ready" || lease.Endpoint == nil {
		return dbBranchConnectionInfo{}, fmt.Errorf("local Postgres branch lease %q is not ready", pin.Branch)
	}
	adminURL, err := p.adminURL(ctx)
	if err != nil {
		return dbBranchConnectionInfo{}, err
	}
	dsn, err := postgresDatabaseURL(adminURL, pin.Database)
	if err != nil {
		return dbBranchConnectionInfo{}, err
	}
	return dbBranchConnectionInfo{
		DatabaseURL:  dsn,
		DatabaseName: pin.Database,
		Endpoint:     normalizedDBBranchEndpoint(*lease.Endpoint, pin),
	}, nil
}

func (p postgresBranchProvider) ResetBranch(ctx context.Context, pin worktreeDBPin, _ dbBranchOptions) error {
	adminURL, err := p.adminURL(ctx)
	if err != nil {
		return err
	}
	if err := p.recreateBranchDatabase(ctx, adminURL, pin); err != nil {
		return err
	}
	endpoint, err := postgresBranchEndpoint(adminURL, pin)
	if err != nil {
		return err
	}
	return upsertPostgresBranchLease(pin, &endpoint, "ready")
}

func (p postgresBranchProvider) DeleteBranch(ctx context.Context, pin worktreeDBPin, branch string, opts dbBranchOptions) error {
	adminURL, err := p.adminURL(ctx)
	if err != nil {
		return err
	}
	root, err := postgresSubstrateRoot()
	if err != nil {
		return err
	}
	unlock, err := lockDBBranchRegistry(root)
	if err != nil {
		return err
	}
	defer unlock()
	registry, err := readPostgresBranchRegistry(root)
	if err != nil {
		return err
	}
	branch = normalizeDBBranchName(branch)
	kept := make([]dbBranchLease, 0, len(registry.Leases))
	var removed bool
	for _, lease := range registry.Leases {
		if !isSceneryOwnedDBLease(lease) || !dbLeaseMatchesBranchForDelete(lease, pin, branch) {
			kept = append(kept, lease)
			continue
		}
		if lease.Status == "ready" && strings.TrimSpace(lease.Pin.Database) != "" {
			if err := dropManagedPostgresDatabase(ctx, adminURL, lease.Pin.Database); err != nil {
				return err
			}
		}
		removed = true
	}
	if !removed {
		return fmt.Errorf("no Scenery-owned local Postgres branch lease found for %q", branch)
	}
	registry.Leases = kept
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	if err := writePostgresBranchRegistry(root, registry); err != nil {
		return err
	}
	if pin.Branch == branch && strings.TrimSpace(opts.AppRoot) != "" {
		if err := os.Remove(worktreeDBPinPath(opts.AppRoot)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

func (p postgresBranchProvider) RestoreBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) (dbBranchRestorePoint, error) {
	if err := p.ResetBranch(ctx, pin, opts); err != nil {
		return dbBranchRestorePoint{}, err
	}
	point := dbBranchRestorePoint{
		Ref:          firstNonEmpty(strings.TrimSpace(opts.At), "parent-template"),
		Source:       "parent-template",
		Branch:       pin.Branch,
		BranchID:     pin.BranchID,
		Project:      pin.Project,
		DatabaseName: pin.Database,
		CreatedAt:    time.Now().UTC(),
	}
	return point, nil
}

func (p postgresBranchProvider) DiffBranch(ctx context.Context, pin worktreeDBPin, target string, _ dbBranchOptions) (string, error) {
	if strings.TrimSpace(target) == "" {
		return "", fmt.Errorf("target branch is required")
	}
	adminURL, err := p.adminURL(ctx)
	if err != nil {
		return "", err
	}
	targetPin := pin
	targetPin.Branch = normalizeDBBranchName(target)
	targetPin.BranchID = dbLocalBranchID(pin.Project, targetPin.Branch)
	targetPin.Database = managedPostgresBranchDatabaseName(pin.Project, targetPin.Branch)
	if targetPin.Branch == normalizeDBBranchName(firstNonEmpty(pin.ParentBranch, dbBranchDefaultParentBranch)) {
		targetPin.Database = postgresParentDatabaseName(p.cfg, pin)
	}
	currentExists, err := postgresDatabaseExists(ctx, adminURL, pin.Database)
	if err != nil {
		return "", err
	}
	targetExists, err := postgresDatabaseExists(ctx, adminURL, targetPin.Database)
	if err != nil {
		return "", err
	}
	if !currentExists || !targetExists {
		return fmt.Sprintf("current: %s exists=%t\ntarget: %s exists=%t\n", pin.Database, currentExists, targetPin.Database, targetExists), nil
	}
	currentURL, err := postgresDatabaseURL(adminURL, pin.Database)
	if err != nil {
		return "", err
	}
	targetURL, err := postgresDatabaseURL(adminURL, targetPin.Database)
	if err != nil {
		return "", err
	}
	currentSchema, err := pgDumpSchema(ctx, currentURL)
	if err != nil {
		return "", fmt.Errorf("dump current branch schema: %w", err)
	}
	targetSchema, err := pgDumpSchema(ctx, targetURL)
	if err != nil {
		return "", fmt.Errorf("dump target branch schema: %w", err)
	}
	if currentSchema == targetSchema {
		return fmt.Sprintf("schemas are identical\ncurrent: %s\ntarget: %s\n", pin.Database, targetPin.Database), nil
	}
	diff, err := unifiedTextDiff(ctx, targetPin.Database, targetSchema, pin.Database, currentSchema)
	if err != nil {
		return "", err
	}
	return diff, nil
}

func (p postgresBranchProvider) adminURL(ctx context.Context) (string, error) {
	env := envpolicy.Environ()
	if adminURL, _ := lookupEnvValue(env, devPostgresAdminURLEnv); adminURL != "" {
		return adminURL, nil
	}
	agent, err := localagent.Ensure(ctx)
	if err != nil {
		return "", err
	}
	env, err = envWithManagedPostgresAdminURL(ctx, p.cfg, env, agent)
	if err != nil {
		return "", err
	}
	adminURL, _ := lookupEnvValue(env, devPostgresAdminURLEnv)
	if adminURL == "" {
		return "", fmt.Errorf("dev.services.postgres requires %s, a reusable agent Postgres substrate, or local initdb/postgres binaries", devPostgresAdminURLEnv)
	}
	return adminURL, nil
}

func (p postgresBranchProvider) ensureTemplateDatabaseBranch(ctx context.Context, adminURL string, pin worktreeDBPin) error {
	strategy := postgresBranchStrategy(p.cfg)
	if strategy != postgresDefaultBranchStrategy {
		return fmt.Errorf("dev.services.postgres branch_strategy %q is not implemented yet; use %q", strategy, postgresDefaultBranchStrategy)
	}
	exists, err := postgresDatabaseExists(ctx, adminURL, pin.Database)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return p.recreateBranchDatabase(ctx, adminURL, pin)
}

func (p postgresBranchProvider) recreateBranchDatabase(ctx context.Context, adminURL string, pin worktreeDBPin) error {
	parentDB := postgresParentDatabaseName(p.cfg, pin)
	if parentDB == "" {
		return fmt.Errorf("Postgres parent database is empty")
	}
	if pin.Database == parentDB {
		return fmt.Errorf("refusing to recreate protected parent database %q", parentDB)
	}
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("managed Postgres admin connection failed: %w", err)
	}
	if err := createPostgresDatabaseIfMissing(ctx, db, parentDB); err != nil {
		return fmt.Errorf("prepare parent template database %s: %w", parentDB, err)
	}
	branch := pq.QuoteIdentifier(pin.Database)
	parent := pq.QuoteIdentifier(parentDB)
	_, _ = db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, pin.Database)
	if _, err := db.ExecContext(ctx, "DROP DATABASE IF EXISTS "+branch); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, parentDB); err != nil {
		return err
	}
	templateClosed := false
	if _, err := db.ExecContext(ctx, "ALTER DATABASE "+parent+" WITH ALLOW_CONNECTIONS false"); err != nil {
		return err
	}
	templateClosed = true
	defer func() {
		if templateClosed {
			_, _ = db.ExecContext(context.Background(), "ALTER DATABASE "+parent+" WITH ALLOW_CONNECTIONS true")
		}
	}()
	if _, err := db.ExecContext(ctx, "CREATE DATABASE "+branch+" WITH TEMPLATE "+parent); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, "ALTER DATABASE "+parent+" WITH ALLOW_CONNECTIONS true"); err != nil {
		return err
	}
	templateClosed = false
	return nil
}

func postgresDatabaseExists(ctx context.Context, adminURL, database string) (bool, error) {
	db, err := sql.Open("postgres", adminURL)
	if err != nil {
		return false, err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, database).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func postgresBranchEndpoint(adminURL string, pin worktreeDBPin) (dbBranchEndpoint, error) {
	parsed, err := url.Parse(strings.TrimSpace(adminURL))
	if err != nil {
		return dbBranchEndpoint{}, err
	}
	host := parsed.Hostname()
	port := 5432
	if parsed.Port() != "" {
		parsedPort, err := strconv.Atoi(parsed.Port())
		if err != nil {
			return dbBranchEndpoint{}, err
		}
		port = parsedPort
	} else if socketHost := strings.TrimSpace(parsed.Query().Get("host")); socketHost != "" {
		host = socketHost
		if queryPort := strings.TrimSpace(parsed.Query().Get("port")); queryPort != "" {
			parsedPort, err := strconv.Atoi(queryPort)
			if err != nil {
				return dbBranchEndpoint{}, err
			}
			port = parsedPort
		}
	}
	if host == "" && parsed.Host != "" {
		host, _, _ = net.SplitHostPort(parsed.Host)
	}
	return dbBranchEndpoint{
		Host:     host,
		Port:     port,
		Database: pin.Database,
		Role:     pin.Role,
		SSLMode:  firstNonEmpty(parsed.Query().Get("sslmode"), "disable"),
		Source:   postgresBranchProviderName,
	}, nil
}

func postgresBranchStrategy(cfg appcfg.Config) string {
	_, svc, _ := managedPostgresDeclared(cfg)
	return firstNonEmpty(strings.TrimSpace(svc.BranchStrategy), postgresDefaultBranchStrategy)
}

func postgresParentDatabaseName(cfg appcfg.Config, pin worktreeDBPin) string {
	_, svc, _ := managedPostgresDeclared(cfg)
	if value := sanitizeDBIdentifier(svc.ParentDatabase); value != "" {
		return value
	}
	parent := normalizeDBBranchName(firstNonEmpty(pin.ParentBranch, svc.ParentBranch, dbBranchDefaultParentBranch))
	return managedPostgresBranchDatabaseName(pin.Project, parent)
}

func managedPostgresBranchDatabaseName(project, branch string) string {
	return managedPostgresDatabaseName(firstNonEmpty(project, "app"), strings.ReplaceAll(firstNonEmpty(branch, "branch"), "/", "_"))
}

func pgDumpSchema(ctx context.Context, databaseURL string) (string, error) {
	db, err := sql.Open("postgres", databaseURL)
	if err != nil {
		return "", err
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return "", err
	}
	var b strings.Builder
	if err := appendSchemaRelations(ctx, db, &b); err != nil {
		return "", err
	}
	if err := appendSchemaColumns(ctx, db, &b); err != nil {
		return "", err
	}
	if err := appendSchemaConstraints(ctx, db, &b); err != nil {
		return "", err
	}
	if err := appendSchemaIndexes(ctx, db, &b); err != nil {
		return "", err
	}
	return b.String(), nil
}

func appendSchemaRelations(ctx context.Context, db *sql.DB, b *strings.Builder) error {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, c.relkind::text, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
  AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
ORDER BY n.nspname, c.relname, c.relkind`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Fprintln(b, "[relations]")
	for rows.Next() {
		var schema, kind, name string
		if err := rows.Scan(&schema, &kind, &name); err != nil {
			return err
		}
		fmt.Fprintf(b, "%s.%s kind=%s\n", schema, name, kind)
	}
	return rows.Err()
}

func appendSchemaColumns(ctx context.Context, db *sql.DB, b *strings.Builder) error {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, c.relname, a.attnum, a.attname,
       pg_catalog.format_type(a.atttypid, a.atttypmod),
       a.attnotnull,
       COALESCE(pg_get_expr(ad.adbin, ad.adrelid), '')
FROM pg_attribute a
JOIN pg_class c ON c.oid = a.attrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
  AND c.relkind IN ('r', 'p', 'v', 'm', 'f')
  AND a.attnum > 0
  AND NOT a.attisdropped
ORDER BY n.nspname, c.relname, a.attnum`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Fprintln(b, "[columns]")
	for rows.Next() {
		var schema, table, name, typ, def string
		var num int
		var notNull bool
		if err := rows.Scan(&schema, &table, &num, &name, &typ, &notNull, &def); err != nil {
			return err
		}
		fmt.Fprintf(b, "%s.%s.%03d %s %s not_null=%t default=%s\n", schema, table, num, name, typ, notNull, def)
	}
	return rows.Err()
}

func appendSchemaConstraints(ctx context.Context, db *sql.DB, b *strings.Builder) error {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, c.relname, con.conname, con.contype::text, pg_get_constraintdef(con.oid)
FROM pg_constraint con
JOIN pg_class c ON c.oid = con.conrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND n.nspname NOT LIKE 'pg_toast%'
ORDER BY n.nspname, c.relname, con.conname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Fprintln(b, "[constraints]")
	for rows.Next() {
		var schema, table, name, typ, def string
		if err := rows.Scan(&schema, &table, &name, &typ, &def); err != nil {
			return err
		}
		fmt.Fprintf(b, "%s.%s %s type=%s %s\n", schema, table, name, typ, def)
	}
	return rows.Err()
}

func appendSchemaIndexes(ctx context.Context, db *sql.DB, b *strings.Builder) error {
	rows, err := db.QueryContext(ctx, `
SELECT schemaname, tablename, indexname, indexdef
FROM pg_indexes
WHERE schemaname NOT IN ('pg_catalog', 'information_schema')
  AND schemaname NOT LIKE 'pg_toast%'
ORDER BY schemaname, tablename, indexname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	fmt.Fprintln(b, "[indexes]")
	for rows.Next() {
		var schema, table, name, def string
		if err := rows.Scan(&schema, &table, &name, &def); err != nil {
			return err
		}
		fmt.Fprintf(b, "%s.%s %s %s\n", schema, table, name, def)
	}
	return rows.Err()
}

func unifiedTextDiff(ctx context.Context, oldName, oldText, newName, newText string) (string, error) {
	program, err := exec.LookPath("diff")
	if err != nil {
		return fmt.Sprintf("--- %s\n+++ %s\nschema differs, but diff was not found in PATH\n", oldName, newName), nil
	}
	dir, err := os.MkdirTemp("", "scenery-branch-diff-*")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	oldPath := filepath.Join(dir, sanitizeFileLabel(oldName)+".sql")
	newPath := filepath.Join(dir, sanitizeFileLabel(newName)+".sql")
	if err := os.WriteFile(oldPath, []byte(oldText), 0o600); err != nil {
		return "", err
	}
	if err := os.WriteFile(newPath, []byte(newText), 0o600); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, program, "-u", "--label", oldName, "--label", newName, oldPath, newPath)
	out, err := cmd.CombinedOutput()
	if err == nil || len(out) > 0 {
		return string(out), nil
	}
	return "", err
}

func sanitizeFileLabel(value string) string {
	label := localagentLabel(value)
	if label == "" {
		return "branch"
	}
	return label
}
