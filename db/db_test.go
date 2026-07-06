package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveDatabaseURLUsesServiceEnv(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"reports": {}`)
	t.Setenv(appRootEnv, root)
	t.Setenv("REPORTS_DATABASE_URL", "postgres://user:secret@localhost/reports?search_path=reports%2Cscenery")

	resolved, err := resolveDatabaseURL("reports")
	if err != nil {
		t.Fatalf("resolveDatabaseURL returned error: %v", err)
	}
	if resolved.URL != "postgres://user:secret@localhost/reports?search_path=reports%2Cscenery" || resolved.Source != "REPORTS_DATABASE_URL" || resolved.Schema != "reports" {
		t.Fatalf("resolveDatabaseURL = %+v", resolved)
	}
}

func TestResolveDatabaseURLDerivesServiceURLFromAppEnv(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"reports": {}`)
	t.Setenv(appRootEnv, root)
	t.Setenv("DATABASE_URL", "postgres://user:secret@localhost/app?sslmode=disable")

	resolved, err := resolveDatabaseURL("reports")
	if err != nil {
		t.Fatalf("resolveDatabaseURL returned error: %v", err)
	}
	if resolved.Source != "DATABASE_URL" || !strings.Contains(resolved.URL, "search_path=reports%2Cscenery") || !strings.Contains(resolved.URL, "sslmode=disable") {
		t.Fatalf("resolveDatabaseURL = %+v", resolved)
	}
}

func TestResolveDatabaseURLUsesDiscoveredRegistry(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, ``)
	t.Setenv(appRootEnv, root)
	t.Setenv("SCENERY_DATABASE_JSON", `{"database":"app_abc","url":"postgres://u:p@localhost/app","source":"managed","schemas":[{"service":"reports","schema":"reports","url":"postgres://u:p@localhost/app?search_path=reports%2Cscenery"}]}`)

	resolved, err := resolveDatabaseURL("reports")
	if err != nil {
		t.Fatalf("resolveDatabaseURL returned error: %v", err)
	}
	if resolved.URL != "postgres://u:p@localhost/app?search_path=reports%2Cscenery" || resolved.Source != "SCENERY_DATABASE_JSON" {
		t.Fatalf("resolveDatabaseURL = %+v", resolved)
	}
}

func TestResolveDatabaseURLRequiresNameWhenMultipleServicesExist(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"main": {}, "reports": {}`)
	t.Setenv(appRootEnv, root)

	_, err := resolveDatabaseURL()
	if err == nil || !strings.Contains(err.Error(), "database service name is required when 2 services are configured") {
		t.Fatalf("resolveDatabaseURL error = %v", err)
	}
}

func TestGetReportsMissingDatabaseURL(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {}`)
	t.Setenv(appRootEnv, root)

	_, err := Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "auth") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestGetRejectsNonPostgresDatabaseURL(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {}`)
	t.Setenv(appRootEnv, root)
	t.Setenv("DATABASE_URL", "mysql://localhost/auth")

	_, err := Get(context.Background())
	if err == nil || !strings.Contains(err.Error(), "postgres") || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestMustGetPanicsOnError(t *testing.T) {
	resetDBForTest(t)
	root := writeAppConfig(t, `"auth": {}`)
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
