package postgresdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	templatePrefix        = "scenery_tpl_"
	maxRetainedTemplates  = 4
	maxPostgresIdentifier = 63
)

// TemplateDatabaseName derives a deterministic PostgreSQL identifier (<= 63 bytes)
// for a migrated and seeded template database keyed by appID and setup fingerprint.
func TemplateDatabaseName(appID, fingerprint string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(appID) + "\n" + strings.TrimSpace(fingerprint)))
	return TemplateScopePrefix(appID) + hex.EncodeToString(sum[:8])
}

// TemplateScopePrefix returns the identifier prefix shared only by templates of appID.
// Including an exact hash of appID prevents prefix collisions across apps such as
// "foo" and "foo-bar" on a shared external PostgreSQL server.
func TemplateScopePrefix(appID string) string {
	appSlug := sanitizeTemplateSlug(appID)
	maxSlug := maxPostgresIdentifier - len(templatePrefix) - 1 - 8 - 1 - 16
	if len(appSlug) > maxSlug {
		appSlug = strings.TrimRight(appSlug[:maxSlug], "_")
		if appSlug == "" {
			appSlug = "app"
		}
	}
	appSum := sha256.Sum256([]byte("scenery.postgres.template.app\x00" + strings.TrimSpace(appID)))
	return templatePrefix + appSlug + "_" + hex.EncodeToString(appSum[:4]) + "_"
}

func sanitizeTemplateSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var b strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			b.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		return "app"
	}
	return out
}

