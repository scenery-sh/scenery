package postgresdb

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const DriverName = "pgx"

func Open(ctx context.Context, rawURL string) (*sql.DB, error) {
	if _, err := ParseURL(rawURL); err != nil {
		return nil, err
	}
	db, err := sql.Open(DriverName, strings.TrimSpace(rawURL))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
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
