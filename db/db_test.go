package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/sqlitedb"
)

func TestGetUsesSQLiteServiceEnv(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite", "database_url_env": "AUTH_DB"}`)
	t.Setenv(appRootEnv, root)
	path := filepath.Join(root, "auth.sqlite")
	t.Setenv("AUTH_DB", sqlitedb.URLForPath(path))

	pool, err := Get(context.Background())
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if pool == nil {
		t.Fatal("Get returned nil pool")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("sqlite file was not created: %v", err)
	}
}

func TestGetDefaultsToDBServiceWhenMultipleServicesExist(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"db": {"kind": "sqlite", "database_url_env": "MAIN_DB"}, "billing": {"kind": "sqlite"}`)
	t.Setenv(appRootEnv, root)
	path := filepath.Join(root, "main.sqlite")
	t.Setenv("MAIN_DB", sqlitedb.URLForPath(path))

	if _, err := Get(context.Background()); err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("default db sqlite file was not created: %v", err)
	}
}

func TestGetUsesNamedService(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite"}, "billing": {"kind": "sqlite", "database_url_env": "BILLING_DB"}`)
	t.Setenv(appRootEnv, root)
	path := filepath.Join(root, "billing.sqlite")
	t.Setenv("BILLING_DB", sqlitedb.URLForPath(path))

	if _, err := Get(context.Background(), "billing"); err != nil {
		t.Fatalf("Get named service returned error: %v", err)
	}
}

func TestGetUsesDiscoveredServiceMetadata(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, ``)
	t.Setenv(appRootEnv, root)
	path := filepath.Join(root, "tasks.sqlite")
	records := []map[string]string{{
		"service": "tasks",
		"path":    path,
	}}
	data, err := json.Marshal(records)
	if err != nil {
		t.Fatalf("Marshal returned error: %v", err)
	}
	t.Setenv("SCENERY_SQLITE_DATABASES_JSON", string(data))

	if _, err := Get(context.Background(), "tasks"); err != nil {
		t.Fatalf("Get discovered service returned error: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("discovered service sqlite file was not created: %v", err)
	}
}

func TestGetReportsMissingDatabaseURL(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite"}`)
	t.Setenv(appRootEnv, root)

	_, err := Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "AUTH_DATABASE_URL") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestGetReportsInvalidDatabaseURLWithoutRawDSN(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite"}`)
	t.Setenv(appRootEnv, root)
	raw := "mysql://user:secret@localhost/db"
	t.Setenv("AUTH_DATABASE_URL", raw)

	_, err := Get(context.Background())
	if err == nil {
		t.Fatal("Get returned nil error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "invalid SQLite database URL") {
		t.Fatalf("error = %q, want invalid URL", msg)
	}
	if strings.Contains(msg, raw) || strings.Contains(msg, "secret") {
		t.Fatalf("error leaked DSN: %q", msg)
	}
}

func TestGetReusesPoolAcrossCalls(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite"}`)
	t.Setenv(appRootEnv, root)
	t.Setenv("AUTH_DATABASE_URL", sqlitedb.URLForPath(filepath.Join(root, "auth.sqlite")))

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

func TestMustGetPanicsOnError(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {"kind": "sqlite"}`)
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
	poolsMu.Lock()
	for _, pool := range pools {
		_ = pool.Close()
	}
	pools = map[string]*sql.DB{}
	poolsMu.Unlock()

	oldLoadDotEnv := loadDotEnv
	loadDotEnv = func() error { return nil }
	t.Cleanup(func() {
		poolsMu.Lock()
		for _, pool := range pools {
			_ = pool.Close()
		}
		pools = map[string]*sql.DB{}
		poolsMu.Unlock()
		loadDotEnv = oldLoadDotEnv
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
