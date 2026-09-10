package postgresdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"

	"scenery.sh/internal/postgresname"
)

const schemaMigrationLedger = "_scenery_migrations"

var errUnversionedSchema = errors.New("populated schema has no migration ledger")

type SchemaVerification struct {
	Path     string `json:"path"`
	Checksum string `json:"checksum"`
	SQL      string `json:"-"`
}

type SchemaMigrationOptions struct {
	StatusOnly          bool
	AdoptInitial        bool
	InitialVerification *SchemaVerification
}

type SchemaMigration struct {
	Revision     int                 `json:"revision"`
	Path         string              `json:"path"`
	Checksum     string              `json:"checksum"`
	SQL          string              `json:"-"`
	Verification *SchemaVerification `json:"verification,omitempty"`
}

type SchemaMigrationRecord struct {
	SchemaMigration
	Status string `json:"status"`
}

type SchemaMigrationResult struct {
	Service    string                  `json:"service"`
	Schema     string                  `json:"schema"`
	Status     string                  `json:"status"`
	Migrations []SchemaMigrationRecord `json:"migrations"`
	Error      string                  `json:"error,omitempty"`
}

// SchemaMigrations serializes one service's complete pending chain under the
// existing PostgreSQL migration lock. DDL and successful ledger rows commit
// together; cancellation, errors, and process death cannot record completion.
// Status is read-only and never creates a schema or ledger.
func SchemaMigrations(ctx context.Context, db *sql.DB, appID, service, schema string, migrations []SchemaMigration, opts SchemaMigrationOptions) (SchemaMigrationResult, error) {
	result := SchemaMigrationResult{Service: service, Schema: schema, Status: "pending", Migrations: []SchemaMigrationRecord{}}
	if normalized, err := postgresname.SchemaNameFor(schema); err != nil || normalized != schema || appID == "" || service == "" {
		return result, fmt.Errorf("migration target must be a named application SQL binding")
	}
	if err := validateSchemaMigrations(migrations); err != nil {
		return result, err
	}
	if opts.AdoptInitial && (opts.StatusOnly || opts.InitialVerification == nil) {
		return result, fmt.Errorf("initial adoption requires an app-authored initial_verification query and cannot be combined with --status")
	}
	for _, migration := range migrations {
		result.Migrations = append(result.Migrations, SchemaMigrationRecord{SchemaMigration: migration, Status: "pending"})
	}
	apply := func(ctx context.Context, tx *sql.Tx) error {
		applied, err := readSchemaMigrations(ctx, tx, appID, schema)
		adopting := opts.AdoptInitial && errors.Is(err, errUnversionedSchema)
		if adopting {
			err = verifyInitialSchema(ctx, tx, schema, opts.InitialVerification)
		}
		if err != nil {
			return err
		}
		if err := matchSchemaMigrations(migrations, applied); err != nil {
			return err
		}
		for index := range applied {
			result.Migrations[index].Status = "applied"
			result.Migrations[index].Verification = applied[index].Verification
		}
		if len(applied) == len(migrations) {
			result.Status = "current"
			return nil
		}
		if opts.StatusOnly {
			return nil
		}
		quoted := quoteSchemaMigrationIdentifier(schema)
		ledger := quoted + "." + quoteSchemaMigrationIdentifier(schemaMigrationLedger)
		if _, err := tx.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS `+quoted+`; CREATE TABLE IF NOT EXISTS `+ledger+` (
  revision integer PRIMARY KEY CHECK (revision > 0),
  app_id text NOT NULL,
  format_revision integer NOT NULL CHECK (format_revision = 1),
  path text NOT NULL,
  checksum text NOT NULL,
  verification_path text NOT NULL DEFAULT '',
  verification_checksum text NOT NULL DEFAULT '',
  applied_at timestamptz NOT NULL DEFAULT now()
)`); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true), set_config('standard_conforming_strings', 'on', true)`, quoted+",scenery"); err != nil {
			return err
		}
		for _, migration := range migrations[len(applied):] {
			verificationPath, verificationChecksum := "", ""
			if adopting && migration.Revision == 1 {
				verificationPath, verificationChecksum = opts.InitialVerification.Path, opts.InitialVerification.Checksum
				result.Migrations[0].Verification = opts.InitialVerification
			} else {
				if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
					return fmt.Errorf("migration %s failed (%s); the pending chain was rolled back", migration.Path, schemaMigrationErrorCode(err))
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+ledger+` (revision, app_id, format_revision, path, checksum, verification_path, verification_checksum) VALUES ($1, $2, 1, $3, $4, $5, $6)`, migration.Revision, appID, migration.Path, migration.Checksum, verificationPath, verificationChecksum); err != nil {
				return err
			}
		}
		return nil
	}
	lockKey := "scenery.application-schema/" + schema
	var err error
	if opts.StatusOnly {
		tx, beginErr := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
		if beginErr != nil {
			err = beginErr
		} else {
			if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock_shared(hashtextextended($1, 0))`, lockKey); err == nil {
				err = apply(ctx, tx)
			}
			err = errors.Join(err, tx.Rollback())
		}
	} else {
		err = Migrate(ctx, db, lockKey, apply)
	}
	if err != nil {
		for index := range result.Migrations {
			if result.Migrations[index].Status != "applied" {
				result.Migrations[index].Verification = nil
			}
		}
		result.Status, result.Error = "blocked", err.Error()
		return result, err
	}
	if !opts.StatusOnly {
		result.Status = "current"
		for index := range result.Migrations {
			result.Migrations[index].Status = "applied"
		}
	}
	return result, nil
}

func validateSchemaMigrations(migrations []SchemaMigration) error {
	if len(migrations) == 0 || len(migrations) > 256 {
		return fmt.Errorf("a schema requires 1 to 256 ordered migration files")
	}
	for index, migration := range migrations {
		if migration.Revision != index+1 || migration.Path == "" {
			return fmt.Errorf("migration revisions must be contiguous, starting at 0001")
		}
		sum := sha256.Sum256([]byte(migration.SQL))
		if migration.Checksum != "sha256:"+hex.EncodeToString(sum[:]) {
			return fmt.Errorf("migration %s does not match its source checksum", migration.Path)
		}
		if err := ValidateMigrationSQL(migration.SQL); err != nil {
			return fmt.Errorf("migration %s: %w", migration.Path, err)
		}
	}
	return nil
}

func readSchemaMigrations(ctx context.Context, tx *sql.Tx, appID, schema string) ([]SchemaMigration, error) {
	var ledger bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1 AND c.relname=$2 AND c.relkind='r')`, schema, schemaMigrationLedger).Scan(&ledger); err != nil {
		return nil, err
	}
	if !ledger {
		var populated bool
		if err := tx.QueryRowContext(ctx, `SELECT
  EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname=$1)
  OR EXISTS (SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname=$1)
  OR EXISTS (SELECT 1 FROM pg_type t JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname=$1 AND t.typtype IN ('e','d'))`, schema).Scan(&populated); err != nil {
			return nil, err
		}
		if populated {
			return nil, fmt.Errorf("schema %s is populated without a migration ledger; initialization is unknown or incomplete, so no SQL was applied: %w", schema, errUnversionedSchema)
		}
		return nil, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT revision, path, checksum, app_id, format_revision, verification_path, verification_checksum FROM `+quoteSchemaMigrationIdentifier(schema)+`.`+quoteSchemaMigrationIdentifier(schemaMigrationLedger)+` ORDER BY revision`)
	if err != nil {
		return nil, fmt.Errorf("schema %s migration ledger is not the current format", schema)
	}
	defer func() { _ = rows.Close() }()
	var applied []SchemaMigration
	for rows.Next() {
		var migration SchemaMigration
		var owner string
		var format int
		var verification SchemaVerification
		if err := rows.Scan(&migration.Revision, &migration.Path, &migration.Checksum, &owner, &format, &verification.Path, &verification.Checksum); err != nil {
			return nil, err
		}
		if owner != appID || format != 1 || len(applied) >= 256 {
			return nil, fmt.Errorf("schema %s migration ledger owner, format, or bound does not match", schema)
		}
		if verification.Path != "" || verification.Checksum != "" {
			if migration.Revision != 1 || verification.Path == "" || len(verification.Checksum) != 71 || !strings.HasPrefix(verification.Checksum, "sha256:") {
				return nil, fmt.Errorf("schema %s has invalid initial-verification evidence", schema)
			}
			migration.Verification = &verification
		}
		applied = append(applied, migration)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(applied) == 0 {
		return nil, fmt.Errorf("schema %s has an empty migration ledger; initialization is unknown or incomplete", schema)
	}
	return applied, nil
}

func matchSchemaMigrations(planned, applied []SchemaMigration) error {
	if len(applied) > len(planned) {
		return fmt.Errorf("database migration history is ahead of this source; use its matching application revision")
	}
	for index, prior := range applied {
		current := planned[index]
		if prior.Revision != current.Revision || prior.Path != current.Path || prior.Checksum != current.Checksum {
			return fmt.Errorf("applied migration %04d changed or was removed; restore its original bytes and append a new migration", prior.Revision)
		}
	}
	return nil
}

func quoteSchemaMigrationIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func schemaMigrationErrorCode(err error) string {
	var postgres *pgconn.PgError
	if errors.As(err, &postgres) {
		return "SQLSTATE " + postgres.Code
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return "execution interrupted"
	}
	return "database execution error"
}
