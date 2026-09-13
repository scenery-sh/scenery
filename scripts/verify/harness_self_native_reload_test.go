package main

import (
	"encoding/json"
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
	got := harnessStepEffects(harnessStep{Name: harnessNativeReloadName})
	want := []string{"external-binary", "filesystem-read", "filesystem-write", "tempdir", "test-cache"}
	if !slices.Equal(got, want) {
		t.Fatalf("experiment effects = %v, want %v", got, want)
	}
}
