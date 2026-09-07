package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
)

type detachedStartupTestWriter struct {
	bytes.Buffer
	closed bool
}

func (w *detachedStartupTestWriter) Close() error {
	w.closed = true
	return nil
}

func TestDetachedStartupDiagnosticRoundTrip(t *testing.T) {
	t.Parallel()
	internal := compiler.TransportDiagnostic("internal", "raw-child-secret")
	for _, tc := range []struct {
		name       string
		code       int
		diagnostic graph.Diagnostic
	}{
		{name: "environment", code: 3, diagnostic: compiler.TransportDiagnostic("failed_precondition", "invalid .env line 1")},
		{name: "capability", code: 4, diagnostic: cliErrorDiagnostic(postgresDockerFailure(errors.New("raw-child-secret")))},
		{name: "source", code: 2, diagnostic: graph.Diagnostic{Code: "SCN1021", Severity: "error", Message: "Use app.scn", Path: "old.scn", Address: "application.demo", Suggestions: []string{"Rename the source file."}}},
		{name: "internal", code: 10, diagnostic: internal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			writer := &detachedStartupTestWriter{}
			reporter := &detachedDevStartupReporter{writer: writer}
			original := &cliDiagnosticError{err: errors.New("raw-child-secret"), code: tc.code, diagnostic: tc.diagnostic}
			wrapped := &silentCLIError{err: devBuildPhaseError{Err: original}, code: tc.code}
			if got := reporter.Report(wrapped); !errors.Is(got, wrapped) {
				t.Fatalf("report changed the existing diagnostic wrapper: %v", got)
			}
			if !writer.closed {
				t.Fatal("startup writer stayed open after reporting failure")
			}
			first := writer.String()
			_ = reporter.Report(errors.New("a later cleanup failure"))
			if writer.String() != first || strings.Contains(first, "raw-child-secret") {
				t.Fatalf("startup pipe duplicated a result or exposed raw output: %s", writer.String())
			}
			failure := readDetachedDevStartupResult(bytes.NewReader(writer.Bytes()))
			if failure == nil || cliExitCode(failure) != tc.code {
				t.Fatalf("decoded failure = %v, code %d", failure, cliExitCode(failure))
			}
			failure = detachedDevWaitFailure(failure, 123, "ready", time.Minute, "/private/startup.log")
			var output bytes.Buffer
			rendered := renderMachineError(&output, []string{"up", "--detach", "-o", "json"}, failure)
			if cliExitCode(rendered) != tc.code {
				t.Fatalf("rendered exit = %d", cliExitCode(rendered))
			}
			envelope, err := machine.Decode[graph.Diagnostic](output.Bytes(), currentMachineSpecRevision())
			if err != nil {
				t.Fatal(err)
			}
			if len(envelope.Diagnostics) != 1 {
				t.Fatalf("diagnostics = %+v", envelope.Diagnostics)
			}
			got := envelope.Diagnostics[0]
			if got.Code != tc.diagnostic.Code || got.Message != tc.diagnostic.Message || got.ReportToken != tc.diagnostic.ReportToken || got.Path != tc.diagnostic.Path || got.Address != tc.diagnostic.Address {
				t.Fatalf("diagnostic changed across startup: %+v, want %+v", got, tc.diagnostic)
			}
			if len(got.Suggestions) != len(tc.diagnostic.Suggestions)+1 {
				t.Fatalf("suggestions lost: %+v", got.Suggestions)
			}
			startup := got.Details["detached_startup"].(map[string]any)
			if startup["reason"] != "child_failure" || startup["log_path"] != "/private/startup.log" {
				t.Fatalf("startup context = %+v", startup)
			}
			if strings.Contains(output.String(), "raw-child-secret") || strings.Contains(failure.Error(), "raw-child-secret") || !strings.Contains(failure.Error(), "/private/startup.log") {
				t.Fatalf("unsafe or unhelpful public failure: %v; %s", failure, output.String())
			}
		})
	}
}

func TestDetachedStartupRejectsInvalidResults(t *testing.T) {
	t.Parallel()
	var valid bytes.Buffer
	result := detachedDevStartupFailure{ExitCode: 3, Diagnostic: compiler.TransportDiagnostic("failed_precondition", "startup precondition failed")}
	if err := newCLIEventWriter(&valid).write("summary", true, result); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{name: "producer", mutate: func(v map[string]any) { v["producer"].(map[string]any)["version"] = "foreign" }},
		{name: "schema", mutate: func(v map[string]any) { v["schema_revision"] = "invalid" }},
		{name: "nonterminal", mutate: func(v map[string]any) { v["event"], v["terminal"] = "event", false }},
		{name: "missing exit", mutate: func(v map[string]any) { delete(v["data"].(map[string]any), "exit_code") }},
		{name: "successful failure", mutate: func(v map[string]any) { v["data"].(map[string]any)["ok"] = true }},
		{name: "raw output field", mutate: func(v map[string]any) { v["data"].(map[string]any)["error"] = "raw-child-secret" }},
		{name: "unknown code", mutate: func(v map[string]any) { v["data"].(map[string]any)["diagnostic"].(map[string]any)["code"] = "SCN8999" }},
		{name: "raw internal message", mutate: func(v map[string]any) {
			d := v["data"].(map[string]any)["diagnostic"].(map[string]any)
			d["code"], d["message"], d["report_token"] = "SCN9000", "raw-child-secret", "rpt_abc"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var v map[string]any
			if err := json.Unmarshal(valid.Bytes(), &v); err != nil {
				t.Fatal(err)
			}
			tc.mutate(v)
			encoded, err := json.Marshal(v)
			if err != nil {
				t.Fatal(err)
			}
			assertDetachedStartupProtocolFailure(t, encoded)
		})
	}
	assertDetachedStartupProtocolFailure(t, []byte("human stderr: raw-child-secret\n"))
	assertDetachedStartupProtocolFailure(t, bytes.Repeat([]byte("x"), detachedDevStartupResultLimit+1))
	if err := readDetachedDevStartupResult(strings.NewReader("")); err != nil {
		t.Fatalf("empty startup pipe should leave readiness/exit authoritative: %v", err)
	}
}

