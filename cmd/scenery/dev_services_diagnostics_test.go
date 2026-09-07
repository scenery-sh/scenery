package main

import (
	"bytes"
	"context"
	"errors"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

func TestPostgresContainerConflictFailsWithoutMutation(t *testing.T) {
	original := postgresDocker
	t.Cleanup(func() { postgresDocker = original })
	for _, status := range []string{"running", "exited"} {
		t.Run(status, func(t *testing.T) {
			state := &postgresServerState{Container: "scenery-postgres", Port: 5432, Password: "private-password"}
			before := *state
			fake := &fakePostgresDockerRunner{run: func(args []string) (string, error) {
				if !slices.Equal(args[:2], []string{"container", "inspect"}) {
					t.Fatalf("unexpected Docker mutation: %v", args)
				}
				if args[len(args)-1] == "{{.State.Status}}" {
					return status, nil
				}
				return "6543", nil
			}}
			postgresDocker = fake
			err := ensurePostgresDockerContainer(t.Context(), state)
			diagnostic := assertSafeRuntimeDiagnostic(t, err, 3, "SCN8003")
			if len(fake.calls) != 2 || *state != before {
				t.Fatalf("mismatch mutated state or performed extra Docker work: %+v; %v", state, fake.calls)
			}
			if diagnostic.Details["expected_port"] != float64(5432) || diagnostic.Details["configured_port"] != float64(6543) {
				t.Fatalf("missing repair context: %+v", diagnostic)
			}
			if strings.Contains(err.Error(), "docker rm") || !strings.Contains(strings.Join(diagnostic.Suggestions, " "), "Do not remove") {
				t.Fatalf("unsafe recovery guidance: %+v", diagnostic)
			}
		})
	}
}

func TestPostgresContainerMatchingPortPreservesLifecycle(t *testing.T) {
	original := postgresDocker
	t.Cleanup(func() { postgresDocker = original })
	for _, tc := range []struct {
		status, action string
		count          int
	}{
		{"running", "container", 2}, {"exited", "start", 3}, {"", "run", 2},
	} {
		t.Run(tc.status+tc.action, func(t *testing.T) {
			fake := &fakePostgresDockerRunner{run: func(args []string) (string, error) {
				if args[0] != "container" {
					return "started", nil
				}
				if args[len(args)-1] == "{{.State.Status}}" {
					if tc.status == "" {
						return "No such container: scenery-postgres", postgresDockerFailure(errors.New("exit status 1"))
					}
					return tc.status, nil
				}
				return "5432", nil
			}}
			postgresDocker = fake
			if err := ensurePostgresDockerContainer(t.Context(), &postgresServerState{Container: "scenery-postgres", Port: 5432}); err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) != tc.count || fake.calls[len(fake.calls)-1][0] != tc.action {
				t.Fatalf("lifecycle calls: %v", fake.calls)
			}
		})
	}
}

func TestPostgresDockerUnavailableDoesNotCreateContainer(t *testing.T) {
	original := postgresDocker
	t.Cleanup(func() { postgresDocker = original })
	fake := &fakePostgresDockerRunner{run: func([]string) (string, error) { return "", postgresDockerFailure(exec.ErrNotFound) }}
	postgresDocker = fake
	err := ensurePostgresDockerContainer(t.Context(), &postgresServerState{Container: "scenery-postgres", Port: 5432})
	assertSafeRuntimeDiagnostic(t, err, 4, "SCN8004")
	if len(fake.calls) != 1 || !slices.Equal(fake.calls[0][:2], []string{"container", "inspect"}) {
		t.Fatalf("unavailable Docker treated as a missing container: %v", fake.calls)
	}
}

func TestPostgresReadinessTimeoutPreservesSafeDiagnostic(t *testing.T) {
	docker, probe, sleep := postgresDocker, postgresReadyProbe, postgresReadySleep
	t.Cleanup(func() { postgresDocker, postgresReadyProbe, postgresReadySleep = docker, probe, sleep })
	postgresDocker = &fakePostgresDockerRunner{run: func([]string) (string, error) { return "5432", nil }}
	postgresReadyProbe = func(context.Context, *postgresServerState) error {
		return errors.New("connection failed postgres://user:private-password@host/db")
	}
	postgresReadySleep = func(context.Context, time.Duration) error { return context.DeadlineExceeded }
	diagnostic := assertSafeRuntimeDiagnostic(t, waitForPostgresServer(t.Context(), &postgresServerState{Container: "scenery-postgres", Port: 5432, Password: "private-password"}), 3, "SCN8003")
	if diagnostic.Details["port"] != float64(5432) || len(diagnostic.Suggestions) == 0 {
		t.Fatalf("readiness repair context: %+v", diagnostic)
	}
}

func TestRuntimeConfigurationAndCapabilityDiagnostics(t *testing.T) {
	t.Parallel()
	cfg := app.Config{Dev: app.DevConfig{Services: map[string]app.DevServiceConfig{"reports": {}}}}
	for _, tc := range []struct {
		name       string
		err        error
		code       int
		diagnostic string
	}{
		{"missing URL", validateHeadlessPostgresEnv(cfg, nil), 3, "SCN8003"},
		{"invalid URL", validateHeadlessPostgresEnv(cfg, []string{"DATABASE_URL=postgres://user:private-password@host/%ZZ"}), 3, "SCN8003"},
		{"missing Docker", postgresDockerFailure(exec.ErrNotFound), 4, "SCN8004"},
		{"Docker stderr", postgresDockerFailure(errors.New("docker run -e POSTGRES_PASSWORD=private-password: untrusted stderr")), 4, "SCN8004"},
	} {
		t.Run(tc.name, func(t *testing.T) { assertSafeRuntimeDiagnostic(t, tc.err, tc.code, tc.diagnostic) })
	}
	_, _, err := managedDatabaseEnv(t.Context(), "", cfg, nil, []string{"DATABASE_URL=postgres://user:private-password@host/%ZZ"})
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
