package auth

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	authdb "scenery.sh/auth/db/gen"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/runtime"
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

func TestDevBootstrapDefaultEmailCreatesUserTenantAndMembership(t *testing.T) {
	ctx := context.Background()
	databaseURL, cleanup := createAuthLiveTestDatabase(t, ctx)
	t.Cleanup(cleanup)
	resetStandardAuthStateForTest(t)
	t.Setenv("DatabaseURL", databaseURL)
	runtime.SetAppConfig(runtime.AppConfig{Name: "auth-dev-bootstrap-test", ListenAddr: "127.0.0.1:0"})

	cfg := normalizeStandardConfig(StandardConfig{
		Enabled: true,
		DevBootstrap: DevBootstrapConfig{
			Enabled:          true,
			DefaultUserEmail: "Owner@Example.test",
			DefaultTenantID:  "d0540000-0000-0000-0000-000000000001",
		},
		AutoBootstrapDatabase: true,
	})
	applyStandardSecrets(cfg)
	standardAuthState.mu.Lock()
	standardAuthState.cfg = cfg
	standardAuthState.mu.Unlock()

	first, err := DevBootstrap(ctx, nil)
	if err != nil {
		t.Fatalf("first DevBootstrap: %v", err)
	}
	firstAuth, err := ValidateToken(first.Token)
	if err != nil {
		t.Fatalf("first token: %v", err)
	}
	if firstAuth.TenantID != TenantID(cfg.DevBootstrap.DefaultTenantID) {
		t.Fatalf("first token tenant = %q, want %q", firstAuth.TenantID, cfg.DevBootstrap.DefaultTenantID)
	}

	second, err := DevBootstrap(ctx, nil)
	if err != nil {
		t.Fatalf("second DevBootstrap: %v", err)
	}
	secondAuth, err := ValidateToken(second.Token)
	if err != nil {
		t.Fatalf("second token: %v", err)
	}
	if secondAuth.UserID != firstAuth.UserID || secondAuth.TenantID != firstAuth.TenantID {
		t.Fatalf("second token auth = %+v, want same user/tenant as %+v", secondAuth, firstAuth)
	}

	authURL, err := postgresdb.ServiceURL(databaseURL, "scenery")
	if err != nil {
		t.Fatalf("derive auth URL: %v", err)
	}
	db, err := postgresdb.Open(ctx, authURL)
	if err != nil {
		t.Fatalf("open auth database: %v", err)
	}
	defer db.Close()
	q := authdb.New(db)
	user, err := q.GetUserByNormalizedEmail(ctx, "owner@example.test")
	if err != nil {
		t.Fatalf("get created user: %v", err)
	}
	if !user.EmailVerifiedAt.Valid {
		t.Fatal("created default user email is not verified")
	}
	tenantID, err := parseUUID(cfg.DevBootstrap.DefaultTenantID)
	if err != nil {
		t.Fatal(err)
	}
	memberships, err := q.ListUserMemberships(ctx, user.ID)
	if err != nil {
		t.Fatalf("list memberships: %v", err)
	}
	if len(memberships) != 1 || uuidString(memberships[0].TenantID) != uuidString(tenantID) || memberships[0].Role != roleOwner {
		t.Fatalf("memberships = %+v, want one owner membership in default tenant", memberships)
	}
	var userCount, tenantCount, membershipCount int
	if err := db.QueryRowContext(ctx, `select count(*) from scenery.scenery_auth_users where normalized_primary_email = $1`, "owner@example.test").Scan(&userCount); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if err := db.QueryRowContext(ctx, `select count(*) from scenery.scenery_auth_tenants where id = $1`, tenantID).Scan(&tenantCount); err != nil {
		t.Fatalf("count tenants: %v", err)
	}
	if err := db.QueryRowContext(ctx, `select count(*) from scenery.scenery_auth_organization_memberships where user_id = $1 and tenant_id = $2 and disabled_at is null`, user.ID, tenantID).Scan(&membershipCount); err != nil {
		t.Fatalf("count memberships: %v", err)
	}
	if userCount != 1 || tenantCount != 1 || membershipCount != 1 {
		t.Fatalf("counts user=%d tenant=%d membership=%d, want all 1", userCount, tenantCount, membershipCount)
	}
}

func resetStandardAuthStateForTest(t *testing.T) {
	t.Helper()
	reset := func() {
		standardAuthState.mu.Lock()
		if standardAuthState.svc != nil && standardAuthState.svc.db != nil {
			_ = standardAuthState.svc.db.Close()
		}
		standardAuthState.cfg = StandardConfig{}
		standardAuthState.svc = nil
		standardAuthState.once = sync.Once{}
		standardAuthState.err = nil
		standardAuthState.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
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
