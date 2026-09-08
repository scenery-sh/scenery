package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/postgresname"
)

func TestSQLSupplyDistinguishesRequirementsFromAllocationAuthority(t *testing.T) {
	t.Parallel()
	for _, lifecycle := range []string{"managed", "external", "attached", "ephemeral"} {
		for _, explicit := range []bool{false, true} {
			requirements := testSQLRequirements(t, "reports")
			requirements[0].Lifecycle = lifecycle
			var env []string
			if explicit {
				env = []string{"DATABASE_URL=postgres://user:secret@localhost/shared"}
			}
			bindings, err := resolveSQLSupply(requirements, env, true)
			allowed := explicit || lifecycle == "managed"
			if (err == nil) != allowed || (allowed && len(bindings) != 1) {
				t.Fatalf("lifecycle=%s explicit=%t bindings=%+v error=%v", lifecycle, explicit, bindings, err)
			}
		}
	}
	durable := compiler.SQLRequirements{{Kind: compiler.SQLDurable, Name: "scenery", Schema: "scenery", Lifecycle: "managed"}}
	remote := []string{"SCENERY_DURABLE_ENDPOINT=https://worker.invalid"}
	if bindings, err := resolveSQLSupply(durable, remote, false); err != nil || len(bindings) != 0 {
		t.Fatalf("remote durable-only selected local SQL: %+v %v", bindings, err)
	}
	withAuth := append(durable, compiler.SQLRequirement{Kind: compiler.SQLStandardAuth, Name: "scenery", Schema: "scenery", Lifecycle: "managed"})
	if _, err := resolveSQLSupply(withAuth, remote, false); err == nil {
		t.Fatal("remote durable endpoint incorrectly satisfied standard-auth SQL")
	}
	if bindings, err := resolveSQLSupply(nil, nil, false); err != nil || len(bindings) != 0 {
		t.Fatalf("no-SQL program required supply: %+v %v", bindings, err)
	}
}

// Pure supply tests receive the compiler's value directly. Integration tests
// compile actual declarations instead of teaching config fixtures an old format.
func testSQLRequirements(t *testing.T, names ...string) compiler.SQLRequirements {
	t.Helper()
	var requirements compiler.SQLRequirements
	for _, name := range names {
		schema, err := postgresname.SchemaNameFor(name)
		if err != nil {
			t.Fatal(err)
		}
		requirements = append(requirements, compiler.SQLRequirement{
			Kind: compiler.SQLDataSource, Address: "app/data_source/" + name,
			Name: name, Schema: schema, Lifecycle: "managed",
		})
	}
	return requirements
}

func writeSQLTestDeclarations(t *testing.T, root string, names ...string) {
	t.Helper()
	var source, inputs, declarations, dependencies strings.Builder
	source.WriteString("application \"sql_fixture\" {}\nprovider \"postgres\" { source = \"registry.scenery.dev/core/postgres\" }\n")
	for i, name := range names {
		key := fmt.Sprintf("database%d", i)
		fmt.Fprintf(&source, "data_source %q {\n provider = provider.postgres\n lifecycle = \"managed\"\n require_capabilities = [\"sql.query/v1\", \"sql.transaction/v1\"]\n config = { database = %q }\n}\n", key, name)
		fmt.Fprintf(&inputs, "  %s = data_source.%s\n", key, key)
		fmt.Fprintf(&declarations, "input %q { type = resource_ref(\"data_source\") }\n", key)
		fmt.Fprintf(&dependencies, " dependency %q { instance = var.%s }\n", key, key)
	}
	fmt.Fprintf(&source, "module \"requirements\" {\n source = \"./requirements\"\n inputs = {\n%s }\n}\n", inputs.String())
	writeTestAppFile(t, root, "app.scn", source.String())
	writeTestAppFile(t, root, "requirements/package.scn", "package \"requirements\" {\n go_contract { import_path = \"example.test/sqlfixture/requirements\" }\n}\n"+declarations.String()+"service \"api\" {\n runtime = \"go\"\n implementation { constructor = \"New\" }\n"+dependencies.String()+"}\noperation \"read\" {\n service = service.api\n input = std.type.unit\n handler { method = \"Read\" }\n result \"ok\" { type = std.type.unit }\n}\n")
	lock, err := os.ReadFile(filepath.Join("..", "..", "testdata", "apps", "worktree-postgres", "app.lock.scn"))
	if err != nil {
		t.Fatal(err)
	}
	writeTestAppFile(t, root, "app.lock.scn", string(lock))
}
