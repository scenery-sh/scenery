package main

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"scenery.sh/internal/envpolicy"
)

type dashboardPostgresRequest struct {
	AppID    string `json:"app_id"`
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Table    string `json:"table"`
}

type dashboardPostgresRowsRequest struct {
	AppID    string `json:"app_id"`
	Database string `json:"database"`
	Schema   string `json:"schema"`
	Table    string `json:"table"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

type dashboardPostgresTable struct {
	Schema   string `json:"schema"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	RowCount int64  `json:"row_count,omitempty"`
}

type dashboardPostgresColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	PrimaryKey bool   `json:"primary_key"`
}

type dashboardPostgresRows struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

func (s *dashboardServer) postgresTables(ctx context.Context, req dashboardPostgresRequest) ([]dashboardPostgresTable, error) {
	db, err := s.openDashboardPostgres(ctx, req.AppID)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
SELECT n.nspname,
       c.relname,
       CASE c.relkind WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized_view' ELSE 'table' END,
       GREATEST(c.reltuples::bigint, 0)
FROM pg_class c
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE c.relkind IN ('r', 'p', 'v', 'm')
  AND (n.nspname = 'scenery' OR n.nspname NOT LIKE 'pg_%' AND n.nspname <> 'information_schema' AND n.nspname <> 'public')
ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dashboardPostgresTable
	for rows.Next() {
		var item dashboardPostgresTable
		if err := rows.Scan(&item.Schema, &item.Name, &item.Type, &item.RowCount); err != nil {
			return nil, err
		}
		item.Name = item.Schema + "." + item.Name
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *dashboardServer) postgresSchema(ctx context.Context, req dashboardPostgresRequest) ([]dashboardPostgresColumn, error) {
	schema, table, err := dashboardPostgresTarget(req)
	if err != nil {
		return nil, err
	}
	db, err := s.openDashboardPostgres(ctx, req.AppID)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `
SELECT c.column_name,
       c.data_type,
       c.is_nullable = 'NO',
       EXISTS (
         SELECT 1
         FROM information_schema.table_constraints tc
         JOIN information_schema.key_column_usage kcu
           ON kcu.constraint_name = tc.constraint_name
          AND kcu.table_schema = tc.table_schema
          AND kcu.table_name = tc.table_name
         WHERE tc.constraint_type = 'PRIMARY KEY'
           AND tc.table_schema = c.table_schema
           AND tc.table_name = c.table_name
           AND kcu.column_name = c.column_name
       )
FROM information_schema.columns c
WHERE c.table_schema = $1 AND c.table_name = $2
ORDER BY c.ordinal_position`, schema, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dashboardPostgresColumn
	for rows.Next() {
		var col dashboardPostgresColumn
		if err := rows.Scan(&col.Name, &col.Type, &col.NotNull, &col.PrimaryKey); err != nil {
			return nil, err
		}
		out = append(out, col)
	}
	return out, rows.Err()
}

func (s *dashboardServer) postgresRows(ctx context.Context, req dashboardPostgresRowsRequest) (dashboardPostgresRows, error) {
	schema, table, err := dashboardPostgresTarget(dashboardPostgresRequest{Schema: req.Schema, Table: req.Table})
	if err != nil {
		return dashboardPostgresRows{}, err
	}
	limit := req.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	offset := req.Offset
	if offset < 0 {
		offset = 0
	}
	db, err := s.openDashboardPostgres(ctx, req.AppID)
	if err != nil {
		return dashboardPostgresRows{}, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT * FROM `+quotePostgresIdent(schema)+`.`+quotePostgresIdent(table)+` LIMIT $1 OFFSET $2`, limit, offset)
	if err != nil {
		return dashboardPostgresRows{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return dashboardPostgresRows{}, err
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return dashboardPostgresRows{}, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		out = append(out, values)
	}
	return dashboardPostgresRows{Columns: cols, Rows: out, Limit: limit, Offset: offset}, rows.Err()
}

func (s *dashboardServer) openDashboardPostgres(ctx context.Context, appID string) (*sql.DB, error) {
	status, err := s.dashboardStatusFor(ctx, firstNonEmpty(appID, s.dashboardActiveAppID()))
	if err != nil {
		return nil, err
	}
	root := strings.TrimSpace(status.AppRoot)
	if root == "" {
		return nil, fmt.Errorf("dashboard postgres explorer requires an app root")
	}
	appRoot, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		return nil, err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot)
	if err != nil {
		return nil, err
	}
	_, database, err := managedDatabaseEnv(ctx, appRoot, cfg, nil, baseEnv)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(database.URL) == "" {
		return nil, fmt.Errorf("no postgres database discovered")
	}
	return openPostgresDatabase(ctx, database.URL)
}

func dashboardPostgresTarget(req dashboardPostgresRequest) (string, string, error) {
	table := strings.TrimSpace(req.Table)
	schema := strings.TrimSpace(req.Schema)
	if schema == "" && strings.Contains(table, ".") {
		schema, table, _ = strings.Cut(table, ".")
	}
	if schema == "" {
		schema = "scenery"
	}
	if table == "" {
		return "", "", fmt.Errorf("postgres table is required")
	}
	return schema, table, nil
}

func quotePostgresIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
