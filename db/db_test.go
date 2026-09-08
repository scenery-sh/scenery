package db

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
)

func TestResolveDatabaseURLPreservesInputBoundaries(t *testing.T) {
	resetDBForTest(t)
	const base = "postgres://user:secret@base.invalid/app?sslmode=disable"
	const derived = "postgres://user:secret@base.invalid/app?search_path=reports%2Cscenery&sslmode=disable"
	const override = "postgresql://user:secret@override.invalid/other?search_path=custom"
	const supplied = "postgres://user:secret@registry.invalid/app?search_path=report_data"
	const invalid = "postgres://private:password@base.invalid/%ZZ"
	const reports = `{"service":"reports","schema":"report_data","url":"` + supplied + `"}`
	const framework = `{"service":"scenery","schema":"scenery","url":"postgres://localhost/app?search_path=scenery"}`
	const registryEnv = "SCENERY_DATABASE_JSON"
	registry := func(bindings string) string {
		return fmt.Sprintf(`{"url":%q,"source":"managed","schemas":[%s]}`, base, bindings)
	}
	for _, tc := range []struct {
		name string
		call []string
		env  map[string]string
		want resolvedDatabaseURL
		err  string
	}{
		{
			name: "standalone override wins without rewriting its search path", call: []string{"reports"},
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": override},
			want: resolvedDatabaseURL{"reports", "reports", override, "REPORTS_DATABASE_URL"},
		},
		{
			name: "standalone override defers validation to Get", call: []string{"reports"},
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": invalid},
			want: resolvedDatabaseURL{"reports", "reports", invalid, "REPORTS_DATABASE_URL"},
		},
		{
			name: "environment and explicit name are trimmed", call: []string{" reports\n"},
			env:  map[string]string{"DATABASE_URL": " " + base + "\n", "REPORTS_DATABASE_URL": " " + override + "\n"},
			want: resolvedDatabaseURL{"reports", "reports", override, "REPORTS_DATABASE_URL"},
		},
		{
			name: "standalone base derives the named schema", call: []string{"reports"},
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": " \n"},
			want: resolvedDatabaseURL{"reports", "reports", derived, "DATABASE_URL"},
		},
		{
			name: "valid override does not validate an unused base", call: []string{"reports"},
			env:  map[string]string{"DATABASE_URL": invalid, "REPORTS_DATABASE_URL": override},
			want: resolvedDatabaseURL{"reports", "reports", override, "REPORTS_DATABASE_URL"},
		},
		{
			name: "compiled registry takes precedence over environment", call: []string{"reports"},
			env:  map[string]string{registryEnv: registry(reports), "DATABASE_URL": invalid, "REPORTS_DATABASE_URL": override},
			want: resolvedDatabaseURL{"reports", "report_data", supplied, registryEnv},
		},
		{
			name: "single compiled binding is the default",
			env:  map[string]string{registryEnv: registry(reports)},
			want: resolvedDatabaseURL{"reports", "report_data", supplied, registryEnv},
		},
		{
			name: "framework does not make the application default ambiguous",
			env:  map[string]string{registryEnv: registry(framework + "," + reports)},
			want: resolvedDatabaseURL{"reports", "report_data", supplied, registryEnv},
		},
		{
			name: "framework alone can be the supplied default",
			env:  map[string]string{registryEnv: registry(framework)},
			want: resolvedDatabaseURL{"scenery", "scenery", "postgres://localhost/app?search_path=scenery", registryEnv},
		},
		{
			name: "unknown compiled name cannot fall back to environment", call: []string{"unknown"},
			env: map[string]string{registryEnv: registry(reports), "DATABASE_URL": base, "UNKNOWN_DATABASE_URL": override},
			err: `scenery db: database service "unknown" is not in the compiled SQL bindings`,
		},
		{
			name: "two application bindings require a name",
			env:  map[string]string{registryEnv: registry(reports + `,{"service":"other","schema":"other","url":"postgres://localhost/app"}`)},
			err:  "scenery db: database service name is required when 2 SQL bindings are supplied",
		},
		{
			name: "unnamed standalone does not invent a default",
			env:  map[string]string{"DATABASE_URL": base},
			err:  "scenery db: database service name is required when 0 SQL bindings are supplied",
		},
		{
			name: "standalone framework name is still reserved", call: []string{"scenery"},
			env: map[string]string{"DATABASE_URL": base, "SCENERY_DATABASE_URL": override},
			err: `scenery db: invalid SQL binding "scenery": postgres schema "scenery" is reserved by plan 0097`,
		},
		{
			name: "registry metadata without schemas does not supply standalone base", call: []string{"reports"},
			env: map[string]string{registryEnv: registry("")},
			err: `scenery db: service "reports" schema "reports" database URL is not configured; set REPORTS_DATABASE_URL or DATABASE_URL`,
		},
		{
			name: "empty registry schemas permit explicit standalone supply", call: []string{"reports"},
			env:  map[string]string{registryEnv: registry(""), "DATABASE_URL": base},
			want: resolvedDatabaseURL{"reports", "reports", derived, "DATABASE_URL"},
		},
		{
			name: "invalid registry fails before explicit supply", call: []string{"reports"},
			env: map[string]string{registryEnv: "private:password", "REPORTS_DATABASE_URL": override},
			err: "scenery db: invalid SCENERY_DATABASE_JSON SQL supply",
		},
		{
			name: "incomplete registry fails before explicit supply", call: []string{"reports"},
			env: map[string]string{registryEnv: `{"schemas":[{"service":"reports","url":""}]}`, "DATABASE_URL": base},
			err: "scenery db: invalid SCENERY_DATABASE_JSON SQL supply",
		},
		{
			name: "invalid base produces a credential-free diagnostic", call: []string{"reports"},
			env: map[string]string{"DATABASE_URL": invalid},
			err: `scenery db: service "reports" schema "reports" requires a valid PostgreSQL URL in DATABASE_URL`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			previous := getEnv
			getEnv = func(key string) string { return tc.env[key] }
			t.Cleanup(func() { getEnv = previous })
			got, err := resolveDatabaseURL(tc.call...)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "password") {
					t.Fatal("endpoint selection diagnostic exposed credentials")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("selection = %+v, error = %v; want %+v", got, err, tc.want)
			}
		})
	}
}

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