// DatabaseURLWithName returns rawURL pointing at dbName with any search_path query removed.
func DatabaseURLWithName(rawURL, dbName string) (string, error) {
	dbName = strings.TrimSpace(dbName)
	if dbName == "" {
		return "", fmt.Errorf("postgres database name is required")
	}
	u, err := ParseURL(rawURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + dbName
	u.RawPath = ""
	query := u.Query()
	query.Del("search_path")
	u.RawQuery = query.Encode()
	return u.String(), nil
}

// EnsureTemplateDatabase materializes a sealed PostgreSQL template database
// (`IS_TEMPLATE = true ALLOW_CONNECTIONS = false`) if not already present.
// It returns templateHit == true when a sealed template with templateName already existed.
func EnsureTemplateDatabase(
	ctx context.Context,
	admin *sql.DB,
	adminServerURL string,
	appID string,
	templateName string,
	populate func(ctx context.Context, dbURL string) error,
) (bool, error) {
	templateName = strings.TrimSpace(templateName)
	if templateName == "" {
		return false, fmt.Errorf("postgres template database name is required")
	}
	templateURL, err := DatabaseURLWithName(adminServerURL, templateName)
	if err != nil {
		return false, err
	}

	conn, err := admin.Conn(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = conn.Close() }()

	lockKey := "scenery.template." + templateName
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return false, fmt.Errorf("acquire template database lock %s: %w", templateName, err)
	}
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtextextended($1, 0))`, lockKey)
	}()

	isTemplate, disallowsConn, exists, err := inspectTemplateState(ctx, conn, templateName)
	if err != nil {
		return false, err
	}
	if exists && isTemplate && disallowsConn {
		return true, nil
	}
	if exists {
		if err := dropUnsealedOrStaleTemplate(ctx, conn, templateName); err != nil {
			return false, err
		}
	}

	quoted := quoteIdent(templateName)
	if _, err := conn.ExecContext(ctx, `CREATE DATABASE `+quoted); err != nil {
		return false, fmt.Errorf("create staging template database %s: %w", templateName, err)
	}
	if populate != nil {
		if err := populate(ctx, templateURL); err != nil {
			_ = dropUnsealedOrStaleTemplate(context.WithoutCancel(ctx), conn, templateName)
			return false, err
		}
	}
	if _, err := conn.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, templateName); err != nil {
		_ = dropUnsealedOrStaleTemplate(context.WithoutCancel(ctx), conn, templateName)
		return false, fmt.Errorf("terminate template connections %s: %w", templateName, err)
	}
	if _, err := conn.ExecContext(ctx, `ALTER DATABASE `+quoted+` WITH IS_TEMPLATE = true ALLOW_CONNECTIONS = false`); err != nil {
		_ = dropUnsealedOrStaleTemplate(context.WithoutCancel(ctx), conn, templateName)
		return false, fmt.Errorf("seal template database %s: %w", templateName, err)
	}
	_, _ = conn.ExecContext(ctx, `CHECKPOINT`)
	_ = pruneOldTemplates(ctx, conn, TemplateScopePrefix(appID), templateName)
	return false, nil
}

// HasSealedTemplate reports whether templateName exists and is sealed as a template database.
func HasSealedTemplate(ctx context.Context, admin *sql.DB, templateName string) (bool, error) {
	var ok bool
	err := admin.QueryRowContext(
		ctx,
		`SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1 AND datistemplate AND NOT datallowconn)`,
		strings.TrimSpace(templateName),
	).Scan(&ok)
	return ok, err
}

// CloneDatabaseFromTemplate creates targetName using `CREATE DATABASE <target> TEMPLATE <tpl>`.
// When replaceExisting is false and targetName already exists, it leaves the existing
// database untouched and returns created == false.
func CloneDatabaseFromTemplate(
	ctx context.Context,
	admin *sql.DB,
	targetName string,
	templateName string,
	replaceExisting bool,
) (bool, time.Duration, error) {
	targetName = strings.TrimSpace(targetName)
	templateName = strings.TrimSpace(templateName)
	if targetName == "" || templateName == "" {
		return false, 0, fmt.Errorf("postgres target and template database names are required")
	}
	exists, err := databaseExists(ctx, admin, targetName)
	if err != nil {
		return false, 0, err
	}
	if exists {
		if !replaceExisting {
			return false, 0, nil
		}
		if err := DropDatabase(ctx, admin, targetName); err != nil {
			return false, 0, err
		}
	}
	conn, err := admin.Conn(ctx)
	if err != nil {
		return false, 0, err
	}
	defer func() { _ = conn.Close() }()
	_, _ = conn.ExecContext(ctx, `SET synchronous_commit = off`)
	defer func() {
		_, _ = conn.ExecContext(context.WithoutCancel(ctx), `SET synchronous_commit = DEFAULT`)
	}()

	started := time.Now()
	_, err = conn.ExecContext(ctx, `CREATE DATABASE `+quoteIdent(targetName)+` TEMPLATE `+quoteIdent(templateName)+` STRATEGY WAL_LOG`)
	if err != nil {
		if !replaceExisting && isDuplicateDatabase(err) {
			return false, 0, nil
		}
		return false, 0, fmt.Errorf("clone database %s from template %s: %w", targetName, templateName, err)
	}
	return true, time.Since(started), nil
}

func inspectTemplateState(ctx context.Context, conn *sql.Conn, name string) (isTemplate, disallowsConn, exists bool, err error) {
	var allowConn bool
	err = conn.QueryRowContext(ctx, `SELECT datistemplate, datallowconn FROM pg_database WHERE datname = $1`, name).Scan(&isTemplate, &allowConn)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, false, nil
	}
	if err != nil {
		return false, false, false, err
	}
	return isTemplate, !allowConn, true, nil
}

func dropUnsealedOrStaleTemplate(ctx context.Context, conn *sql.Conn, name string) error {
	quoted := quoteIdent(name)
	_, _ = conn.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
	_, _ = conn.ExecContext(ctx, `ALTER DATABASE `+quoted+` WITH IS_TEMPLATE = false ALLOW_CONNECTIONS = true`)
	_, err := conn.ExecContext(ctx, `DROP DATABASE IF EXISTS `+quoted)
	return err
}

func pruneOldTemplates(ctx context.Context, conn *sql.Conn, prefix, keepCurrent string) error {
	names, err := listSealedTemplateNames(ctx, conn, prefix)
	if err != nil {
		return err
	}
	if len(names) <= maxRetainedTemplates {
		return nil
	}
	kept := 0
	for _, name := range names {
		if name == keepCurrent {
			kept++
			continue
		}
		if kept < maxRetainedTemplates {
			kept++
			continue
		}
		_ = dropUnsealedOrStaleTemplate(ctx, conn, name)
	}
	return nil
}

func listSealedTemplateNames(ctx context.Context, conn *sql.Conn, prefix string) ([]string, error) {
	rows, err := conn.QueryContext(
		ctx,
		`SELECT datname FROM pg_database WHERE datistemplate AND NOT datallowconn AND starts_with(datname, $1) ORDER BY oid DESC`,
		prefix,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}
