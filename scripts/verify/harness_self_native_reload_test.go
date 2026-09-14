package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func nativeReloadTestIdentity() nativeReloadIdentity {
	digest := nativeReloadDigest([]byte("captured inputs"))
	return nativeReloadIdentity{ABI: nativeReloadABI, ProtocolRevision: digest, FrameworkSource: digest, FrameworkExecutable: digest, ContractABI: digest,
		BuildInputs: digest, Implementation: digest, ExecutionGeneration: digest, ExecutableDigest: digest,
		Worktree: "/owned/onlv", Session: "session-epoch", Toolchain: "go1.27.0", Target: "darwin/arm64"}
}

func TestNativeReloadRejectsEveryStaleIdentityField(t *testing.T) {
	t.Parallel()
	want := nativeReloadTestIdentity()
	if err := nativeReloadCheckIdentity(want, want); err != nil {
		t.Fatal(err)
	}
	for i := range reflect.TypeOf(want).NumField() {
		stale := want
		field := reflect.ValueOf(&stale).Elem().Field(i)
		field.SetString(field.String() + "-stale")
		if err := nativeReloadCheckIdentity(want, stale); err == nil {
			t.Fatalf("accepted changed %s", reflect.TypeOf(want).Field(i).Name)
		}
	}
	if err := nativeReloadCheckIdentity(nativeReloadIdentity{}, nativeReloadIdentity{}); err == nil {
		t.Fatal("accepted empty identity")
	}
}

func TestNativeReloadFramesRejectUnknownTrailingAndOversizedData(t *testing.T) {
	t.Parallel()
	for _, data := range []string{`{"kind":"ready","unrecognized":true}`, `{} {}`, strings.Repeat(" ", nativeReloadFrameLimit+1)} {
		if _, err := nativeReloadDecodeFrame([]byte(data)); err == nil {
			t.Fatalf("accepted malformed frame with %d bytes", len(data))
		}
	}
	want := nativeReloadFrame{Kind: "ready", Identity: nativeReloadTestIdentity(), PID: 123, Constructed: true}
	data, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := nativeReloadDecodeFrame(data)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("frame changed: %+v %v", got, err)
	}
}

func TestNativeReloadRejectsIncompleteClosure(t *testing.T) {
	t.Parallel()
	for _, data := range []string{
		`{"ImportPath":"clean.tech/scenery_implementation_island","Incomplete":true}`,
		`{"ImportPath":"clean.tech/scenery_implementation_island","Imports":["missing.example/dep"]}`,
		`{"ImportPath":"scenery.sh/runtime"}`,
		`{"ImportPath":"clean.tech/scenery_implementation_island","Error":{"Err":"load failed"}}`,
	} {
		if _, err := nativeReloadCapture([]byte(data), json.RawMessage(`{}`), "/absent"); err == nil {
			t.Fatal("accepted incomplete or monolithic capture")
		}
	}
}

