package runtime

import (
	"strings"
	"testing"

	"scenery.sh/internal/postgresdb"
)

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
