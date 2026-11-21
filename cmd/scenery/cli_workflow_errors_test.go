package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/machine"
)

// Routine workflow failures name what the caller must change. None of them
// is an internal failure: each keeps its message, carries an SCN8000-range
// code and mints no failure report. The test installs the process-wide
// report sink, so it does not run in parallel.
func TestRoutineWorkflowErrorsAreNotInternalFailures(t *testing.T) {
	var minted []machine.InternalFailure
	machine.SetInternalFailureSink(func(failure machine.InternalFailure) { minted = append(minted, failure) })
	t.Cleanup(func() { machine.SetInternalFailureSink(nil) })

	for _, failure := range []struct {
		name       string
		err        error
		exit       int
		code       string
		message    string
		suggestion string
	}{
		{"changed applied seed", devBuildOperationError{err: &changedAppliedSeedError{Path: "projects/db/seed.sql"}}, 3, "SCN8003", "restore the applied seed", ""},
		{"missing app.scn", fmt.Errorf("prepare: %w", &compiler.MissingAppError{Root: "/work/app"}), 2, "SCN8001", "/work/app does not contain app.scn", "--app-root"},
		{"superseded generation", devBuildOperationError{err: errBuildSuperseded}, 3, "SCN8002", "discard the superseded generation and retry", ""},
		{"pending migration", &pendingMigrationError{Service: "projects"}, 3, "SCN8003", "run scenery down, scenery db migrate, then scenery up", ""},
		{"frontend command not found", &frontendExitError{err: errors.New("frontend web exited before becoming ready: exit status 127\nvite: command not found"), commandNotFound: true}, 3, "SCN8003", "vite: command not found", "Install the frontend's package dependencies"},
		{"frontend exited", &frontendExitError{err: errors.New("frontend web exited before becoming ready: exit status 1")}, 3, "SCN8003", "exit status 1", "Fix the frontend's dev command"},
		{"compiler capture unavailable", fmt.Errorf("prepare candidate: %w", &build.PreconditionError{Message: "captured compiler input set is unavailable"}), 3, "SCN8003", "captured compiler input set is unavailable", ""},
		{"framework selection mismatch", fmt.Errorf("verify: %w", &build.FrameworkMismatchError{Selected: "sha256:a", Producer: "sha256:b"}), 3, "SCN8003", "run scenery framework use", ""},
	} {
		reported := preserveCLIDiagnostic(failure.err)
		diagnostic := cliErrorDiagnostic(reported)
		if exit := cliExitCode(reported); exit != failure.exit || diagnostic.Code != failure.code || diagnostic.ReportToken != "" || !strings.Contains(diagnostic.Message, failure.message) {
			t.Errorf("%s: exit %d, %+v; want exit %d, %s naming %q", failure.name, exit, diagnostic, failure.exit, failure.code, failure.message)
		}
		if failure.suggestion != "" && (len(diagnostic.Suggestions) != 1 || !strings.Contains(diagnostic.Suggestions[0], failure.suggestion)) {
			t.Errorf("%s: suggestions %q; want one naming %q", failure.name, diagnostic.Suggestions, failure.suggestion)
		}
	}
	if len(minted) != 0 {
		t.Fatalf("routine failures minted internal failure reports: %+v", minted)
	}
}

// Migration sources the app authored decide before any database work whether
// a migration can run; a refused statement is the app's to remove.
func TestRefusedMigrationStatementIsAPrecondition(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	directory := filepath.Join(root, "reports/db/migrations")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "0001_seed.sql"), []byte("SET search_path = reports;\nCREATE TABLE notes(id int);"), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{Database: app.DatabaseConfig{Migrations: []app.DatabaseMigrationConfig{{Service: "reports", Directory: "reports/db/migrations"}}}}
	_, err := discoverDBMigrationPlans(root, cfg, testSQLRequirements(t, "reports"))
	diagnostic := cliErrorDiagnostic(err)
	if cliExitCode(err) != 3 || diagnostic.Code != "SCN8003" || !strings.Contains(diagnostic.Message, `migration reports/db/migrations/0001_seed.sql: unsupported migration statement "SET"`) || !strings.Contains(diagnostic.Message, "remove it from the file") {
		t.Fatalf("exit %d, %+v; want SCN8003 naming the file, statement and remedy", cliExitCode(err), diagnostic)
	}
}
