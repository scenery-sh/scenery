package db

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/pgxpool"
)

func TestGetUsesDefaultDatabaseURLEnv(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv(defaultDatabaseURLEnv, testDSN("defaultdb"))

	var gotDatabase string
	realNew := newPoolWithConfig
	newPoolWithConfig = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		gotDatabase = cfg.ConnConfig.Database
		return realNew(ctx, cfg)
	}

	pool, err := Get(context.Background())
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if pool == nil {
		t.Fatal("Get returned nil pool")
	}
	if gotDatabase != "defaultdb" {
		t.Fatalf("database = %q, want defaultdb", gotDatabase)
	}
}

func TestGetUsesCustomDatabaseURLEnv(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres", "database_url_env": "APP_DATABASE_URL"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv("APP_DATABASE_URL", testDSN("customdb"))

	var gotDatabase string
	realNew := newPoolWithConfig
	newPoolWithConfig = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		gotDatabase = cfg.ConnConfig.Database
		return realNew(ctx, cfg)
	}

	if _, err := Get(context.Background()); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if gotDatabase != "customdb" {
		t.Fatalf("database = %q, want customdb", gotDatabase)
	}
}

func TestGetUsesManagedDatabaseFallback(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres", "database_url_env": "APP_DATABASE_URL"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv(managedDatabaseURLEnv, testDSN("manageddb"))

	var gotDatabase string
	realNew := newPoolWithConfig
	newPoolWithConfig = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		gotDatabase = cfg.ConnConfig.Database
		return realNew(ctx, cfg)
	}

	if _, err := Get(context.Background()); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if gotDatabase != "manageddb" {
		t.Fatalf("database = %q, want manageddb", gotDatabase)
	}
}

func TestGetRequiresPostgresConfig(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, ``)
	t.Setenv(appRootEnv, root)
	t.Setenv(defaultDatabaseURLEnv, testDSN("unused"))

	_, err := Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	if !strings.Contains(err.Error(), "dev.services.postgres is not configured") {
		t.Fatalf("error = %q, want missing postgres config", err)
	}
}

func TestGetReportsMissingDatabaseURLWithoutRawDSN(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)

	_, err := Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, defaultDatabaseURLEnv) {
		t.Fatalf("error = %q, want env name", msg)
	}
	if strings.Contains(msg, "postgres://") {
		t.Fatalf("error leaked DSN: %q", msg)
	}
}

func TestGetReportsInvalidDatabaseURLWithoutRawDSN(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)
	raw := "postgres://user:secret@%zz"
	t.Setenv(defaultDatabaseURLEnv, raw)

	_, err := Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid database URL") {
		t.Fatalf("error = %q, want invalid URL", msg)
	}
	if strings.Contains(msg, raw) || strings.Contains(msg, "secret") {
		t.Fatalf("error leaked DSN: %q", msg)
	}
}

func TestGetReusesPoolAcrossCalls(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv(defaultDatabaseURLEnv, testDSN("reusedb"))

	first, err := Get(context.Background())
	if err != nil {
		t.Fatalf("first Get returned error: %v", err)
	}
	second, err := Get(context.Background())
	if err != nil {
		t.Fatalf("second Get returned error: %v", err)
	}
	if first != second {
		t.Fatal("Get did not reuse the existing pool")
	}
}

func TestGetUsesTracedPoolConfig(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv(defaultDatabaseURLEnv, testDSN("tracedb"))

	realNew := newPoolWithConfig
	newPoolWithConfig = func(ctx context.Context, cfg *pgxpool.Config) (*pgxpool.Pool, error) {
		if cfg.ConnConfig.Tracer == nil {
			t.Fatal("pool config tracer is nil")
		}
		return realNew(ctx, cfg)
	}

	if _, err := Get(context.Background()); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
}

func TestGetRedactsPoolCreationError(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)
	dsn := "postgres://user:secret@localhost/redacteddb?sslmode=disable"
	t.Setenv(defaultDatabaseURLEnv, dsn)
	newPoolWithConfig = func(context.Context, *pgxpool.Config) (*pgxpool.Pool, error) {
		return nil, fmt.Errorf("dial %s with password secret failed", dsn)
	}

	_, err := Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	msg := err.Error()
	if strings.Contains(msg, dsn) || strings.Contains(msg, "secret") {
		t.Fatalf("error leaked DSN: %q", msg)
	}
}

func TestMustGetPanicsOnError(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"postgres": {"kind": "postgres"}`)
	t.Setenv(appRootEnv, root)

	defer func() {
		if recover() == nil {
			t.Fatal("MustGet did not panic")
		}
	}()
	MustGet(context.Background())
}

func resetDBForTest(t *testing.T) {
	t.Helper()
	defaultPoolMu.Lock()
	if defaultPool != nil {
		defaultPool.Close()
	}
	defaultPool = nil
	defaultPoolDSN = ""
	defaultPoolMu.Unlock()

	oldLoadDotEnv := loadDotEnv
	oldDiscoverRoot := discoverRoot
	oldGetEnv := getEnv
	oldParseConfig := parseConfig
	oldNewPoolWithConfig := newPoolWithConfig

	loadDotEnv = func() error { return nil }
	t.Cleanup(func() {
		defaultPoolMu.Lock()
		if defaultPool != nil {
			defaultPool.Close()
		}
		defaultPool = nil
		defaultPoolDSN = ""
		defaultPoolMu.Unlock()
		loadDotEnv = oldLoadDotEnv
		discoverRoot = oldDiscoverRoot
		getEnv = oldGetEnv
		parseConfig = oldParseConfig
		newPoolWithConfig = oldNewPoolWithConfig
	})
}

func writeAppConfig(t *testing.T, services string) string {
	t.Helper()
	root := t.TempDir()
	config := fmt.Sprintf(`{
		"name": "db-test",
		"dev": {
			"services": {
				%s
			}
		}
	}`, services)
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(config), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return root
}

func testDSN(database string) string {
	return "postgres://user:pass@localhost/" + database + "?sslmode=disable"
}
