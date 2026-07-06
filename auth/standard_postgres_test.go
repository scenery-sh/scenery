package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/postgresdb"
)

func TestStandardAuthBootstrapPostgresSchema(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)

	authURL, err := postgresdb.ServiceURL(databaseURL, "scenery")
	if err != nil {
		t.Fatalf("derive auth URL: %v", err)
	}
	db, err := postgresdb.Open(ctx, authURL)
	if err != nil {
		t.Fatalf("open auth database: %v", err)
	}
	defer db.Close()
	if err := bootstrapStandardAuthSchema(ctx, db); err != nil {
		t.Fatalf("bootstrap standard auth schema: %v", err)
	}
	var schema sql.NullString
	if err := db.QueryRowContext(ctx, `select n.nspname from pg_class c join pg_namespace n on n.oid = c.relnamespace where c.relname = 'scenery_auth_users'`).Scan(&schema); err != nil {
		t.Fatalf("query auth table schema: %v", err)
	}
	if !schema.Valid || schema.String != "scenery" {
		t.Fatalf("auth users table schema = %q, valid=%v", schema.String, schema.Valid)
	}
}

func createAuthLiveTestDatabase(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()

	raw := strings.TrimSpace(os.Getenv("SCENERY_TEST_DATABASE_URL"))
	if raw == "" {
		t.Skip("SCENERY_TEST_DATABASE_URL is not set; skipping live Postgres auth test")
	}
	base, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse SCENERY_TEST_DATABASE_URL: %v", err)
	}
	adminURL := authAdminDatabaseURL(*base)
	admin, err := postgresdb.Open(ctx, adminURL)
	if err != nil {
		t.Skipf("SCENERY_TEST_DATABASE_URL is not reachable for live Postgres tests: %v", err)
	}
	name := fmt.Sprintf("scenery_auth_test_%d", time.Now().UnixNano())
	if err := postgresdb.EnsureDatabase(ctx, admin, name); err != nil {
		_ = admin.Close()
		t.Skipf("SCENERY_TEST_DATABASE_URL cannot create per-test database: %v", err)
	}
	testURL := *base
	testURL.Path = "/" + name
	cleanup := func() {
		_, _ = admin.ExecContext(ctx, `select pg_terminate_backend(pid) from pg_stat_activity where datname = $1`, name)
		_ = postgresdb.DropDatabase(ctx, admin, name)
		_ = admin.Close()
	}
	return testURL.String(), cleanup
}

func authAdminDatabaseURL(u url.URL) string {
	u.Path = "/postgres"
	u.RawQuery = ""
	return u.String()
}
