package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"scenery.sh/internal/sqlitedb"
)

type dashboardSQLiteRequest struct {
	AppID    string `json:"app_id"`
	Database string `json:"database"`
	Table    string `json:"table"`
}

type dashboardSQLiteRowsRequest struct {
	AppID    string `json:"app_id"`
	Database string `json:"database"`
	Table    string `json:"table"`
	Limit    int    `json:"limit"`
	Offset   int    `json:"offset"`
}

type dashboardSQLiteTable struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

type dashboardSQLiteColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	NotNull    bool   `json:"not_null"`
	PrimaryKey bool   `json:"primary_key"`
}

type dashboardSQLiteRows struct {
	Columns []string `json:"columns"`
	Rows    [][]any  `json:"rows"`
	Limit   int      `json:"limit"`
	Offset  int      `json:"offset"`
}

func (s *dashboardServer) sqliteTables(ctx context.Context, req dashboardSQLiteRequest) ([]dashboardSQLiteTable, error) {
	db, err := s.openDashboardSQLiteReadOnly(ctx, req.AppID, req.Database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, `SELECT name, type FROM sqlite_schema WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dashboardSQLiteTable
	for rows.Next() {
		var item dashboardSQLiteTable
		if err := rows.Scan(&item.Name, &item.Type); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (s *dashboardServer) sqliteSchema(ctx context.Context, req dashboardSQLiteRequest) ([]dashboardSQLiteColumn, error) {
	if strings.TrimSpace(req.Table) == "" {
		return nil, fmt.Errorf("sqlite table is required")
	}
	db, err := s.openDashboardSQLiteReadOnly(ctx, req.AppID, req.Database)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+quoteSQLiteIdent(req.Table)+")")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []dashboardSQLiteColumn
	for rows.Next() {
		var cid int
		var col dashboardSQLiteColumn
		var notNull, pk int
		var defaultValue any
		if err := rows.Scan(&cid, &col.Name, &col.Type, &notNull, &defaultValue, &pk); err != nil {
			return nil, err
		}
		col.NotNull = notNull != 0
		col.PrimaryKey = pk != 0
		out = append(out, col)
	}
	return out, rows.Err()
}

func (s *dashboardServer) sqliteRows(ctx context.Context, req dashboardSQLiteRowsRequest) (dashboardSQLiteRows, error) {
	if strings.TrimSpace(req.Table) == "" {
		return dashboardSQLiteRows{}, fmt.Errorf("sqlite table is required")
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
	db, err := s.openDashboardSQLiteReadOnly(ctx, req.AppID, req.Database)
	if err != nil {
		return dashboardSQLiteRows{}, err
	}
	defer db.Close()
	query := "SELECT * FROM " + quoteSQLiteIdent(req.Table) + " LIMIT ? OFFSET ?"
	rows, err := db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return dashboardSQLiteRows{}, err
	}
	defer rows.Close()
	cols, err := rows.Columns()
	if err != nil {
		return dashboardSQLiteRows{}, err
	}
	var out [][]any
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return dashboardSQLiteRows{}, err
		}
		for i, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[i] = string(bytes)
			}
		}
		out = append(out, values)
	}
	return dashboardSQLiteRows{Columns: cols, Rows: out, Limit: limit, Offset: offset}, rows.Err()
}

func (s *dashboardServer) openDashboardSQLiteReadOnly(ctx context.Context, appID, database string) (*sql.DB, error) {
	path, err := s.dashboardSQLitePath(ctx, appID, database)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(path); err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro"}
	return sql.Open(sqlitedb.DriverName, u.String())
}

func (s *dashboardServer) dashboardSQLitePath(ctx context.Context, appID, database string) (string, error) {
	status, err := s.dashboardStatusFor(ctx, firstNonEmpty(appID, s.dashboardActiveAppID()))
	if err != nil {
		return "", err
	}
	var meta struct {
		Databases []dashboardSQLiteDatabase `json:"sql_databases"`
	}
	if len(status.Meta) == 0 {
		return "", fmt.Errorf("no sqlite databases discovered")
	}
	if err := json.Unmarshal(status.Meta, &meta); err != nil {
		return "", err
	}
	database = strings.TrimSpace(database)
	for _, db := range meta.Databases {
		if database == "" || database == db.Name || database == db.FileLabel || database == db.Path {
			if db.Path == "" {
				return "", fmt.Errorf("sqlite database %q has no path", db.Name)
			}
			return db.Path, nil
		}
	}
	return "", fmt.Errorf("sqlite database %q is not available", database)
}

func quoteSQLiteIdent(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}
