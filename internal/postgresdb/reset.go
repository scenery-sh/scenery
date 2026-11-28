package postgresdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

var (
	ErrDatabaseNotResettable = errors.New("database template is not fast-resettable")
	ErrSchemaChanged         = errors.New("database schema changed during test")
)

// ResettableDatabaseState captures the schema fingerprint and truncatable user
// tables of a freshly cloned template database so pooled test databases can be
// fast-reset between modules when the template has no seeded user rows.
type ResettableDatabaseState struct {
	Resettable          bool
	SchemaFingerprint   string
	Tables              []string
	StandaloneSequences []string
}

// InspectResettableDatabase inspects a freshly cloned database (before any test
// runs) and reports whether TRUNCATE ... RESTART IDENTITY CASCADE can faithfully
// restore it to its freshly cloned state.
func InspectResettableDatabase(ctx context.Context, db *sql.DB) (*ResettableDatabaseState, error) {
	fingerprint, hasMatViews, err := databaseSchemaFingerprint(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("inspect database schema fingerprint: %w", err)
	}
	if hasMatViews {
		return &ResettableDatabaseState{
			Resettable:        false,
			SchemaFingerprint: fingerprint,
		}, nil
	}
	tables, err := listUserTables(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("list user tables: %w", err)
	}
	hasRows, err := hasAnyRows(ctx, db, tables)
	if err != nil {
		return nil, fmt.Errorf("inspect template seed rows: %w", err)
	}
	if hasRows {
		return &ResettableDatabaseState{
			Resettable:        false,
			SchemaFingerprint: fingerprint,
		}, nil
	}
	sequences, err := listStandaloneSequences(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("list standalone sequences: %w", err)
	}
	return &ResettableDatabaseState{
		Resettable:          true,
		SchemaFingerprint:   fingerprint,
		Tables:              tables,
		StandaloneSequences: sequences,
	}, nil
}

// FastResetDatabase verifies the database schema still matches expected and
// truncates all non-migration user tables (plus restarts standalone sequences)
// inside a single transaction. Returns ErrSchemaChanged if DDL mutated the schema.
func FastResetDatabase(ctx context.Context, db *sql.DB, expected *ResettableDatabaseState) error {
	if expected == nil || !expected.Resettable {
		return ErrDatabaseNotResettable
	}
	fingerprint, _, err := databaseSchemaFingerprint(ctx, db)
	if err != nil {
		return fmt.Errorf("inspect database schema before reset: %w", err)
	}
	if fingerprint != expected.SchemaFingerprint {
		return ErrSchemaChanged
	}
	if len(expected.Tables) == 0 && len(expected.StandaloneSequences) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fast reset transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SET LOCAL synchronous_commit = off`); err != nil {
		return fmt.Errorf("configure fast reset commit mode: %w", err)
	}
	if len(expected.Tables) > 0 {
		if _, err := tx.ExecContext(ctx, `TRUNCATE TABLE `+strings.Join(expected.Tables, ", ")+` RESTART IDENTITY CASCADE`); err != nil {
			return fmt.Errorf("truncate test tables: %w", err)
		}
	}
	for _, seq := range expected.StandaloneSequences {
		if _, err := tx.ExecContext(ctx, `ALTER SEQUENCE `+seq+` RESTART`); err != nil {
			return fmt.Errorf("restart standalone sequence %s: %w", seq, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit fast reset: %w", err)
	}
	return nil
}

func listUserTables(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p')
  AND NOT c.relispartition
  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
  AND n.nspname NOT LIKE 'pg_temp_%'
  AND n.nspname NOT LIKE 'pg_toast_temp_%'
  AND c.relname <> $1
ORDER BY n.nspname, c.relname`, schemaMigrationLedger)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return nil, err
		}
		tables = append(tables, quoteIdent(schema)+"."+quoteIdent(table))
	}
	return tables, rows.Err()
}

func listStandaloneSequences(ctx context.Context, db *sql.DB) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname, c.relname
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind = 'S'
  AND n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
  AND n.nspname NOT LIKE 'pg_temp_%'
  AND n.nspname NOT LIKE 'pg_toast_temp_%'
  AND NOT EXISTS (
      SELECT 1 FROM pg_depend d
      WHERE d.objid = c.oid
        AND d.classid = 'pg_class'::regclass
        AND d.deptype IN ('a', 'i')
  )
ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var seqs []string
	for rows.Next() {
		var schema, name string
		if err := rows.Scan(&schema, &name); err != nil {
			return nil, err
		}
		seqs = append(seqs, quoteIdent(schema)+"."+quoteIdent(name))
	}
	return seqs, rows.Err()
}

func databaseSchemaFingerprint(ctx context.Context, db *sql.DB) (string, bool, error) {
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname,
       COALESCE(c.relkind::text, ''),
       COALESCE(c.relname, ''),
       COALESCE(a.attname, ''),
       COALESCE(pg_catalog.format_type(a.atttypid, a.atttypmod), ''),
       COALESCE(a.attnotnull, false)
FROM pg_namespace n
LEFT JOIN pg_class c
  ON c.relnamespace = n.oid
 AND c.relkind IN ('r', 'p', 'v', 'm', 'S', 'f')
LEFT JOIN pg_attribute a
  ON a.attrelid = c.oid
 AND a.attnum > 0
 AND NOT a.attisdropped
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema', 'pg_toast')
  AND n.nspname NOT LIKE 'pg_temp_%'
  AND n.nspname NOT LIKE 'pg_toast_temp_%'
ORDER BY n.nspname, COALESCE(c.relkind::text, ''), COALESCE(c.relname, ''), COALESCE(a.attnum, 0)`)
	if err != nil {
		return "", false, err
	}
	defer func() { _ = rows.Close() }()
	h := sha256.New()
	hasMatViews := false
	for rows.Next() {
		var schema, relkind, relname, attname, typ string
		var notNull bool
		if err := rows.Scan(&schema, &relkind, &relname, &attname, &typ, &notNull); err != nil {
			return "", false, err
		}
		if relkind == "m" {
			hasMatViews = true
		}
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%s\x00%s\x00%t\n", schema, relkind, relname, attname, typ, notNull)
	}
	if err := rows.Err(); err != nil {
		return "", false, err
	}
	return hex.EncodeToString(h.Sum(nil)), hasMatViews, nil
}

func hasAnyRows(ctx context.Context, db *sql.DB, quotedTables []string) (bool, error) {
	const batchSize = 64
	for start := 0; start < len(quotedTables); start += batchSize {
		end := min(start+batchSize, len(quotedTables))
		var hasRows bool
		if err := db.QueryRowContext(ctx, buildHasRowsQuery(quotedTables[start:end])).Scan(&hasRows); err != nil {
			return false, err
		}
		if hasRows {
			return true, nil
		}
	}
	return false, nil
}

func buildHasRowsQuery(quotedTables []string) string {
	var b strings.Builder
	b.WriteString("SELECT ")
	for i, tbl := range quotedTables {
		if i > 0 {
			b.WriteString(" OR ")
		}
		b.WriteString("EXISTS (SELECT 1 FROM ")
		b.WriteString(tbl)
		b.WriteString(" LIMIT 1)")
	}
	return b.String()
}
