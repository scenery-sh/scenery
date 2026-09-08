package main

import (
	"bytes"
	"fmt"
	"path/filepath"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/postgresdb"
	"scenery.sh/internal/postgresname"
)

func (p *worktreeRuntimeProbe) inertSnapshot(source string) error {
	return p.scenario("A14", "inert verified snapshot preserves exact rows and failed restore blocks readiness", func(e map[string]any) error {
		archive := filepath.Join(p.root, "faithful-library.zip")
		if _, err := p.run(source, p.binary, "snapshot", "save", "--output", archive, "--db", "--app-root", source, "-o", "json"); err != nil {
			return err
		}
		if _, err := p.run(source, p.binary, "snapshot", "verify", "--input", archive, "-o", "json"); err != nil {
			return err
		}
		expected, err := p.libraryRows(source)
		if err != nil {
			return err
		}
		root := filepath.Join(p.root, "snapshot-target")
		if _, err := p.run(source, "git", "worktree", "add", "--quiet", "-b", "probe-snapshot", root); err != nil {
			return err
		}
		p.roots = append(p.roots, root)
		if _, err := p.run(root, p.binary, "snapshot", "load", "--input", archive, "--db", "--mode", "overwrite", "--yes", "--app-root", root, "-o", "json"); err != nil {
			return err
		}
		paths, err := localagent.PathsForWorktree(p.home, root)
		if err != nil {
			return err
		}
		held, err := paths.ProbeLiveLock()
		if err != nil || held {
			return fmt.Errorf("inert restore left a live runtime owner: %v", err)
		}
		actual, err := p.libraryRows(root)
		if err != nil || actual != expected {
			return fmt.Errorf("inert restore changed source rows: %v", err)
		}
		record, err := p.record(root)
		if err != nil || record.Postgres.Restore != nil || record.Postgres.Phase != "ready" {
			return fmt.Errorf("successful inert restore retained an incomplete marker: %v", err)
		}
		// Loading the same primary keys in merge mode fails in real pg_restore,
		// exercising the SQL failure boundary after the archive was verified.
		if _, err := p.run(root, p.binary, "snapshot", "load", "--input", archive, "--db", "--mode", "merge", "--yes", "--app-root", root, "-o", "json"); err == nil {
			return fmt.Errorf("duplicate-key merge unexpectedly succeeded")
		}
		failed, err := p.record(root)
		if err != nil || failed.Postgres.Restore == nil || failed.Postgres.Phase != "restore-failed" {
			return fmt.Errorf("failed SQL restore did not retain its failure marker: %v", err)
		}
		output, rejected := p.run(root, p.binary, "up", "--app-root", root, "--detach", "--wait", "ready", "-o", "json")
		if rejected == nil || !bytes.Contains(output, []byte("SCN8003")) {
			return fmt.Errorf("ordinary startup reported readiness after failed restore")
		}
		actual, err = p.libraryRows(root)
		if err != nil || actual != expected {
			return fmt.Errorf("failed transactional merge changed restored rows: %v", err)
		}
		e["exact_rows_preserved"], e["no_runtime_started"], e["failed_restore_blocks_ready"] = true, true, true
		e["archive"] = archive
		return nil
	})
}

func (p *worktreeRuntimeProbe) libraryRows(root string) (string, error) {
	record, err := p.record(root)
	if err != nil {
		return "", err
	}
	database, err := postgresdb.Open(p.ctx, worktreePostgresURL(record.Postgres, postgresname.DatabaseNameFor(record.AppID, record.AppRoot)))
	if err != nil {
		return "", fmt.Errorf("open probe database for native row comparison")
	}
	defer func() { _ = database.Close() }()
	var rows string
	err = database.QueryRowContext(p.ctx, `SELECT COALESCE(jsonb_agg(to_jsonb(b) ORDER BY book_id), '[]'::jsonb)::text FROM library.books b`).Scan(&rows)
	return rows, err
}
