package main

import (
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestSeedDiscoveryDoesNotValidateSQLCGenerationInputs(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"config", "receipt", "symlink"} {
		t.Run(failure, func(t *testing.T) {
			root := t.TempDir()
			writeSQLTestDeclarations(t, root, "catalog")
			writeTestAppFile(t, root, "catalog/db/seed.sql", "insert into catalog.items(id) values (1);\n")
			writeTestAppFile(t, root, "sqlc.yaml", "version: \"2\"\nsql:\n - engine: postgresql\n   schema: catalog/db/schema.sql\n   queries: catalog/db/queries.sql\n   gen:\n     go:\n       out: catalog/db/gen\n")
			cfg := appcfg.Config{Name: "demo"}
			cfg.Generators.SQLC.Provider = "sqlc"
			switch failure {
			case "config":
				writeTestAppFile(t, root, "sqlc.yaml", "invalid: [yaml\n")
			case "receipt":
				writeTestAppFile(t, root, ".scenery/artifacts/sqlc.json", "invalid receipt\n")
			case "symlink":
				writeTestAppFile(t, root, "query.sql", "-- name: Ping :one\nselect 1;\n")
				if err := os.Symlink(filepath.Join(root, "query.sql"), filepath.Join(root, "catalog/db/queries.sql")); err != nil {
					t.Fatal(err)
				}
			}
			plans, err := discoverDBSeedPlans(root, cfg)
			if err != nil || len(plans) != 1 || plans[0].Path != "catalog/db/seed.sql" {
				t.Fatalf("unrelated SQLC %s blocked seed discovery: %v %+v", failure, err, plans)
			}
		})
	}
}
