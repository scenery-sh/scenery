package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"scenery.sh/internal/sqlitedb"
)

var runHarnessSQLiteBranchCheckFunc = runHarnessSQLiteBranchCheck

func runHarnessSQLiteBranchStep(ctx context.Context, repoRoot string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "sqlite branch lifecycle",
		Command: []string{"scenery", "harness", "self", "internal:sqlite-branch-lifecycle", repoRoot},
	}
	summary, diagnostics, err := runHarnessSQLiteBranchCheckFunc(ctx)
	step.DurationMS = time.Since(started).Milliseconds()
	step.Summary = summary
	step.Diagnostics = diagnostics
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the managed SQLite branch lifecycle, then rerun `scenery harness self --json --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(diagnostics)
	return step
}

func runHarnessSQLiteBranchCheck(parent context.Context) (map[string]any, []checkDiagnostic, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
	defer cancel()
	agentHome := filepath.Join(os.TempDir(), "scenery-harness-sqlite-branch-"+harnessRandomLabel())
	appRoot := filepath.Join(agentHome, "app")
	defer os.RemoveAll(agentHome)
	restoreEnv := patchEnv(map[string]*string{
		"SCENERY_AGENT_HOME":            stringPtr(agentHome),
		"SCENERY_DEV_CACHE_DIR":         nil,
		"SCENERY_DEV_DASHBOARD_ADDR":    nil,
		"SCENERY_AGENT_DISABLE":         nil,
		"SCENERY_DEV_VICTORIA":          stringPtr("0"),
		"SCENERY_DEV_VICTORIA_DOWNLOAD": stringPtr("0"),
	})
	defer restoreEnv()
	if err := os.MkdirAll(appRoot, 0o755); err != nil {
		return nil, nil, err
	}
	config := `{
  "name": "branch-harness",
  "id": "branch-harness",
  "dev": {
    "services": {
      "main": {
        "kind": "sqlite",
        "mode": "local",
        "project": "branch-harness",
        "branch_policy": "manual",
        "database": "branch_harness.sqlite"
      }
    }
  }
}`
	if err := os.WriteFile(filepath.Join(appRoot, ".scenery.json"), []byte(config), 0o644); err != nil {
		return nil, nil, err
	}
	if err := runDBBranchCommand(ctx, ioDiscardWriter{}, []string{"checkout", "feature/a", "--app-root", appRoot, "--json"}); err != nil {
		return nil, nil, err
	}
	statusA, err := harnessSQLiteBranchStatus(ctx, appRoot)
	if err != nil {
		return nil, nil, err
	}
	if err := runDBBranchCommand(ctx, ioDiscardWriter{}, []string{"checkout", "feature/b", "--app-root", appRoot, "--json"}); err != nil {
		return nil, nil, err
	}
	statusB, err := harnessSQLiteBranchStatus(ctx, appRoot)
	if err != nil {
		return nil, nil, err
	}
	diagnostics := []checkDiagnostic{}
	check := func(ok bool, message string) {
		if ok {
			return
		}
		diagnostics = append(diagnostics, checkDiagnostic{Stage: "sqlite branch lifecycle", Severity: "error", Message: message})
	}
	check(statusA.BackendStatus == "ready", "branch feature/a must become ready")
	check(statusB.BackendStatus == "ready", "branch feature/b must become ready")
	check(statusA.Connection != nil && statusB.Connection != nil, "ready branches must expose endpoint metadata")
	if statusA.Connection != nil && statusB.Connection != nil {
		check(statusA.Connection.Database != statusB.Connection.Database, "parallel SQLite branches must use distinct databases")
	}
	if statusA.Pin != nil && statusB.Pin != nil {
		if err := harnessInsertBranchMarker(ctx, appRoot, *statusA.Pin, "feature_a"); err != nil {
			return nil, diagnostics, err
		}
		seen, err := harnessBranchMarkerExists(ctx, appRoot, *statusB.Pin)
		if err != nil {
			return nil, diagnostics, err
		}
		check(!seen, "branch feature/b must not see data written to feature/a")
	}
	if err := runDBBranchCommand(ctx, ioDiscardWriter{}, []string{"reset", "--app-root", appRoot, "--yes"}); err != nil {
		return nil, diagnostics, err
	}
	var diffOut bytes.Buffer
	if err := runDBBranchCommand(ctx, &diffOut, []string{"diff", "feature/a", "--app-root", appRoot, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	check(strings.Contains(diffOut.String(), `"schema_version": "scenery.db.branch.diff.v1"`), "db branch diff must emit branch diff JSON")
	if err := runDBBranchCommand(ctx, ioDiscardWriter{}, []string{"delete", "feature/b", "--force", "--app-root", appRoot, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	listOut := bytes.Buffer{}
	if err := runDBBranchCommand(ctx, &listOut, []string{"list", "--app-root", appRoot, "--json"}); err != nil {
		return nil, diagnostics, err
	}
	var list dbBranchListResult
	if err := json.Unmarshal(listOut.Bytes(), &list); err != nil {
		return nil, diagnostics, fmt.Errorf("decode branch list: %w", err)
	}
	check(len(list.Leases) == 1, fmt.Sprintf("branch registry should contain one lease after delete, got %d", len(list.Leases)))
	summary := map[string]any{
		"branches":       2,
		"leases_after":   len(list.Leases),
		"database_a":     "",
		"database_b":     "",
		"diagnostics":    len(diagnostics),
		"branch_backend": statusB.BackendStatus,
	}
	if statusA.Connection != nil {
		summary["database_a"] = statusA.Connection.Database
	}
	if statusB.Connection != nil {
		summary["database_b"] = statusB.Connection.Database
	}
	if hasErrorDiagnostics(diagnostics) {
		return summary, diagnostics, fmt.Errorf("SQLite branch lifecycle check failed")
	}
	return summary, diagnostics, nil
}

type ioDiscardWriter struct{}

func (ioDiscardWriter) Write(p []byte) (int, error) { return len(p), nil }

func harnessSQLiteBranchStatus(ctx context.Context, appRoot string) (dbBranchStatusResult, error) {
	var out bytes.Buffer
	if err := runDBBranchCommand(ctx, &out, []string{"status", "--app-root", appRoot, "--json"}); err != nil {
		return dbBranchStatusResult{}, err
	}
	var status dbBranchStatusResult
	if err := json.Unmarshal(out.Bytes(), &status); err != nil {
		return dbBranchStatusResult{}, fmt.Errorf("decode branch status: %w", err)
	}
	return status, nil
}

func harnessInsertBranchMarker(ctx context.Context, appRoot string, pin worktreeDBPin, marker string) error {
	return harnessWithBranchDB(ctx, appRoot, pin, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scenery_branch_marker(value text primary key)`); err != nil {
			return err
		}
		_, err := db.ExecContext(ctx, `INSERT INTO scenery_branch_marker(value) VALUES (?) ON CONFLICT DO NOTHING`, marker)
		return err
	})
}

func harnessBranchMarkerExists(ctx context.Context, appRoot string, pin worktreeDBPin) (bool, error) {
	var exists bool
	err := harnessWithBranchDB(ctx, appRoot, pin, func(db *sql.DB) error {
		if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS scenery_branch_marker(value text primary key)`); err != nil {
			return err
		}
		return db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM scenery_branch_marker)`).Scan(&exists)
	})
	return exists, err
}

func harnessWithBranchDB(ctx context.Context, appRoot string, pin worktreeDBPin, fn func(*sql.DB) error) error {
	_, cfg, err := discoverConfiguredApp(appRoot)
	if err != nil {
		return err
	}
	conn, err := (sqliteBranchProvider{cfg: cfg}).Connection(ctx, pin)
	if err != nil {
		return err
	}
	sqlitePath, err := sqlitedb.ParseURL(conn.DatabaseURL)
	if err != nil {
		return err
	}
	db, err := sqlitedb.Open(ctx, sqlitePath)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(db)
}
