package postgresdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"

	"scenery.sh/internal/appsdk"
)

const DriverName = "pgx"

func Open(ctx context.Context, rawURL string) (*sql.DB, error) {
	if _, err := ParseURL(rawURL); err != nil {
		return nil, err
	}
	config, err := tracedConnectionConfig(rawURL)
	if err != nil {
		return nil, err
	}
	db := stdlib.OpenDB(*config)
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func tracedConnectionConfig(rawURL string) (*pgx.ConnConfig, error) {
	config, err := pgx.ParseConfig(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	config.Tracer = queryTracer{}
	return config, nil
}

// pgx invokes these hooks for database/sql Exec, Query and prepared statements,
// including transaction queries. QueryEnd runs when rows close or are exhausted.
// The lightweight host bridge is a no-op outside a Scenery runtime.
type queryTracer struct{}

func (queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if host := appsdk.CurrentHost(); host != nil {
		return host.TraceDBQueryStart(ctx, data.SQL, len(data.Args))
	}
	return ctx
}

func (queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if host := appsdk.CurrentHost(); host != nil {
		rows := int64(-1)
		if data.CommandTag.String() != "" {
			rows = data.CommandTag.RowsAffected()
		}
		host.TraceDBQueryEnd(ctx, data.CommandTag.String(), rows, data.Err)
	}
}

func ParseURL(rawURL string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		return nil, fmt.Errorf("postgres URL must use postgres or postgresql scheme")
	}
	if u.Host == "" {
		return nil, fmt.Errorf("postgres URL must include a host")
	}
	if strings.Trim(u.Path, "/") == "" {
		return nil, fmt.Errorf("postgres URL must include a database name")
	}
	return u, nil
}

func RedactURL(rawURL string) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "<redacted>"
	}
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			u.User = url.UserPassword(name, "xxxxx")
		} else {
			u.User = url.UserPassword("xxxxx", "xxxxx")
		}
	}
	return u.String()
}
