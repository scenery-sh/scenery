// Package datasource defines stable capability interfaces injected into
// generated service constructors.
package datasource

import (
	"context"
	"database/sql"
)

// SQL is the query and transaction capability exposed by a SQL data source.
// Implementations receive this interface rather than discovering a concrete
// database provider or process-global connection.
type SQL interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}
