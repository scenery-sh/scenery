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

	"scenery.sh/internal/vnext"
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

func TestCLIAPIVersionSelectorsAreExactAndConflictSafe(t *testing.T) {
	args, version, err := normalizeCLIAPIVersion([]string{"check", "--api-version", "scenery.cli.v1"})
	if err != nil || version != "scenery.cli.v1" || strings.Join(args, " ") != "check" {
		t.Fatalf("v1 selector = %#v %q %v", args, version, err)
	}
	args, version, err = normalizeCLIAPIVersion([]string{"check", "--api-version=scenery.cli.v0", "--json"})
	if err != nil || version != "scenery.cli.v0" || strings.Join(args, " ") != "check --json" {
		t.Fatalf("v0 selector = %#v %q %v", args, version, err)
	}
	args, version, err = normalizeCLIAPIVersion([]string{"storage", "get", "app", "key", "--output", "result.json", "--json"})
	if err != nil || version != "" || strings.Join(args, " ") != "storage get app key --output result.json --json" {
		t.Fatalf("v0 file output = %#v %q %v", args, version, err)
	}
	args, version, err = normalizeCLIAPIVersion([]string{"build", "-o", "scenery-app"})
	if err != nil || version != "" || strings.Join(args, " ") != "build -o scenery-app" {
		t.Fatalf("v0 build output = %#v %q %v", args, version, err)
	}
	for _, input := range [][]string{
		{"check", "--json", "-o", "json"},
		{"check", "--api-version", "scenery.cli.v0", "-o", "json"},
		{"check", "--api-version", "scenery.cli.v1", "--json"},
		{"check", "--api-version", "scenery.cli.v2"},
	} {
		if _, _, err := normalizeCLIAPIVersion(input); err == nil {
			t.Fatalf("conflicting selector accepted: %#v", input)
		}
	}
}

func TestCLIProcessExitStatusMatchesEdition2027Contract(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "success", args: nil, want: 0},
		{name: "invalid usage", args: []string{"not-a-command"}, want: 2},
		{name: "missing resource", args: []string{"get", "missing/operation/nope", "--app-root", vnextFixtureRoot(t)}, want: 2},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{"-test.run=^TestCLIProcessHelper$", "--"}
			args = append(args, test.args...)
			command := exec.Command(os.Args[0], args...)
			command.Env = append(os.Environ(), "SCENERY_TEST_CLI_PROCESS=1")
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

func TestVNextJSONEnvelopeHasStableFields(t *testing.T) {
	var output strings.Builder
	err := runVNextCompile(&output, []string{"--app-root", vnextFixtureRoot(t), "-o", "json", "--non-interactive", "--quiet"})
	if err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal([]byte(output.String()), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"api_version", "diagnostic_catalog", "ok", "workspace_revision", "contract_revision", "implementation_revision", "deployment_revision", "data", "diagnostics"} {
		if _, ok := envelope[field]; !ok {
			t.Errorf("missing stable envelope field %q in %s", field, output.String())
		}
	}
	if envelope["api_version"] != "scenery.cli.v1" {
		t.Fatalf("api_version = %v", envelope["api_version"])
	}
}

func TestVNextSchemaPublishesDiagnosticCatalog(t *testing.T) {
	var output strings.Builder
	if err := runVNextSchema(&output, []string{vnext.DiagnosticCatalog, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var envelope vnextEnvelope
	if err := json.Unmarshal([]byte(output.String()), &envelope); err != nil {
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

func TestVNextDiffConsumesRenameReceiptFile(t *testing.T) {
	root := t.TempDir()
	before := vnext.Resource{Address: "house/record/old", Kind: "scenery.record/v1", Module: "house", Name: "old", Spec: map[string]any{}}
	after := before
	after.Address, after.Name = "house/record/new", "new"
	base := &vnext.Manifest{APIVersion: vnext.ManifestVersion, ContractRevision: "sha256:base", Resources: []vnext.Resource{before}}
	target := &vnext.Manifest{APIVersion: vnext.ManifestVersion, ContractRevision: "sha256:target", Resources: []vnext.Resource{after}}
	receipt := vnext.RenameReceipt{From: before.Address, To: after.Address, BaseContractRevision: base.ContractRevision, TargetContractRevision: target.ContractRevision}
	canonical, err := vnext.MarshalCanonical(receipt)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(append([]byte("scenery.rename-receipt.v1\x00"), canonical...))
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
	receiptPath := writeJSON("receipt.json", map[string]any{"rename_receipts": []vnext.RenameReceipt{receipt}})
	var output strings.Builder
	if err := runVNextDiff(&output, []string{"--semantic", basePath, targetPath, "--rename-receipts", receiptPath, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var envelope struct {
		Data vnext.SemanticDiff `json:"data"`
	}
	if err := json.Unmarshal([]byte(output.String()), &envelope); err != nil || len(envelope.Data.Changes) != 1 || envelope.Data.Changes[0].Operation != "rename" {
		t.Fatalf("diff output = %s, %v", output.String(), err)
	}
}

func TestVNextJSONFailureWritesExactlyOneStableEnvelope(t *testing.T) {
	var output strings.Builder
	err := renderVNextMachineError(&output, []string{"migrate", "activate", "missing", "-o", "json"}, errors.New("failed_precondition: candidate unavailable"))
	if err == nil || cliExitCode(err) != 3 {
		t.Fatalf("render error = %v, code %d", err, cliExitCode(err))
	}
	decoder := json.NewDecoder(strings.NewReader(output.String()))
	var envelope vnextEnvelope
	if err := decoder.Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8003" {
		t.Fatalf("failure envelope = %#v", envelope)
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		t.Fatalf("failure output contains more than one document: %q", output.String())
	}
}

func TestVNextJSONFailureCodesMatchTransportErrorKinds(t *testing.T) {
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
		err := renderVNextMachineError(&output, []string{"compile", "-o", "json"}, test.err)
		if err == nil {
			t.Fatalf("renderVNextMachineError(%v) returned nil", test.err)
		}
		var envelope vnextEnvelope
		if decodeErr := json.Unmarshal([]byte(output.String()), &envelope); decodeErr != nil {
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

func TestVNextQuietSuppressesHumanOutput(t *testing.T) {
	var output strings.Builder
	if err := runVNextCompile(&output, []string{"--app-root", vnextFixtureRoot(t), "--quiet"}); err != nil {
		t.Fatal(err)
	}
	if output.Len() != 0 {
		t.Fatalf("quiet output = %q", output.String())
	}
}

func vnextFixtureRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Join(filepath.Dir(file), "..", "..", "internal", "vnext", "testdata", "house")
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

	for _, flag := range []string{"--proxy", "--trust"} {
		if _, err := parseDevArgs([]string{flag}); err == nil || !strings.Contains(err.Error(), `unknown flag "`+flag+`"`) {
			t.Fatalf("parseDevArgs(%s) error = %v, want unknown flag", flag, err)
		}
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
