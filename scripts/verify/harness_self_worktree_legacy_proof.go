package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"scenery.sh/internal/postgresname"
)

func (s *worktreeLegacySandbox) prove(e map[string]any) error {
	old, err := s.up(true, "/app-old")
	if err != nil {
		return err
	}
	sibling, err := s.up(true, "/app-sibling")
	if err != nil {
		return err
	}
	oldContainer, err := s.run("docker", "inspect", "--format", "{{.Id}}", "scenery-postgres")
	if err != nil {
		return err
	}
	cid := strings.TrimSpace(string(oldContainer))
	volume, err := s.run("docker", "volume", "inspect", "scenery-postgres-data", "--format", "{{.Name}} {{.CreatedAt}}")
	if err != nil {
		return err
	}
	agentPID, err := s.legacyAgentPID()
	if err != nil {
		return err
	}
	if _, err := s.run("cp", "-p", "/legacy-state/agent/postgres/server.json", "/baseline-server.before"); err != nil {
		return err
	}
	db := postgresname.DatabaseNameFor("worktree-library", "/app-old")
	if _, err := s.sql(cid, db, `INSERT INTO library.books(book_id,title,borrower) VALUES ('migration-proof','Native ownership and grants','reader'); CREATE ROLE fixture_reader NOLOGIN; GRANT USAGE ON SCHEMA library TO fixture_reader; GRANT SELECT ON library.books TO fixture_reader; CREATE EXTENSION pgcrypto; CREATE TABLE IF NOT EXISTS scenery.seed_runs (app_id text not null, path text not null, sha256 text not null, applied_at timestamptz not null default now(), primary key(app_id,path)); INSERT INTO scenery.seed_runs(app_id,path,sha256) VALUES ('worktree-library','migration-fixture.sql','migration-proof');`); err != nil {
		return err
	}
	before, err := s.sql(cid, db, worktreeLegacyDataSQL)
	if err != nil {
		return err
	}
	sentinelDone := make(chan struct {
		out []byte
		err error
	}, 1)
	go func() {
		out, err := s.run("sh", "-c", `while ! test -f /sentinel-stop; do if wget -q -O /dev/null "$1"; then echo ok; else echo fail; fi; sleep 0.05; done`, "sentinel", worktreeLegacyAPI(sibling)+"/books")
		sentinelDone <- struct {
			out []byte
			err error
		}{out, err}
	}()
	stopped := false
	stop := func() error {
		if stopped {
			return nil
		}
		stopped = true
		if _, err := s.run("touch", "/sentinel-stop"); err != nil {
			return err
		}
		result := <-sentinelDone
		requests, failures := bytes.Count(result.out, []byte("ok\n")), bytes.Count(result.out, []byte("fail\n"))
		e["legacy_sibling_requests"], e["legacy_sibling_failures"] = requests+failures, failures
		if result.err != nil || requests < 2 || failures != 0 {
			return fmt.Errorf("legacy sibling sentinel failed: requests=%d failures=%d error=%v", requests+failures, failures, result.err)
		}
		return nil
	}
	defer func() { _ = stop() }()
	current, err := s.up(false, "/app-new")
	if err != nil {
		return err
	}
	if _, err := s.run("wget", "-q", "-O", "/dev/null", worktreeLegacyAPI(current)+"/books"); err != nil {
		return err
	}
	if _, err := s.cli(false, "/app-new", "down"); err != nil {
		return err
	}
	afterPID, err := s.legacyAgentPID()
	if err != nil || afterPID != agentPID {
		return fmt.Errorf("current runtime replaced the baseline shared agent")
	}
	afterContainer, err := s.run("docker", "inspect", "--format", "{{.Id}}", "scenery-postgres")
	if err != nil || !bytes.Equal(oldContainer, afterContainer) {
		return fmt.Errorf("current runtime replaced the baseline cluster")
	}
	afterVolume, err := s.run("docker", "volume", "inspect", "scenery-postgres-data", "--format", "{{.Name}} {{.CreatedAt}}")
	if err != nil || !bytes.Equal(volume, afterVolume) {
		return fmt.Errorf("current runtime replaced the baseline data volume")
	}
	if _, err := s.run("wget", "-q", "-O", "/dev/null", worktreeLegacyAPI(old)+"/books"); err != nil {
		return err
	}
	if _, err := s.run("cmp", "-s", "/baseline-server.before", "/legacy-state/agent/postgres/server.json"); err != nil {
		return fmt.Errorf("current runtime changed the baseline credential/resource record")
	}
	e["baseline_server_record_unchanged"] = true
	e["http_probe_address"] = "explicit IPv4 loopback matches the owned listener; BusyBox wget does not fall back from IPv6 localhost"
	e["baseline_agent_pid"], e["baseline_container_id"], e["baseline_volume"] = agentPID, cid, strings.TrimSpace(string(volume))
	if err := s.nativeMigration(cid, db, before, e); err != nil {
		return err
	}
	return stop()
}

func worktreeLegacyAPI(runtime detachedDevResult) string {
	return strings.Replace(worktreeProbeAPI(runtime), "http://localhost:", "http://127.0.0.1:", 1)
}

