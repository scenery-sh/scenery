package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"scenery.sh/internal/evolution"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/spec"
)

func TestCLIExitStatusMatchesEdition2027Contract(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, 0},
		{&silentCLIError{err: errors.New("predicate false")}, 1},
		{errors.New("invalid_request: malformed change"), 2},
		{errors.New("revision_conflict: stale graph"), 3},
		{errors.New("failed_precondition: stale plan"), 3},
		{errors.New("capability_unavailable: provider missing"), 4},
		{errors.New("permission_denied: approval missing"), 5},
		{errors.New("internal: compiler panic"), 10},
		{errors.New("usage: scenery get ADDRESS"), 2},
		{errors.New("provider log mentioned permission_denied"), 10},
	}
	for _, test := range tests {
		if got := cliExitCode(test.err); got != test.want {
			t.Errorf("cliExitCode(%v) = %d, want %d", test.err, got, test.want)
		}
	}
}

func TestCLIProcessExitStatusMatchesEdition2027Contract(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		want        int
		wantCommand string
	}{
		{name: "success", args: nil, want: 0, wantCommand: "help"},
		{name: "invalid usage", args: []string{"not-a-command"}, want: 2, wantCommand: "not-a-command"},
		{name: "missing resource", args: []string{"get", "missing/operation/nope", "--app-root", contractFixtureRoot(t)}, want: 2, wantCommand: "get"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			home := t.TempDir()
			args := []string{"-test.run=^TestCLIProcessHelper$", "--"}
			args = append(args, test.args...)
			command := exec.Command(os.Args[0], args...)
			command.Env = append(os.Environ(), "SCENERY_TEST_CLI_PROCESS=1", "HOME="+home)
			err := command.Run()
			got := 0
			if exitError, ok := err.(*exec.ExitError); ok {
				got = exitError.ExitCode()
			} else if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("exit code = %d, want %d", got, test.want)
			}
			encoded, err := os.ReadFile(filepath.Join(home, ".scenery", "telemetry.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			var record cliTelemetryRecord
			if err := json.Unmarshal(encoded, &record); err != nil {
				t.Fatal(err)
			}
			if record.Command != test.wantCommand || record.ExitCode != test.want {
				t.Fatalf("telemetry = %#v, want command %q and exit code %d", record, test.wantCommand, test.want)
			}
		})
	}
}

func TestCLIProcessHelper(t *testing.T) {
	if os.Getenv("SCENERY_TEST_CLI_PROCESS") != "1" {
		return
	}
	separator := -1
	for index, argument := range os.Args {
		if argument == "--" {
			separator = index
			break
		}
	}
	if separator < 0 {
		os.Exit(97)
	}
	os.Args = append([]string{"scenery"}, os.Args[separator+1:]...)
	main()
}

func TestContractJSONEnvelopeHasStableFields(t *testing.T) {
	var output strings.Builder
	err := runContractCompile(&output, []string{"--app-root", contractFixtureRoot(t), "-o", "json", "--non-interactive", "--quiet"})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(output.String()), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"kind", "schema_revision", "spec_revision", "producer", "ok", "workspace_revision", "contract_revision", "implementation_revision", "deployment_revision", "data", "diagnostics"} {
		if _, ok := envelope[field]; !ok {
			t.Errorf("missing stable envelope field %q in %s", field, output.String())
		}
	}
	if envelope["kind"] != machine.EnvelopeKind || envelope["schema_revision"] != machine.EnvelopeSchemaRevision {
		t.Fatalf("machine identity = %v %v", envelope["kind"], envelope["schema_revision"])
	}
}

