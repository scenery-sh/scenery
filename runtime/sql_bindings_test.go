package runtime

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"scenery.sh/internal/postgresdb"
)

func TestRuntimeSQLBindingEndpointPrecedence(t *testing.T) {
	const base = "postgres://user:secret@base.invalid/app?sslmode=disable"
	const derived = "postgres://user:secret@base.invalid/app?search_path=report_data%2Cscenery&sslmode=disable"
	const override = "postgresql://user:secret@override.invalid/other?search_path=custom"
	const invalid = "postgres://private:password@base.invalid/%ZZ"
	const endpointError = "runtime: SQL binding reports requires a valid PostgreSQL endpoint in REPORTS_DATABASE_URL or DATABASE_URL"
	const baseError = "runtime: SQL binding reports requires a valid PostgreSQL DATABASE_URL"
	const registryError = "runtime: invalid SCENERY_DATABASE_JSON SQL supply"
	registry := func(baseURL, schema, endpoint string, source postgresdb.Source) string {
		return fmt.Sprintf(`{"url":%q,"source":%q,"schemas":[{"service":"reports","schema":%q,"url":%q}]}`, baseURL, source, schema, endpoint)
	}
	want := func(baseURL, database, schema, endpoint string, source postgresdb.Source) postgresdb.Database {
		return postgresdb.Database{Database: database, URL: baseURL, Source: source, Schemas: []postgresdb.Service{{Name: "reports", Schema: schema, URL: endpoint}}}
	}
	for _, tc := range []struct {
		name     string
		env      map[string]string
		bindings []SQLBinding
		want     postgresdb.Database
		err      string
	}{
		{
			name: "base derives the compiled schema, not the binding name",
			env:  map[string]string{"DATABASE_URL": base},
			want: want(base, "app", "report_data", derived, postgresdb.SourceExternal),
		},
		{
			name: "explicit override wins without changing its search path",
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": override},
			want: want(base, "app", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "environment supply is trimmed",
			env:  map[string]string{"DATABASE_URL": " " + base + "\n", "REPORTS_DATABASE_URL": " " + override + "\n"},
			want: want(base, "app", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "whitespace override uses the base",
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": " \n"},
			want: want(base, "app", "report_data", derived, postgresdb.SourceExternal),
		},
		{
			name: "override does not require a base",
			env:  map[string]string{"REPORTS_DATABASE_URL": override},
			want: want("", "", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "valid override does not validate an unused base",
			env:  map[string]string{"DATABASE_URL": invalid, "REPORTS_DATABASE_URL": override},
			want: want(invalid, "", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "matching registry base retains managed provenance",
			env:  map[string]string{postgresdb.RegistryEnv: registry(base, "report_data", derived, postgresdb.SourceManaged)},
			want: want(base, "app", "report_data", derived, postgresdb.SourceManaged),
		},
		{
			name: "exact override can retain managed provenance",
			env:  map[string]string{postgresdb.RegistryEnv: registry(base, "report_data", derived, postgresdb.SourceManaged), "REPORTS_DATABASE_URL": derived},
			want: want(base, "app", "report_data", derived, postgresdb.SourceManaged),
		},
		{
			name: "different override removes managed provenance",
			env:  map[string]string{postgresdb.RegistryEnv: registry(base, "report_data", derived, postgresdb.SourceManaged), "REPORTS_DATABASE_URL": override},
			want: want(base, "app", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "base derivation precedes a different registry binding",
			env:  map[string]string{postgresdb.RegistryEnv: registry(base, "report_data", override, postgresdb.SourceManaged)},
			want: want(base, "app", "report_data", derived, postgresdb.SourceExternal),
		},
		{
			name: "environment base supersedes registry base",
			env:  map[string]string{"DATABASE_URL": base, postgresdb.RegistryEnv: registry("postgres://user:secret@old.invalid/app", "report_data", derived, postgresdb.SourceManaged)},
			want: want(base, "app", "report_data", derived, postgresdb.SourceExternal),
		},
		{
			name: "registry binding fallback requires matching name and schema",
			env:  map[string]string{postgresdb.RegistryEnv: registry("", "report_data", override, postgresdb.SourceExternal)},
			want: want("", "", "report_data", override, postgresdb.SourceExternal),
		},
		{
			name: "different registry schema cannot supply the binding",
			env:  map[string]string{postgresdb.RegistryEnv: registry("", "different", override, postgresdb.SourceExternal)},
			err:  endpointError,
		},
		{
			name: "whitespace registry base is invalid, not absent",
			env:  map[string]string{postgresdb.RegistryEnv: registry(" \n", "report_data", override, postgresdb.SourceExternal)},
			err:  baseError,
		},
		{
			name: "malformed base does not fall through to registry binding",
			env:  map[string]string{"DATABASE_URL": invalid, postgresdb.RegistryEnv: registry("", "report_data", override, postgresdb.SourceExternal)},
			err:  baseError,
		},
		{
			name: "malformed override does not fall through to valid base",
			env:  map[string]string{"DATABASE_URL": base, "REPORTS_DATABASE_URL": invalid},
			err:  endpointError,
		},
		{name: "missing supply", err: endpointError},
		{
			name: "invalid registry is rejected even with valid explicit supply",
			env:  map[string]string{"DATABASE_URL": base, postgresdb.RegistryEnv: "private:password"},
			err:  registryError,
		},
		{
			name:     "framework ignores a per-service override",
			env:      map[string]string{"DATABASE_URL": base, "SCENERY_DATABASE_URL": invalid},
			bindings: []SQLBinding{{Name: "scenery", Schema: "scenery"}},
			want: postgresdb.Database{Database: "app", URL: base, Source: postgresdb.SourceExternal,
				Schemas: []postgresdb.Service{{Name: "scenery", Schema: "scenery", URL: "postgres://user:secret@base.invalid/app?search_path=scenery&sslmode=disable"}}},
		},
		{
			name:     "remote durable-only binding needs no local endpoint",
			env:      map[string]string{envDurableEndpoint: "https://worker.invalid"},
			bindings: []SQLBinding{{Name: "scenery", Schema: "scenery", DurableOnly: true}},
			want:     postgresdb.Database{Source: postgresdb.SourceExternal},
		},
		{
			name:     "empty compiled schema remains invalid",
			env:      map[string]string{"REPORTS_DATABASE_URL": override},
			bindings: []SQLBinding{{Name: "reports"}},
			err:      "runtime: SQL bindings require distinct non-empty names and schemas",
		},
		{
			name:     "duplicate compiled names remain invalid",
			env:      map[string]string{"DATABASE_URL": base},
			bindings: []SQLBinding{{Name: "reports", Schema: "report_data"}, {Name: "reports", Schema: "different"}},
			err:      "runtime: SQL bindings require distinct non-empty names and schemas",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bindings := tc.bindings
			if bindings == nil {
				bindings = []SQLBinding{{Name: "reports", Schema: "report_data"}}
			}
			got, err := resolveRuntimeSQLBindings(bindings, func(key string) string { return tc.env[key] })
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("error = %v, want %q", err, tc.err)
				}
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "password") {
					t.Fatal("endpoint diagnostic exposed credentials")
				}
				return
			}
			if err != nil || !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("database = %+v, error = %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestRuntimeSQLBindingsUseCompiledNamesAndExplicitSupply(t *testing.T) {
	bindings := []SQLBinding{{Name: "billing-data", Schema: "billing_data"}, {Name: "scenery", Schema: "scenery", DurableOnly: true}}
	for _, tc := range []struct {
		name      string
		env       map[string]string
		count     int
		wantError bool
	}{
		{name: "app URL", env: map[string]string{"DATABASE_URL": "postgres://u:p@localhost/app"}, count: 2},
		{name: "framework uses canonical URL", env: map[string]string{"DATABASE_URL": "postgres://u:p@localhost/app", "SCENERY_DATABASE_URL": "invalid"}, count: 2},
		{name: "remote durable", env: map[string]string{"DATABASE_URL": "postgres://u:p@localhost/app", envDurableEndpoint: "https://worker.invalid"}, count: 1},
		{name: "per binding supply", env: map[string]string{"BILLING_DATA_DATABASE_URL": "postgres://u:p@localhost/billing", envDurableEndpoint: "https://worker.invalid"}, count: 1},
		{name: "missing supply", env: map[string]string{}, wantError: true},
		{name: "invalid URL", env: map[string]string{"DATABASE_URL": "postgres://private:password@localhost/%ZZ"}, wantError: true},
		{name: "invalid registry", env: map[string]string{postgresdb.RegistryEnv: "private:password"}, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			database, err := resolveRuntimeSQLBindings(bindings, func(key string) string { return tc.env[key] })
			if (err != nil) != tc.wantError {
				t.Fatalf("database=%+v err=%v", database, err)
			}
			if err != nil {
				if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "password") {
					t.Fatalf("credential leaked: %v", err)
				}
				return
			}
			if len(database.Schemas) != tc.count || database.Schemas[0].Name != "billing-data" || database.Schemas[0].Schema != "billing_data" || database.Source != postgresdb.SourceExternal {
				t.Fatalf("compiled identity or source changed: %+v", database)
			}
		})
	}
}

func TestRuntimeSQLBindingsDoNotRequireLocalDurableSupply(t *testing.T) {
	database, err := resolveRuntimeSQLBindings([]SQLBinding{{Name: "scenery", Schema: "scenery", DurableOnly: true}}, func(key string) string {
		if key == envDurableEndpoint {
			return "https://worker.invalid"
		}
		return ""
	})
	if err != nil || len(database.Schemas) != 0 {
		t.Fatalf("remote durable-only binding=%+v err=%v", database, err)
	}
}

func TestRuntimeSQLBindingsPreserveOnlyMatchingManagedSupply(t *testing.T) {
	const base = "postgres://u:p@localhost/app"
	const bindingURL = "postgres://u:p@localhost/app?search_path=reports%2Cscenery"
	for _, override := range []string{"", "postgres://u:p@elsewhere/shared"} {
		env := map[string]string{
			"DATABASE_URL":         base,
			"REPORTS_DATABASE_URL": override,
			postgresdb.RegistryEnv: `{"database":"app","url":"` + base + `","source":"managed","schemas":[{"service":"reports","schema":"reports","url":"` + bindingURL + `"}]}`,
		}
		database, err := resolveRuntimeSQLBindings([]SQLBinding{{Name: "reports", Schema: "reports"}}, func(key string) string { return env[key] })
		wantSource := postgresdb.SourceManaged
		if override != "" {
			wantSource = postgresdb.SourceExternal
		}
		if err != nil || database.Source != wantSource || len(database.Schemas) != 1 {
			t.Fatalf("override=%t supply=%+v error=%v", override != "", database, err)
		}
	}
}
