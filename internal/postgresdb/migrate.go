package postgresdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Migrate runs apply inside one transaction that holds a transaction-scoped
// advisory lock derived from lockKey. Concurrent openers from other processes
// therefore execute their migrations one at a time, and the lock releases
// with the transaction on both commit and rollback.
func Migrate(ctx context.Context, db *sql.DB, lockKey string, apply func(ctx context.Context, tx *sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres migrate %s: begin: %w", lockKey, err)
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return rollbackMigrate(tx, fmt.Errorf("postgres migrate %s: acquire advisory lock: %w", lockKey, err))
	}
	if err := apply(ctx, tx); err != nil {
		return rollbackMigrate(tx, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres migrate %s: commit: %w", lockKey, err)
	}
	return nil
}

// MigrateStatements is Migrate for plain idempotent DDL statement lists.
func MigrateStatements(ctx context.Context, db *sql.DB, lockKey string, statements []string) error {
	return Migrate(ctx, db, lockKey, func(ctx context.Context, tx *sql.Tx) error {
		for _, stmt := range statements {
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("postgres migrate %s: apply statement: %w", lockKey, err)
			}
		}
		return nil
	})
}

func rollbackMigrate(tx *sql.Tx, err error) error {
	if rbErr := tx.Rollback(); rbErr != nil {
		return errors.Join(err, rbErr)
	}
	return err
}
