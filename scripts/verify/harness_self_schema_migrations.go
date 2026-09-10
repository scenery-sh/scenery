package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/postgresdb"
)

// All failures, interruption, and writes target the already owned cache
// fixture schema. Public CLI execution supplies lifecycle ownership; SQL reads
// independently establish whether DDL and its migration evidence committed.
func runHarnessSchemaMigrations(ctx context.Context, repoRoot, root string, database *sql.DB) (map[string]any, error) {
	configPath := filepath.Join(root, ".scenery.json")
	before, err := os.ReadFile(configPath)
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.WriteFile(configPath, before, 0o600) }()
	var config map[string]any
	if err := json.Unmarshal(before, &config); err != nil {
		return nil, err
	}
	config["database"] = map[string]any{"migrations": []map[string]string{{"service": "cache", "directory": "cache/db/migrations", "initial_verification": "cache/db/verify_initial.sql"}}}
	configured, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(configPath, configured, 0o600); err != nil {
		return nil, err
	}
	directory := filepath.Join(root, "cache/db/migrations")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	verificationPath := filepath.Join(root, "cache/db/verify_initial.sql")
	if err := os.WriteFile(verificationPath, []byte("SELECT false;"), 0o600); err != nil {
		return nil, err
	}
	write := func(name, source string) error {
		return os.WriteFile(filepath.Join(directory, name), []byte(source), 0o600)
	}
	run := func(status bool, extra ...string) ([]postgresdb.SchemaMigrationResult, error) {
		args := []string{"db", "migrate", "cache", "--app-root", root, "-o", "json"}
		args = append(args, extra...)
		if status {
			args = append(args, "--status")
		}
		var output bytes.Buffer
		runErr := runProduct(ctx, repoRoot, &output, args...)
		var result struct {
			Schemas []postgresdb.SchemaMigrationResult `json:"schemas"`
		}
		if decodeErr := decodeCLIJSON(output.Bytes(), &result); decodeErr != nil {
			return nil, fmt.Errorf("migration result: %w: %v", decodeErr, runErr)
		}
		return result.Schemas, runErr
	}
	assertState := func(table, column string, want bool) error {
		var exists bool
		query := `SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_schema='cache' AND table_name=$1)`
		arguments := []any{table}
		if column != "" {
			query = `SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema='cache' AND table_name=$1 AND column_name=$2)`
			arguments = append(arguments, column)
		}
		if err := database.QueryRowContext(ctx, query, arguments...).Scan(&exists); err != nil {
			return err
		}
		if exists != want {
			return fmt.Errorf("migration state %s.%s exists=%t, want %t", table, column, exists, want)
		}
		return nil
	}
	initial := "CREATE TABLE migration_notes(id integer PRIMARY KEY, content text NOT NULL);"
	if err := write("0001_initialize.sql", initial); err != nil {
		return nil, err
	}
	if _, err := database.ExecContext(ctx, `CREATE TABLE cache.partial_initialization(id int)`); err != nil {
		return nil, err
	}
	if result, err := run(false); err == nil || len(result) != 1 || !strings.Contains(result[0].Error, "without a migration ledger") {
		return nil, fmt.Errorf("unknown populated schema was not diagnosed: %+v: %v", result, err)
	}
	if _, err := database.ExecContext(ctx, `DROP TABLE cache.partial_initialization`); err != nil {
		return nil, err
	}
	if err := write("0001_initialize.sql", initial+" SELECT missing_initialization_column;"); err != nil {
		return nil, err
	}
	if _, err := run(false); err == nil {
		return nil, fmt.Errorf("broken initialization succeeded")
	}
	if err := assertState("migration_notes", "", false); err != nil {
		return nil, err
	}
	if err := assertState("_scenery_migrations", "", false); err != nil {
		return nil, err
	}
	if err := write("0001_initialize.sql", initial); err != nil {
		return nil, err
	}
	if result, err := run(true); err != nil || len(result) != 1 || result[0].Status != "pending" {
		return nil, fmt.Errorf("empty schema migration status: %+v: %v", result, err)
	}
	if err := assertState("_scenery_migrations", "", false); err != nil {
		return nil, err
	}
	if _, err := run(false); err != nil {
		return nil, err
	}
	if _, err := database.ExecContext(ctx, `INSERT INTO cache.migration_notes VALUES (1, 'retained application row')`); err != nil {
		return nil, err
	}
	second := "ALTER TABLE migration_notes ADD COLUMN title text NOT NULL DEFAULT '';"
	if err := write("0002_title.sql", second+" SELECT missing_migration_column;"); err != nil {
		return nil, err
	}
	if _, err := run(false); err == nil {
		return nil, fmt.Errorf("broken migration succeeded")
	}
	if err := assertState("migration_notes", "title", false); err != nil {
		return nil, err
	}
	if err := write("0002_title.sql", second); err != nil {
		return nil, err
	}
	if _, err := run(false); err != nil {
		return nil, err
	}
	if err := write("0001_initialize.sql", initial+" -- changed applied history\n"); err != nil {
		return nil, err
	}
	if _, err := run(false); err == nil {
		return nil, fmt.Errorf("changed applied migration was accepted")
	}
	if err := write("0001_initialize.sql", initial); err != nil {
		return nil, err
	}
	third := "ALTER TABLE migration_notes ADD COLUMN published boolean NOT NULL DEFAULT false;"
	if err := write("0003_published.sql", third+" SELECT pg_sleep(30) /* scenery_migration_interruption */;"); err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, harnessLocalSceneryBinaryPath(repoRoot), "db", "migrate", "cache", "--app-root", root, "-o", "json")
	command.Dir = root
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		return nil, err
	}
	done := false
	defer func() {
		if !done {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	}()
	for {
		var active bool
		if err := database.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state='active' AND query LIKE '%scenery_migration_interruption%')`).Scan(&active); err != nil {
			return nil, err
		}
		if active {
			break
		}
		if err := harnessWaitContext(ctx, 50*time.Millisecond); err != nil {
			return nil, err
		}
	}
	if err := command.Process.Kill(); err != nil {
		return nil, err
	}
	if err := command.Wait(); err == nil {
		return nil, fmt.Errorf("interrupted migration exited successfully")
	}
	done = true
	if result, err := run(true); err != nil || len(result) != 1 || len(result[0].Migrations) != 3 || result[0].Migrations[2].Status != "pending" {
		return nil, fmt.Errorf("interrupted migration was not pending: %+v: %v", result, err)
	}
	if err := assertState("migration_notes", "published", false); err != nil {
		return nil, err
	}
	if err := write("0003_published.sql", third); err != nil {
		return nil, err
	}
	if _, err := run(false); err != nil {
		return nil, err
	}
	var value string
	var title string
	var published bool
	if err := database.QueryRowContext(ctx, `SELECT content, title, published FROM cache.migration_notes WHERE id=1`).Scan(&value, &title, &published); err != nil {
		return nil, err
	}
	if value != "retained application row" || title != "" || published {
		return nil, fmt.Errorf("schema evolution lost or changed the existing row")
	}
	if result, err := run(true); err != nil || len(result) != 1 || result[0].Status != "current" {
		return nil, fmt.Errorf("final migration status: %+v: %v", result, err)
	}
	// Recreate only this owned fixture's pre-ledger schema, retaining its row.
	if _, err := database.ExecContext(ctx, `DROP TABLE cache._scenery_migrations; ALTER TABLE cache.migration_notes DROP COLUMN title, DROP COLUMN published`); err != nil {
		return nil, err
	}
	if _, err := run(false, "--adopt-initial"); err == nil {
		return nil, fmt.Errorf("nonmatching initial schema was adopted")
	}
	if err := assertState("_scenery_migrations", "", false); err != nil {
		return nil, err
	}
	verification := `SELECT array_agg(column_name::text || ':' || data_type::text || ':' || is_nullable::text ORDER BY ordinal_position) = ARRAY['id:integer:NO','content:text:NO'] FROM information_schema.columns WHERE table_schema=current_schema() AND table_name='migration_notes';`
	for _, predicate := range []string{"SELECT true UNION ALL SELECT true", "SELECT 'true'", "SELECT 1", "SELECT NULL::boolean", "SELECT true WHERE false", "SELECT true, true", "SELECT true; SELECT true"} {
		if err := os.WriteFile(verificationPath, []byte(predicate), 0o600); err != nil {
			return nil, err
		}
		if _, err := run(false, "--adopt-initial"); err == nil {
			return nil, fmt.Errorf("invalid initial verification result was adopted: %s", predicate)
		}
		if err := assertState("_scenery_migrations", "", false); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(verificationPath, []byte(verification), 0o600); err != nil {
		return nil, err
	}
	if err := write("0002_title.sql", second+" SELECT missing_adoption_column;"); err != nil {
		return nil, err
	}
	if _, err := run(false, "--adopt-initial"); err == nil {
		return nil, fmt.Errorf("failed post-baseline migration succeeded")
	}
	if err := assertState("_scenery_migrations", "", false); err != nil {
		return nil, err
	}
	if err := write("0002_title.sql", second); err != nil {
		return nil, err
	}
	if result, err := run(false, "--adopt-initial"); err != nil || len(result) != 1 || result[0].Migrations[0].Verification == nil {
		return nil, fmt.Errorf("verified initial adoption: %+v: %v", result, err)
	}
	if result, err := run(true); err != nil || len(result) != 1 || result[0].Migrations[0].Verification == nil {
		return nil, fmt.Errorf("initial verification evidence was not retained: %+v: %v", result, err)
	}
	if err := database.QueryRowContext(ctx, `SELECT content FROM cache.migration_notes WHERE id=1`).Scan(&value); err != nil || value != "retained application row" {
		return nil, fmt.Errorf("initial adoption did not preserve the populated row: %v", err)
	}
	return map[string]any{"unknown_populated_rejected": true, "initialization_rollback": true, "status_non_mutating": true, "failed_migration_rollback": true, "checksum_change_rejected": true, "process_death_not_complete": true, "retry_applied": true, "existing_rows_preserved": true, "initial_adoption_predicate_enforced": true, "initial_adoption_result_shape_enforced": true, "initial_adoption_transactional": true, "initial_adoption_evidence_retained": true}, nil
}