func TestContractCheckJSONReportsValidNativeImplementation(t *testing.T) {
	root := filepath.Join(filepath.Dir(contractFixtureRoot(t)), "native")
	var output strings.Builder
	if err := runContractCheck(&output, []string{"--app-root", root, "-o", "json", "--non-interactive", "--quiet"}); err != nil {
		t.Fatal(err)
	}
	envelope, err := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
	if err != nil {
		t.Fatal(err)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok {
		t.Fatalf("data = %#v", envelope.Data)
	}
	if got := data["implementation_status"]; got != "valid" {
		t.Fatalf("implementation_status = %#v, want valid", got)
	}
	if _, ok := data["manifest"]; ok {
		t.Fatal("check output must not embed the full manifest; the graph belongs to compile/list/get")
	}
	summary, ok := data["manifest_summary"].(map[string]any)
	if !ok {
		t.Fatalf("manifest_summary = %#v", data["manifest_summary"])
	}
	resources, ok := summary["resources"].(float64)
	if !ok || resources <= 0 {
		t.Fatalf("manifest_summary resources = %#v, want > 0", summary["resources"])
	}
	if _, ok := summary["resources_by_kind"].(map[string]any); !ok {
		t.Fatalf("manifest_summary resources_by_kind = %#v", summary["resources_by_kind"])
	}
}

func TestCLIJSONWrapsCommandData(t *testing.T) {
	var output strings.Builder
	if err := writeCLIJSON(&output, map[string]any{"schema_version": "example.v1"}); err != nil {
		t.Fatal(err)
	}
	envelope, err := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
	if err != nil {
		t.Fatal(err)
	}
	if envelope.Kind != machine.EnvelopeKind || envelope.SchemaRevision != machine.EnvelopeSchemaRevision || !envelope.OK {
		t.Fatalf("envelope = %#v", envelope)
	}
	data, ok := envelope.Data.(map[string]any)
	if !ok || data["schema_version"] != "example.v1" {
		t.Fatalf("data = %#v", envelope.Data)
	}
}

func TestCLIJSONLSequencesEventsAndTerminates(t *testing.T) {
	var output strings.Builder
	events := newCLIEventWriter(&output)
	if err := events.event(map[string]any{"message": "one"}); err != nil {
		t.Fatal(err)
	}
	if err := events.summary(1); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("event lines = %d, want 2", len(lines))
	}
	first, err := machine.DecodeEvent[graph.Diagnostic]([]byte(lines[0]), currentMachineSpecRevision())
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := machine.DecodeEvent[graph.Diagnostic]([]byte(lines[1]), currentMachineSpecRevision())
	if err != nil {
		t.Fatal(err)
	}
	if first.Kind != machine.EventEnvelopeKind || first.SchemaRevision != machine.EventEnvelopeSchemaRevision || first.Sequence != 1 || first.Terminal {
		t.Fatalf("first = %#v", first)
	}
	if terminal.Sequence != 2 || terminal.Event != "summary" || !terminal.Terminal {
		t.Fatalf("terminal = %#v", terminal)
	}
}

func TestMachineEnvelopeSchemasMatchConstructors(t *testing.T) {
	root := repoRootForTest(t)
	specRevision := currentMachineSpecRevision()
	producer := cliProducer()
	for path, payload := range map[string]any{
		"scenery.cli.schema.json":       machine.NewEnvelope[graph.Diagnostic](specRevision, producer, true, map[string]any{"fixture": true}, nil),
		"scenery.cli.event.schema.json": machine.NewEventEnvelope[graph.Diagnostic](specRevision, producer, 1, "summary", true, map[string]any{"event_count": 0}, nil),
	} {
		if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(root, "docs", "schemas", path), payload); len(diagnostics) != 0 {
			t.Fatalf("%s diagnostics = %v", path, diagnostics)
		}
	}
}