func TestNativeReloadUniqueEditChangesHandlerLiteral(t *testing.T) {
	t.Parallel()
	original := []byte(`return "query must be at most %d characters"`)
	first, err := nativeReloadEditedSource(original, "one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := nativeReloadEditedSource(original, "two")
	if err != nil || string(first) == string(second) || !strings.Contains(string(first), "characters [one]") {
		t.Fatalf("not a unique handler edit: %s %s %v", first, second, err)
	}
	if _, err := nativeReloadEditedSource([]byte("unrelated"), "one"); err == nil {
		t.Fatal("accepted missing implementation anchor")
	}
	if got := nativeReloadStats([]float64{5, 2, 1, 4, 3}); got["p50_ms"] != 3.0 || got["p95_ms"] != 5.0 {
		t.Fatalf("quantiles: %v", got)
	}
}

func TestNativeReloadActionAttribution(t *testing.T) {
	t.Parallel()
	actions, err := nativeReloadToolActions([]byte(`[
		{"Mode":"build","Package":"cached"},
		{"Mode":"build","Package":"handler","Cmd":["compile"],"CmdReal":25000000,
		 "TimeReady":"2026-09-13T12:00:00Z","TimeStart":"2026-09-13T12:00:00.002Z","TimeDone":"2026-09-13T12:00:00.040Z"}
	]`))
	if err != nil || len(actions) != 1 || actions[0].CommandMS != 25 || actions[0].ActionMS != 38 || actions[0].QueueMS != 2 {
		t.Fatalf("action attribution: %+v %v", actions, err)
	}
	if _, err := nativeReloadToolActions([]byte(`[]`)); err == nil {
		t.Fatal("accepted absent tool attribution")
	}
}

func TestNativeReloadEffects(t *testing.T) {
	t.Parallel()
	want := []string{"external-binary", "filesystem-read", "filesystem-write", "tempdir", "test-cache"}
	for _, name := range []string{harnessNativeReloadName, harnessNativeReloadPluginName, harnessNativeAttributionName} {
		got := harnessStepEffects(harnessStep{Name: name})
		if !slices.Equal(got, want) {
			t.Fatalf("%s effects = %v, want %v", name, got, want)
		}
	}
}

func pluginReloadTestIdentity(t *testing.T) (pluginReloadHostIdentity, pluginReloadIdentity) {
	t.Helper()
	digest := pluginReloadDigest([]byte("input"))
	host := pluginReloadHostIdentity{Base: pluginReloadHostBase{ABI: pluginReloadABI, ProtocolRevision: digest,
		FrameworkSource: digest, FrameworkExecutable: digest, ContractABI: digest, HostBuildInputs: digest,
		ArtifactRoot: t.TempDir(), Worktree: "/owned/onlv", Session: "session", Toolchain: "go1.27.0", Target: "darwin/arm64"},
		ExecutableDigest: digest}
	linked := pluginReloadLinkedIdentity{Host: host, BuildInputs: digest, Implementation: digest}
	data, err := json.Marshal(linked)
	if err != nil {
		t.Fatal(err)
	}
	linked.ExecutionGeneration = pluginReloadDigest(data)
	identity := pluginReloadIdentity{Linked: linked, ArtifactDigest: digest}
	return host, identity
}

func TestPluginReloadStrictIdentityFramesAndPaths(t *testing.T) {
	t.Parallel()
	host, identity := pluginReloadTestIdentity(t)
	if err := pluginReloadCheckIdentity(host, identity, identity); err != nil {
		t.Fatal(err)
	}
	stale := identity
	stale.Linked.Host.Base.Session += "-foreign"
	if err := pluginReloadCheckIdentity(host, identity, stale); err == nil {
		t.Fatal("accepted a foreign plugin session")
	}
	stale = identity
	stale.ArtifactDigest = pluginReloadDigest([]byte("other artifact"))
	if err := pluginReloadCheckIdentity(host, identity, stale); err == nil {
		t.Fatal("accepted a different plugin artifact")
	}
	stale = identity
	stale.Linked.ExecutionGeneration = pluginReloadDigest([]byte("invented generation"))
	if err := pluginReloadCheckIdentity(host, stale, stale); err == nil {
		t.Fatal("accepted a generation not derived from the linked inputs")
	}
	for _, data := range []string{`{"kind":"host_ready","unknown":true}`, `{} {}`, strings.Repeat(" ", pluginReloadFrameLimit+1)} {
		if _, err := pluginReloadDecodeFrame([]byte(data)); err == nil {
			t.Fatalf("accepted malformed plugin frame with %d bytes", len(data))
		}
	}
	inside := filepath.Join(host.Base.ArtifactRoot, "generation.so")
	if !pluginReloadPathWithin(host.Base.ArtifactRoot, inside) || pluginReloadPathWithin(host.Base.ArtifactRoot, host.Base.ArtifactRoot) || pluginReloadPathWithin(host.Base.ArtifactRoot, filepath.Join(host.Base.ArtifactRoot, "..", "foreign.so")) {
		t.Fatal("plugin artifact confinement changed")
	}
}

func TestPluginReloadUsesUniqueGeneratedPackages(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	bench := &nativeReloadPluginBenchmark{
		common:         &nativeReloadBenchmark{appRoot: root},
		pluginTemplate: []byte("package main\n"),
	}
	first, err := bench.preparePluginPackage("edit-01")
	if err != nil {
		t.Fatal(err)
	}
	second, err := bench.preparePluginPackage("edit-02")
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first != "./scenery_implementation_plugin_edit_01" || second != "./scenery_implementation_plugin_edit_02" {
		t.Fatalf("plugin package paths are not unique: %q %q", first, second)
	}
	for _, path := range []string{first, second} {
		data, err := os.ReadFile(filepath.Join(root, strings.TrimPrefix(path, "./"), "main.go"))
		if err != nil || string(data) != "package main\n" {
			t.Fatalf("generated plugin package %q: %q %v", path, data, err)
		}
	}
	if _, err := bench.preparePluginPackage("../foreign"); err == nil {
		t.Fatal("accepted a plugin package outside the owned root")
	}
}
