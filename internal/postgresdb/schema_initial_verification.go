package postgresdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
)

// The application owns the complete known-schema predicate. This explicit
// transition executes it under the same exclusion and transaction as its
// checksum-bearing baseline row and later pending migrations. It never infers
// completeness from table counts and never runs during ordinary startup.
func verifyInitialSchema(ctx context.Context, tx *sql.Tx, schema string, verification *SchemaVerification) error {
	if verification == nil || verification.Path == "" {
		return fmt.Errorf("initial schema verification is not configured")
	}
	sum := sha256.Sum256([]byte(verification.SQL))
	if verification.Checksum != "sha256:"+hex.EncodeToString(sum[:]) {
		return fmt.Errorf("initial schema verification does not match its source checksum")
	}
	if err := ValidateInitialVerificationSQL(verification.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true), set_config('standard_conforming_strings', 'on', true)`, quoteSchemaMigrationIdentifier(schema)+",scenery"); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, verification.SQL)
	if err != nil {
		return fmt.Errorf("initial schema verification %s failed (%s); nothing was adopted", verification.Path, schemaMigrationErrorCode(err))
	}
	defer func() { _ = rows.Close() }()
	invalidResult := func() error {
		return fmt.Errorf("initial schema verification %s must return exactly one row and one boolean column; nothing was adopted", verification.Path)
	}
	columns, err := rows.Columns()
	if err != nil || len(columns) != 1 || !rows.Next() {
		return invalidResult()
	}
	var value any
	if err := rows.Scan(&value); err != nil {
		return invalidResult()
	}
	matches, boolean := value.(bool)
	if !boolean || rows.Next() || rows.NextResultSet() || rows.Err() != nil {
		return invalidResult()
	}
	if err := rows.Close(); err != nil {
		return invalidResult()
	}
	if !matches {
		return fmt.Errorf("schema %s does not match the complete known initial schema in %s; nothing was adopted", schema, verification.Path)
	}
	return nil
}
