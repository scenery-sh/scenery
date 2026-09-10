package postgresdb

import (
	"crypto/sha256"
	"fmt"
	"testing"
)

func TestMigrationHistoryRequiresExactContiguousSource(t *testing.T) {
	script := "CREATE TABLE notes(id integer PRIMARY KEY);"
	migration := SchemaMigration{Revision: 1, Path: "notes/db/migrations/0001_initial.sql", SQL: script, Checksum: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(script)))}
	if err := validateSchemaMigrations([]SchemaMigration{migration}); err != nil {
		t.Fatal(err)
	}
	if err := matchSchemaMigrations([]SchemaMigration{migration}, []SchemaMigration{migration}); err != nil {
		t.Fatal(err)
	}
	changed := migration
	changed.Checksum = "sha256:changed"
	if err := matchSchemaMigrations([]SchemaMigration{changed}, []SchemaMigration{migration}); err == nil {
		t.Fatal("changed applied source was accepted")
	}
	if err := matchSchemaMigrations(nil, []SchemaMigration{migration}); err == nil {
		t.Fatal("source behind database was accepted")
	}
	migration.Revision = 2
	if err := validateSchemaMigrations([]SchemaMigration{migration}); err == nil {
		t.Fatal("migration history gap was accepted")
	}
}

func TestMigrationSQLCannotEndItsOwningTransaction(t *testing.T) {
	for _, script := range []string{
		"CREATE TABLE notes(id int); COMMIT;",
		"CREATE TABLE notes(id int); /* outer /* inner */ still outer */ END;",
		"SELECT 'not; COMMIT'; ROLLBACK;",
		`SELECT E'escaped\'; COMMIT'; ABORT;`,
		"DO $body$ BEGIN PERFORM 1; END $body$; PREPARE TRANSACTION 'escape';",
		"BEGIN; CREATE TABLE notes(id int); END;",
		"SET search_path TO public;",
		"CREATE TABLE notes(id int); /* missing close",
	} {
		if err := ValidateMigrationSQL(script); err == nil {
			t.Fatalf("accepted transaction escape or invalid input: %s", script)
		}
	}
	for _, script := range []string{
		"-- comment\nCREATE TABLE notes(id int, content text DEFAULT 'COMMIT;');",
		"DO $body$ BEGIN PERFORM 1; END $body$; ALTER TABLE notes ADD COLUMN title text;",
		`/* outer /* inner */ still outer */ INSERT INTO notes VALUES (1, E'escaped\'; COMMIT');`,
		"CREATE TABLE \"COMMIT;\" (id int); COMMENT ON TABLE \"COMMIT;\" IS 'example';",
	} {
		if err := ValidateMigrationSQL(script); err != nil {
			t.Fatalf("rejected ordinary migration: %v", err)
		}
	}
}

func TestInitialVerificationRequiresOneQuery(t *testing.T) {
	for _, script := range []string{"SELECT true; SELECT true", "CREATE TABLE notes(id int)", "-- no query", "SELECT true; COMMIT"} {
		if err := ValidateInitialVerificationSQL(script); err == nil {
			t.Fatalf("accepted non-predicate SQL: %s", script)
		}
	}
	for _, script := range []string{"SELECT true;", "/* comment */ WITH expected AS (SELECT ';' AS value) SELECT value = ';' FROM expected;"} {
		if err := ValidateInitialVerificationSQL(script); err != nil {
			t.Fatalf("rejected initial verification: %v", err)
		}
	}
}