func TestContractSchemaPublishesDiagnosticCatalog(t *testing.T) {
	var output strings.Builder
	if err := runContractSchema(&output, []string{graph.DiagnosticCatalog, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	envelope, err := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
	if err != nil {
		t.Fatal(err)
	}
	catalog, ok := envelope.Data.(map[string]any)
	if !ok || catalog["type"] != "diagnostic_catalog" {
		t.Fatalf("catalog = %#v", envelope.Data)
	}
	found := false
	for _, value := range catalog["definitions"].([]any) {
		definition, _ := value.(map[string]any)
		if definition["code"] == "SCN8001" && definition["identity"] == "invalid_request" {
			found = true
		}
	}
	if !found {
		t.Fatalf("catalog definitions = %#v", catalog["definitions"])
	}
}

func TestContractDiffConsumesRenameReceiptFile(t *testing.T) {
	root := t.TempDir()
	before := graph.Resource{Address: "house/record/old", Kind: "scenery.record", Module: "house", Name: "old", Spec: map[string]any{}}
	after := before
	after.Address, after.Name = "house/record/new", "new"
	newManifest := func(resource graph.Resource) *graph.Manifest {
		t.Helper()
		manifest := &graph.Manifest{
			Kind: graph.ManifestKind, SchemaRevision: graph.ManifestSchemaRevision, SpecRevision: string(spec.CurrentRevision()),
			Producer: machine.RuntimeProducer(), DiagnosticCatalog: graph.DiagnosticCatalog,
			Application: graph.ApplicationIdentity{Name: "app"}, Resources: []graph.Resource{resource},
			SourceMap: map[string]graph.SourceRecord{}, Diagnostics: []graph.Diagnostic{},
		}
		var revisionErr error
		manifest.ContractRevision, revisionErr = graph.ContractRevision(manifest.Resources, manifest.Application.Name)
		if revisionErr != nil {
			t.Fatal(revisionErr)
		}
		return manifest
	}
	base := newManifest(before)
	target := newManifest(after)
	receipt := evolution.RenameReceipt{From: before.Address, To: after.Address, BaseContractRevision: base.ContractRevision, TargetContractRevision: target.ContractRevision}
	canonical, err := spec.MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("scenery.rename-receipt\x00"), canonical...))
	receipt.Digest = "sha256:" + hex.EncodeToString(digest[:])
	writeJSON := func(name string, value any) string {
		t.Helper()
		path := filepath.Join(root, name)
		encoded, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		if writeErr := os.WriteFile(path, encoded, 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
		return path
	}
	basePath := writeJSON("base.json", base)
	targetPath := writeJSON("target.json", target)
	receiptPath := writeJSON("receipt.json", map[string]any{"rename_receipts": []evolution.RenameReceipt{receipt}})
	var output strings.Builder
	if err := runContractDiff(&output, []string{"--semantic", basePath, targetPath, "--rename-receipts", receiptPath, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var diff evolution.SemanticDiff
	err = machine.DecodeData[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision(), &diff)
	if err != nil || len(diff.Changes) != 1 || diff.Changes[0].Operation != "rename" {
		t.Fatalf("diff output = %s, %v", output.String(), err)
	}
}

func TestContractJSONFailureWritesExactlyOneStableEnvelope(t *testing.T) {
	var output strings.Builder
	err := renderMachineError(&output, []string{"migrate", "activate", "missing", "-o", "json"}, errors.New("failed_precondition: candidate unavailable"))
	if err == nil || cliExitCode(err) != 3 {
		t.Fatalf("render error = %v, code %d", err, cliExitCode(err))
	}
	envelope, decodeErr := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" {
		t.Fatalf("failure envelope = %#v", envelope)
	}
}

func TestContractJSONFailureCodesMatchTransportErrorKinds(t *testing.T) {
	tests := []struct {
		err        error
		wantCode   string
		wantReport bool
	}{
		{err: errors.New("invalid_request: malformed request"), wantCode: "SCN8001"},
		{err: errors.New("revision_conflict: stale graph"), wantCode: "SCN8002"},
		{err: errors.New("failed_precondition: stale plan"), wantCode: "SCN8003"},
		{err: errors.New("capability_unavailable: provider missing"), wantCode: "SCN8004"},
		{err: errors.New("permission_denied: approval missing"), wantCode: "SCN8005"},
		{err: errors.New("internal: compiler invariant"), wantCode: "SCN9000", wantReport: true},
	}
	for _, test := range tests {
		var output strings.Builder
		err := renderMachineError(&output, []string{"compile", "-o", "json"}, test.err)
		if err == nil {
			t.Fatalf("renderMachineError(%v) returned nil", test.err)
		}
		envelope, decodeErr := machine.Decode[graph.Diagnostic]([]byte(output.String()), currentMachineSpecRevision())
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != test.wantCode {
			t.Fatalf("%v diagnostic = %#v, want %s", test.err, envelope.Diagnostics, test.wantCode)
		}
		reportToken := envelope.Diagnostics[0].ReportToken
		if test.wantReport != strings.HasPrefix(reportToken, "rpt_") {
			t.Fatalf("%v report_token = %q", test.err, reportToken)
		}
		if test.wantReport && strings.Contains(envelope.Diagnostics[0].Message, "compiler invariant") {
			t.Fatalf("internal cause leaked in %#v", envelope.Diagnostics[0])
		}
	}
}

func TestContractQuietSuppressesHumanOutput(t *testing.T) {
	var output strings.Builder
	if err := runContractCompile(&output, []string{"--app-root", contractFixtureRoot(t), "--quiet"}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("quiet output = %q", output.String())
	}
}

func contractFixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "internal", "compiler", "testdata", "house")
}