func (s *worktreeLegacySandbox) legacyAgentPID() (int, error) {
	data, err := s.run("cat", "/legacy-state/run/agent.json")
	if err != nil {
		return 0, err
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil || state.PID <= 0 {
		return 0, fmt.Errorf("baseline did not expose its known process identity")
	}
	return state.PID, nil
}

func (s *worktreeLegacySandbox) sql(container, database, query string) ([]byte, error) {
	return s.run("docker", "exec", container, "psql", "-U", "scenery", "-d", database, "-At", "-v", "ON_ERROR_STOP=1", "-c", query)
}

const worktreeLegacyDataSQL = `SELECT jsonb_build_object('books',(SELECT jsonb_agg(to_jsonb(b) ORDER BY book_id) FROM library.books b),'seed_ledger',(SELECT jsonb_agg(to_jsonb(s) ORDER BY app_id,path) FROM scenery.seed_runs s),'reader_grant',has_table_privilege('fixture_reader','library.books','SELECT'),'owner',(SELECT tableowner FROM pg_tables WHERE schemaname='library' AND tablename='books'),'extension',(SELECT extversion FROM pg_extension WHERE extname='pgcrypto'),'framework_tables',(SELECT jsonb_agg(tablename ORDER BY tablename) FROM pg_tables WHERE schemaname='scenery'))::text;`

func (s *worktreeLegacySandbox) nativeMigration(source, database string, before []byte, e map[string]any) error {
	// Quiesce only the selected source app. A second old app keeps serving on
	// the shared source cluster throughout export, restore and target startup.
	if _, err := s.cli(true, "/app-old", "down"); err != nil {
		return err
	}
	if _, err := s.run("docker", "exec", source, "pg_dump", "-U", "scenery", "-d", database, "--format=custom", "--file=/tmp/migration.dump"); err != nil {
		return err
	}
	checksum, err := s.run("docker", "exec", source, "sha256sum", "/tmp/migration.dump")
	if err != nil {
		return err
	}
	if _, err := s.run("docker", "exec", source, "pg_restore", "--list", "/tmp/migration.dump"); err != nil {
		return err
	}
	if _, err := s.run("docker", "cp", source+":/tmp/migration.dump", "/migration.dump"); err != nil {
		return err
	}
	out, err := s.cli(false, "/app-migration", "db", "server", "start")
	if err != nil {
		return err
	}
	var status dbServerStatusResponse
	if err := decodeCLIJSON(out, &status); err != nil {
		return err
	}
	if status.Container == "" || status.Container == "scenery-postgres" {
		return fmt.Errorf("migration target did not receive its own cluster")
	}
	target := status.Container
	targetDB := postgresname.DatabaseNameFor("worktree-library", "/app-migration")
	if _, err := s.run("docker", "exec", target, "createdb", "-U", "scenery", "--owner=scenery", targetDB); err != nil {
		return err
	}
	if _, err := s.sql(target, targetDB, "CREATE ROLE fixture_reader NOLOGIN;"); err != nil {
		return err
	}
	if _, err := s.run("docker", "cp", "/migration.dump", target+":/tmp/migration.dump"); err != nil {
		return err
	}
	targetChecksum, err := s.run("docker", "exec", target, "sha256sum", "/tmp/migration.dump")
	if err != nil || !bytes.Equal(checksum, targetChecksum) {
		return fmt.Errorf("native archive checksum changed during transfer")
	}
	if _, err := s.run("docker", "exec", target, "pg_restore", "-U", "scenery", "--dbname", targetDB, "--clean", "--if-exists", "--exit-on-error", "--single-transaction", "/tmp/migration.dump"); err != nil {
		return err
	}
	after, err := s.sql(target, targetDB, worktreeLegacyDataSQL)
	if err != nil || !bytes.Equal(before, after) {
		return fmt.Errorf("native migration changed rows, ownership, grants, extension or framework schema")
	}
	runtime, err := s.up(false, "/app-migration")
	if err != nil {
		return err
	}
	if _, err := s.run("wget", "-q", "-O", "/dev/null", worktreeLegacyAPI(runtime)+"/books"); err != nil {
		return err
	}
	afterUp, err := s.sql(target, targetDB, worktreeLegacyDataSQL)
	if err != nil || !bytes.Equal(before, afterUp) {
		return fmt.Errorf("target startup changed migrated fixture data or database semantics")
	}
	sourceAfter, err := s.sql(source, database, worktreeLegacyDataSQL)
	if err != nil || !bytes.Equal(before, sourceAfter) {
		return fmt.Errorf("migration changed the retained source database")
	}
	e["migration"] = map[string]any{"source_database": database, "target_database": targetDB, "archive_sha256": strings.Fields(string(checksum))[0], "source_quiesced": true, "source_preserved": true, "owners_grants_extensions_preserved": true, "data_evidence": strings.TrimSpace(string(after)), "restore_flags": []string{"--clean", "--if-exists", "--exit-on-error", "--single-transaction"}, "role_preparation": "fixture_reader NOLOGIN recreated explicitly; existing scenery owner retained; no no-owner/no-acl flags"}
	return nil
}
