package postgresdb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
)

type DatabaseInfo struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes,omitempty"`
}

func EnsureDatabase(ctx context.Context, db *sql.DB, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("postgres database name is required")
	}
	exists, err := databaseExists(ctx, db, name)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	_, err = db.ExecContext(ctx, `CREATE DATABASE `+quoteIdent(name))
	if isDuplicateDatabase(err) {
		exists, checkErr := databaseExists(ctx, db, name)
		if checkErr != nil {
			return checkErr
		}
		if exists {
			return nil
		}
	}
	return err
}

func DropDatabase(ctx context.Context, db *sql.DB, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("postgres database name is required")
	}
	if err := terminateBackends(ctx, db, name); err != nil {
		return err
	}
	_, err := db.ExecContext(ctx, `DROP DATABASE IF EXISTS `+quoteIdent(name))
	return err
}

func ResetDatabase(ctx context.Context, db *sql.DB, name string) error {
	if err := DropDatabase(ctx, db, name); err != nil {
		return err
	}
	return EnsureDatabase(ctx, db, name)
}

func ListSceneryDatabases(ctx context.Context, db *sql.DB) ([]DatabaseInfo, error) {
	rows, err := db.QueryContext(ctx, `
SELECT datname, pg_database_size(datname)
FROM pg_database
WHERE datistemplate = false AND datname <> 'postgres'
ORDER BY datname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DatabaseInfo
	for rows.Next() {
		var info DatabaseInfo
		if err := rows.Scan(&info.Name, &info.SizeBytes); err != nil {
			return nil, err
		}
		out = append(out, info)
	}
	return out, rows.Err()
}

func terminateBackends(ctx context.Context, db *sql.DB, name string) error {
	_, err := db.ExecContext(ctx, `SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1 AND pid <> pg_backend_pid()`, name)
	return err
}

func databaseExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var exists bool
	if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

func isDuplicateDatabase(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42P04"
}

func quoteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