func TestResolveAppRoot(t *testing.T) {
	t.Parallel()

	if got, err := resolveAppRoot(""); err != nil || got != "." {
		t.Fatalf("resolveAppRoot(\"\") = %q, %v; want \".\", nil", got, err)
	}

	root := t.TempDir()
	got, err := resolveAppRoot(root)
	if err != nil {
		t.Fatalf("resolveAppRoot returned error: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("resolveAppRoot(%q) = %q, want %q", root, got, filepath.Clean(root))
	}
}

func TestDevLegacyProxySurfaceRejected(t *testing.T) {
	t.Parallel()

	for _, flag := range []string{"--proxy", "--trust", "-p", "-v"} {
		if _, err := parseDevArgs([]string{flag}); err == nil || !strings.Contains(err.Error(), `unknown flag "`+flag+`"`) {
			t.Fatalf("parseDevArgs(%s) error = %v, want unknown flag", flag, err)
		}
	}
}

func TestDevOutputMatchesExecutionMode(t *testing.T) {
	t.Parallel()

	live, err := parseDevArgs([]string{"-o", "jsonl"})
	if err != nil || !live.JSON || live.Output != "jsonl" {
		t.Fatalf("live output = %+v, %v", live, err)
	}
	detached, err := parseDevArgs([]string{"--detach", "-o", "json"})
	if err != nil || !detached.JSON || detached.Output != "json" {
		t.Fatalf("detached output = %+v, %v", detached, err)
	}
	if _, err := parseDevArgs([]string{"-o", "json"}); err == nil || !strings.Contains(err.Error(), "use -o jsonl") {
		t.Fatalf("live json error = %v", err)
	}
	if _, err := parseDevArgs([]string{"--detach", "-o", "jsonl"}); err == nil || !strings.Contains(err.Error(), "use -o json") {
		t.Fatalf("detached jsonl error = %v", err)
	}
}

func TestUpCommandDelegatesValidationToWatchLoop(t *testing.T) {
	original := runWithWatchFunc
	t.Cleanup(func() { runWithWatchFunc = original })
	called := false
	runWithWatchFunc = func(_ devListenRequest, _ bool, _ bool, _ bool, _, _ string) error {
		called = true
		return nil
	}
	if err := upCommand([]string{"--app-root", filepath.Join(t.TempDir(), "missing")}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("up did not delegate validation to the watch loop")
	}
}

func TestLegacyLocalProxyEnvRemovedFromProductionSource(t *testing.T) {
	t.Parallel()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	repoRoot := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	banned := "SCENERY_LOCAL_" + "PROXY"
	var hits []string
	for _, dir := range []string{"cmd/scenery", "internal"} {
		root := filepath.Join(repoRoot, dir)
		if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			if filepath.Ext(path) != ".go" || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if strings.Contains(string(data), banned) {
				rel, _ := filepath.Rel(repoRoot, path)
				hits = append(hits, filepath.ToSlash(rel))
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(hits) > 0 {
		t.Fatalf("%s remains in production source: %s", banned, strings.Join(hits, ", "))
	}
}
