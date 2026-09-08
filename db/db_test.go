package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestResolveDatabaseURLUsesServiceEnv(t *testing.T) {
	resetDBForTest(t)
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
	t.Setenv("SCENERY_DATABASE_JSON", `{"database":"app_abc","url":"postgres://u:p@localhost/app","source":"managed","schemas":[{"service":"reports","schema":"reports","url":"postgres://u:p@localhost/app?search_path=reports%2Cscenery"}]}`)

	resolved, err := resolveDatabaseURL("reports")
	if err != nil {
		t.Fatalf("resolveDatabaseURL returned error: %v", err)
	}
	if resolved.URL != "postgres://u:p@localhost/app?search_path=reports%2Cscenery" || resolved.Source != "SCENERY_DATABASE_JSON" {
		t.Fatalf("resolveDatabaseURL = %+v", resolved)
	}
	defaultBinding, err := resolveDatabaseURL()
	if err != nil || defaultBinding != resolved {
		t.Fatalf("default binding=%+v err=%v, want %+v", defaultBinding, err, resolved)
	}
	t.Setenv("SCENERY_DATABASE_JSON", `{"database":"app_abc","url":"postgres://u:p@localhost/app","source":"managed","schemas":[{"service":"scenery","schema":"scenery","url":"postgres://u:p@localhost/app?search_path=scenery"},{"service":"reports","schema":"reports","url":"postgres://u:p@localhost/app?search_path=reports%2Cscenery"}]}`)
	defaultBinding, err = resolveDatabaseURL()
	if err != nil || defaultBinding != resolved {
		t.Fatalf("framework SQL changed the default application binding=%+v err=%v", defaultBinding, err)
	}
}

func TestResolveDatabaseURLRequiresNameWhenMultipleServicesExist(t *testing.T) {
	resetDBForTest(t)
	t.Setenv("SCENERY_DATABASE_JSON", `{"database":"app","url":"postgres://localhost/app","source":"external","schemas":[{"service":"main","schema":"main","url":"postgres://localhost/app?search_path=main"},{"service":"reports","schema":"reports","url":"postgres://localhost/app?search_path=reports"}]}`)

	_, err := resolveDatabaseURL()
	if err == nil || !strings.Contains(err.Error(), "database service name is required when 2 SQL bindings are supplied") {
		t.Fatalf("resolveDatabaseURL error = %v", err)
	}
}

func TestGetReportsMissingDatabaseURL(t *testing.T) {
	resetDBForTest(t)

	_, err := Get(context.Background(), "auth")
	if err == nil || !strings.Contains(err.Error(), "auth") || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestGetRejectsNonPostgresDatabaseURL(t *testing.T) {
	resetDBForTest(t)
	t.Setenv("DATABASE_URL", "mysql://localhost/auth")

	_, err := Get(context.Background(), "auth")
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "postgres") || !strings.Contains(err.Error(), "schema") {
		t.Fatalf("Get error = %v", err)
	}
}

func TestMustGetPanicsOnError(t *testing.T) {
	resetDBForTest(t)

	defer func() {
		if recover() == nil {
			t.Fatal("MustGet did not panic")
		}
	}()
	MustGet(context.Background())
}

func resetDBForTest(t *testing.T) {
	t.Helper()
	for _, key := range []string{"SCENERY_DATABASE_JSON", "DATABASE_URL", "AUTH_DATABASE_URL", "REPORTS_DATABASE_URL"} {
		t.Setenv(key, "")
	}
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
