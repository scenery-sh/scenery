package postgresdb

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"scenery.sh/internal/postgresname"
)

func TestDatabaseNameForIsStableAndKeepsHash(t *testing.T) {
	a := postgresname.DatabaseNameFor("My App With A Very Very Very Long Name", "/tmp/worktree-a")
	b := postgresname.DatabaseNameFor("My App With A Very Very Very Long Name", "/tmp/worktree-a")
	c := postgresname.DatabaseNameFor("My App With A Very Very Very Long Name", "/tmp/worktree-b")
	if a != b {
		t.Fatalf("DatabaseNameFor not stable: %q != %q", a, b)
	}
	if a == c {
		t.Fatalf("DatabaseNameFor did not distinguish worktrees: %q", a)
	}
	if len(a) > 63 {
		t.Fatalf("DatabaseNameFor length = %d, want <= 63: %q", len(a), a)
	}
	if suffix := a[len(a)-12:]; strings.Trim(suffix, "0123456789abcdef") != "" {
		t.Fatalf("DatabaseNameFor suffix = %q, want hex hash", suffix)
	}
}

func TestSchemaNameForRejectsReservedNames(t *testing.T) {
	for _, name := range []string{"scenery", "public", "information-schema", "pg_catalog"} {
		if _, err := postgresname.SchemaNameFor(name); err == nil {
			t.Fatalf("SchemaNameFor(%q) returned nil error", name)
		}
	}
}

func TestParseURLAndRedactURL(t *testing.T) {
	if _, err := ParseURL("postgres://user:secret@localhost:5432/app"); err != nil {
		t.Fatalf("ParseURL returned error: %v", err)
	}
	if _, err := ParseURL("mysql://localhost/app"); err == nil {
		t.Fatalf("ParseURL accepted non-postgres URL")
	}
	redacted := RedactURL("postgres://user:secret@localhost/app?sslmode=disable")
	if strings.Contains(redacted, "secret") || !strings.Contains(redacted, "xxxxx") {
		t.Fatalf("RedactURL = %q", redacted)
	}
}

func TestEnvAndRegistry(t *testing.T) {
	serviceURL, err := ServiceURL("postgres://u:p@localhost/app", "reports")
	if err != nil {
		t.Fatalf("ServiceURL returned error: %v", err)
	}
	database := Database{Database: "app_abc", URL: "postgres://u:p@localhost/app", Source: SourceManaged, Schemas: []Service{{Name: "reports", Schema: "reports", URL: serviceURL}}}
	envList := Env(database)
	env := strings.Join(envList, "\n")
	if !strings.Contains(env, "DATABASE_URL=postgres://u:p@localhost/app") || !strings.Contains(env, "REPORTS_DATABASE_URL="+serviceURL) {
		t.Fatalf("Env missing service URL: %s", env)
	}
	registry := ""
	for _, item := range envList {
		if after, ok := strings.CutPrefix(item, RegistryEnv+"="); ok {
			registry = after
		}
	}
	if registry == "" {
		t.Fatalf("Env missing registry: %s", env)
	}
	decoded, err := DecodeRegistry(registry)
	if err != nil || decoded.Database != "app_abc" || len(decoded.Schemas) != 1 || decoded.Schemas[0].Name != "reports" {
		t.Fatalf("DecodeRegistry = %+v err=%v", decoded, err)
	}
	database.Schemas = append(database.Schemas, Service{Name: "scenery", Schema: "scenery", URL: database.URL + "?search_path=scenery"})
	if env := strings.Join(Env(database), "\n"); strings.Contains(env, "SCENERY_DATABASE_URL=") || !strings.Contains(env, `"schema":"scenery"`) {
		t.Fatalf("framework SQL must use DATABASE_URL and retain schema metadata: %s", env)
	}
}

func TestServiceURLUsesSearchPathRuntimeParam(t *testing.T) {
	got, err := ServiceURL("postgres://user:secret@localhost/app?sslmode=disable", "reports")
	if err != nil {
		t.Fatalf("ServiceURL returned error: %v", err)
	}
	if !strings.Contains(got, "search_path=reports%2Cscenery") || !strings.Contains(got, "sslmode=disable") {
		t.Fatalf("ServiceURL = %q", got)
	}
}

func TestResolveServiceEndpointSelectsPreparedInputs(t *testing.T) {
	const base = "postgres://user:secret@localhost/app?search_path=old&sslmode=disable"
	const derived = "postgres://user:secret@localhost/app?search_path=reports%2Cscenery&sslmode=disable"
	for _, tc := range []struct {
		name, schema, override, base string
		want                         ServiceEndpoint
		wantError                    bool
	}{
		{name: "explicit override wins", schema: "reports", override: "postgres://localhost/override", base: base, want: ServiceEndpoint{URL: "postgres://localhost/override"}},
		{name: "explicit override remains opaque", override: "invalid", base: base, want: ServiceEndpoint{URL: "invalid"}},
		{name: "base derives search path", schema: "reports", base: base, want: ServiceEndpoint{URL: derived, FromBaseURL: true}},
		{name: "framework schema", schema: "scenery", base: base, want: ServiceEndpoint{URL: "postgres://user:secret@localhost/app?search_path=scenery&sslmode=disable", FromBaseURL: true}},
		{name: "absent inputs leave the decision to the consumer", schema: "reports"},
		{name: "blank schema cannot derive a base", base: base, wantError: true},
		{name: "invalid base", schema: "reports", base: "mysql://localhost/app", wantError: true},
		{name: "whitespace supplied base is not absence", schema: "reports", base: " \n", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveServiceEndpoint(tc.schema, tc.override, tc.base)
			if tc.wantError {
				if err == nil {
					t.Fatal("invalid base derivation succeeded")
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("endpoint = %+v, error = %v; want %+v", got, err, tc.want)
			}
		})
	}
}

func TestIsDuplicateDatabase(t *testing.T) {
	if !isDuplicateDatabase(&pgconn.PgError{Code: "42P04"}) {
		t.Fatalf("isDuplicateDatabase rejected duplicate_database")
	}
	if isDuplicateDatabase(fmt.Errorf("duplicate_database")) {
		t.Fatalf("isDuplicateDatabase accepted plain text error")
	}
}