func assertDetachedStartupProtocolFailure(t *testing.T, encoded []byte) {
	t.Helper()
	err := readDetachedDevStartupResult(bytes.NewReader(encoded))
	reported, ok := errors.AsType[*cliDiagnosticError](err)
	if !ok || reported.code != 3 || reported.startupReason != "protocol_error" || strings.Contains(err.Error(), "raw-child-secret") {
		t.Fatalf("invalid startup result error = %v", err)
	}
}

func TestDetachedStartupFailureCancelsSessionPolling(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	startup := make(chan error)
	exited := make(chan error)
	monitorDone := make(chan struct{})
	go func() {
		monitorDetachedDevStartup(ctx, cancel, startup, exited)
		close(monitorDone)
	}()
	want := &cliDiagnosticError{code: 3, diagnostic: compiler.TransportDiagnostic("failed_precondition", "invalid .env line 1")}
	listed := make(chan struct{})
	go func() { <-listed; startup <- want }()
	_, err := waitForDetachedDevSessionWithLister(ctx, func(ctx context.Context, _ string) ([]localagent.Session, error) {
		close(listed)
		<-ctx.Done()
		return nil, ctx.Err()
	}, "/removed-session", 123, "ready", nil)
	<-monitorDone
	if !errors.Is(err, context.Canceled) || !errors.Is(context.Cause(ctx), want) {
		t.Fatalf("poll result = %v, cause = %v", err, context.Cause(ctx))
	}
}

func TestDetachedStartupExitWaitsForBufferedDiagnostic(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	startup := make(chan error)
	exited := make(chan error)
	done := make(chan struct{})
	go func() { monitorDetachedDevStartup(ctx, cancel, startup, exited); close(done) }()
	exited <- errors.New("exit before the result reader finished")
	if ctx.Err() != nil {
		t.Fatal("exit erased the pending startup result")
	}
	want := &cliDiagnosticError{code: 3, diagnostic: compiler.TransportDiagnostic("failed_precondition", "invalid .env line 1")}
	startup <- want
	<-done
	if !errors.Is(context.Cause(ctx), want) {
		t.Fatalf("exit won over the structured result: %v", context.Cause(ctx))
	}
}

func TestDetachedStartupDistinguishesExitTimeoutAndReadiness(t *testing.T) {
	t.Parallel()
	for _, testExit := range []bool{false, true} {
		t.Run(fmt.Sprintf("exit=%v", testExit), func(t *testing.T) {
			parent, stopParent := context.WithCancelCause(context.Background())
			defer stopParent(nil)
			ctx, cancel := context.WithCancelCause(parent)
			defer cancel(nil)
			startup := make(chan error)
			exited := make(chan error)
			done := make(chan struct{})
			go func() { monitorDetachedDevStartup(ctx, cancel, startup, exited); close(done) }()
			startup <- nil // Successful readiness closes the pipe without a failure.
			if ctx.Err() != nil {
				t.Fatal("pipe EOF was treated as a startup failure")
			}
			wantReason := "timeout"
			if testExit {
				wantReason = "child_exit"
				exited <- nil
			} else {
				stopParent(context.DeadlineExceeded)
			}
			<-done
			err := detachedDevWaitFailure(context.Cause(ctx), 123, "ready", time.Minute, "/startup.log")
			diagnostic := cliErrorDiagnostic(err)
			if cliExitCode(err) != 3 || diagnostic.Code != "SCN8003" || diagnostic.Details["detached_startup"].(map[string]any)["reason"] != wantReason {
				t.Fatalf("failure classification = %v, %+v", err, diagnostic)
			}
		})
	}
}

func TestDetachedStartupReportingAfterParentDetachDoesNotReplaceFailure(t *testing.T) {
	t.Parallel()
	reporter := &detachedDevStartupReporter{writer: detachedStartupBrokenWriter{}}
	want := &codedCLIError{err: errors.New("invalid .env line 1"), code: 3}
	got := reporter.Report(want)
	if !errors.Is(got, want) || cliExitCode(got) != 3 {
		t.Fatalf("broken startup pipe replaced the original failure: %v", got)
	}
}

type detachedStartupBrokenWriter struct{}

func (detachedStartupBrokenWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (detachedStartupBrokenWriter) Close() error              { return nil }
