package main

import (
	"bytes"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestRuntimeConfigurationAndCapabilityDiagnostics(t *testing.T) {
	t.Parallel()
	cfg := app.Config{}
	requirements := testSQLRequirements(t, "reports")
	for _, tc := range []struct {
		name       string
		err        error
		code       int
		diagnostic string
	}{
		{"missing URL", validateHeadlessPostgresEnv(requirements, nil), 3, "SCN8003"},
		{"invalid URL", validateHeadlessPostgresEnv(requirements, []string{"DATABASE_URL=postgres://user:private-password@host/%ZZ"}), 3, "SCN8003"},
		{"missing Docker", postgresDockerFailure(exec.ErrNotFound), 4, "SCN8004"},
		{"Docker stderr", postgresDockerFailure(errors.New("docker run -e POSTGRES_PASSWORD=private-password: untrusted stderr")), 4, "SCN8004"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertSafeRuntimeDiagnostic(t, tc.err, tc.code, tc.diagnostic) })
	}
	_, _, err := managedDatabaseEnv(t.Context(), "", cfg, requirements, []string{"DATABASE_URL=postgres://user:private-password@host/%ZZ"})
	assertSafeRuntimeDiagnostic(t, err, 3, "SCN8003")
	_, err = devRoutingMode(app.ResolvedEnv{Name: "local", Mode: "unsupported"})
	assertSafeRuntimeDiagnostic(t, err, 3, "SCN8003")
	_, err = devExposeRouteNames(cfg, app.ResolvedEnv{Name: "local", Expose: []string{"api"}})
	assertSafeRuntimeDiagnostic(t, err, 3, "SCN8003")
}

func assertSafeRuntimeDiagnostic(t *testing.T, err error, code int, diagnosticCode string) graph.Diagnostic {
	t.Helper()
	if err == nil || cliExitCode(err) != code {
		t.Fatalf("error = %v, exit = %d, want %d", err, cliExitCode(err), code)
	}
	var output bytes.Buffer
	if rendered := renderMachineError(&output, []string{"up", "--detach", "-o", "json"}, err); cliExitCode(rendered) != code {
		t.Fatalf("rendering changed exit classification: %v", rendered)
	}
	envelope, decodeErr := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != diagnosticCode {
		t.Fatalf("public diagnostic = %s", output.String())
	}
	if strings.Contains(output.String(), "private-password") || strings.Contains(err.Error(), "private-password") || strings.Contains(output.String(), "untrusted stderr") {
		t.Fatalf("raw credential/output escaped: %s; %v", output.String(), err)
	}
	return envelope.Diagnostics[0]
}
