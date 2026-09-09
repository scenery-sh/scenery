package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/envpolicy"
)

func runPostgresStorageCaptureExclusion(ctx context.Context, repo, root, databaseURL string, evidence map[string]any) (returnErr error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	db, err := openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	defer func() { returnErr = errors.Join(returnErr, db.Close()) }()
	var output bytes.Buffer
	shell := commandTreeContext(ctx, harnessLocalSceneryBinaryPath(repo), "db", "shell", "--app-root", root, "reports", "-c", "SELECT pg_sleep(3) /* scenery_storage_capture_probe */")
	shell.Env = envpolicy.Environ()
	shell.Stdout, shell.Stderr = &output, &output
	if err := shell.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- shell.Wait() }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("managed shell: %w: %s", err, output.String()))
		}
	}()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		var active bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS (SELECT FROM pg_stat_activity WHERE pid <> pg_backend_pid() AND state = 'active' AND query LIKE '%scenery_storage_capture_probe%')`).Scan(&active); err != nil {
			return err
		}
		if active {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	archive := filepath.Join(filepath.Dir(root), "capture-while-shell.zip")
	var capture bytes.Buffer
	err = runProduct(ctx, repo, &capture, "snapshot", "save", "--db", "--storage", "--output", archive, "--app-root", root, "-o", "json")
	if err == nil || !bytes.Contains(capture.Bytes(), []byte("SCN8003")) {
		return fmt.Errorf("combined capture did not reject active managed shell: %v: %s", err, capture.String())
	}
	if _, err := os.Lstat(archive); !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("rejected capture published an archive: %v", err)
	}
	// Let the bounded SQL statement finish normally; the deferred Wait reaps it.
	select {
	case err := <-done:
		done <- err
	case <-ctx.Done():
		return ctx.Err()
	}
	evidence["managed_shell_blocks_combined_capture_without_output"] = "passed"
	return nil
}

// Real pg_restore completes before each process cut: first before storage
// publication, then after publication but before recovery-marker removal.
// Rejected SQL-only mutation and exact resume use the normal product binary.
func runPostgresStorageRecoveryProbe(ctx context.Context, repo, root, databaseURL, archive string, evidence map[string]any) error {
	p := &worktreeRuntimeProbe{ctx: ctx, repo: repo, root: filepath.Join(filepath.Dir(root), "storage-recovery"), binary: harnessLocalSceneryBinaryPath(repo), env: envpolicy.Environ()}
	variant, control, ack, provenance, err := p.buildCheckpointVariant()
	if err != nil {
		return err
	}
	evidence["binary_provenance"] = provenance
	for _, phase := range []string{"restore-db-complete", "restore-storage-switched"} {
		checkpoint := map[string]any{}
		if err := runPostgresStorageRecoveryCheckpoint(ctx, p, root, databaseURL, archive, variant, control, ack, phase, checkpoint); err != nil {
			return fmt.Errorf("%s: %w", phase, err)
		}
		evidence[phase] = checkpoint
	}
	return nil
}

func runPostgresStorageRecoveryCheckpoint(ctx context.Context, p *worktreeRuntimeProbe, root, databaseURL, archive, variant, control, ack, phase string, evidence map[string]any) error {
	if err := os.WriteFile(control, []byte(phase), 0o600); err != nil {
		return err
	}
	if err := os.Remove(ack); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	db, err := openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	_, updateErr := db.ExecContext(ctx, `update reports.snapshot_marker set value = 'before-interrupted-restore'`)
	closeErr := db.Close()
	if updateErr != nil {
		return updateErr
	}
	if closeErr != nil {
		return closeErr
	}
	otherArchive := filepath.Join(filepath.Dir(root), phase+"-database-b.zip")
	if _, err := p.run(root, p.binary, "snapshot", "save", "--db", "--output", otherArchive, "--app-root", root, "-o", "json"); err != nil {
		return err
	}
	input := filepath.Join(filepath.Dir(root), "interrupted-input")
	if err := os.WriteFile(input, []byte("before-interrupted-restore"), 0o600); err != nil {
		return err
	}
	if _, err := p.run(root, p.binary, "storage", "put", "app", "snapshot.txt", input, "--app-root", root, "-o", "json"); err != nil {
		return err
	}
	load := []string{"snapshot", "load", "--input", archive, "--db", "--storage", "--mode", "overwrite", "--yes", "--app-root", root, "-o", "json"}
	owner, err := p.killCheckpoint(root, variant, ack, load)
	if err != nil {
		return err
	}
	db, err = openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	var value string
	readErr := db.QueryRowContext(ctx, `select value from reports.snapshot_marker`).Scan(&value)
	closeErr = db.Close()
	if readErr != nil {
		return readErr
	}
	if closeErr != nil {
		return closeErr
	}
	if value != "saved" {
		return fmt.Errorf("checkpoint preceded real database restoration: %q", value)
	}
	inspection, err := p.run(root, p.binary, "inspect", "storage", "--app-root", root, "-o", "json")
	if err != nil {
		return err
	}
	var state struct {
		Storage struct {
			Readiness string `json:"readiness"`
			Recovery  struct {
				Database      bool   `json:"database"`
				Storage       bool   `json:"storage"`
				ArchiveSHA256 string `json:"archive_sha256"`
			} `json:"recovery"`
		} `json:"storage"`
	}
	if err := decodeCLIJSON(inspection, &state); err != nil {
		return err
	}
	if state.Storage.Readiness != "recovery_required" || !state.Storage.Recovery.Database || !state.Storage.Recovery.Storage || len(state.Storage.Recovery.ArchiveSHA256) != 64 {
		return fmt.Errorf("interrupted combined restore omitted its pinned recovery projection: %s", inspection)
	}
	for _, args := range [][]string{
		{"storage", "stat", "app", "snapshot.txt"},
		{"up", "--detach", "--wait", "ready"},
		{"snapshot", "load", "--input", archive, "--storage", "--mode", "overwrite", "--yes"},
		{"snapshot", "load", "--input", otherArchive, "--db", "--mode", "overwrite", "--yes"},
		{"snapshot", "load", "--input", otherArchive, "--db", "--mode", "merge"},
	} {
		out, err := p.run(root, p.binary, append(args, "--app-root", root, "-o", "json")...)
		if err == nil || !bytes.Contains(out, []byte("SCN8008")) {
			return fmt.Errorf("combined recovery did not reject %v with its recovery diagnostic: %v", args, err)
		}
	}
	wrong := append(append([]string(nil), load...), "--expect-sha256", strings.Repeat("b", 64))
	if _, err := p.run(root, p.binary, wrong...); err == nil {
		return fmt.Errorf("mismatched archive digest resumed combined recovery")
	}
	merge := []string{"snapshot", "load", "--input", archive, "--db", "--storage", "--mode", "merge", "--yes", "--app-root", root, "-o", "json"}
	if _, err := p.run(root, p.binary, merge...); err == nil {
		return fmt.Errorf("combined merge bypassed pending recovery")
	}
	db, err = openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	readErr = db.QueryRowContext(ctx, `select value from reports.snapshot_marker`).Scan(&value)
	if err := errors.Join(readErr, db.Close()); err != nil {
		return err
	}
	if value != "saved" {
		return fmt.Errorf("rejected SQL-only load changed database before resume: %q", value)
	}
	resume := append(append([]string(nil), load...), "--expect-sha256", state.Storage.Recovery.ArchiveSHA256)
	if _, err := p.run(root, p.binary, resume...); err != nil {
		return err
	}
	if _, err := p.run(root, p.binary, "storage", "get", "app", "snapshot.txt", "--output", input, "--app-root", root, "-o", "json"); err != nil {
		return err
	}
	data, err := os.ReadFile(input)
	if err != nil {
		return err
	}
	if string(data) != "saved\n" {
		return fmt.Errorf("pinned recovery did not restore storage: %q", data)
	}
	db, err = openPostgresDatabase(ctx, databaseURL)
	if err != nil {
		return err
	}
	readErr = db.QueryRowContext(ctx, `select value from reports.snapshot_marker`).Scan(&value)
	if err := errors.Join(readErr, db.Close()); err != nil {
		return err
	}
	if value != "saved" {
		return fmt.Errorf("SQL-only load changed database during recovery: %q", value)
	}
	evidence["killed_pid"] = owner.PID
	evidence["database_restored_before_process_cut"] = true
	evidence["sql_only_overwrite_and_merge_blocked"] = true
	evidence["database_and_objects_match_after_resume"] = true
	evidence["startup_and_ordinary_access_blocked"] = true
	evidence["mismatched_resume_and_combined_merge_rejected"] = true
	evidence["pinned_production_resume"] = "passed"
	return nil
}
