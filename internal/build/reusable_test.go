package build

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/parse"
)

func TestPrepareReusesPersistentWorkspace(t *testing.T) {
	t.Parallel()

	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}

	first, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if !first.NeedsTidy {
		t.Fatal("expected first prepare to require go mod tidy")
	}
	first.NeedsTidy = false
	if err := savePrimedWorkspace(first); err != nil {
		t.Fatalf("save primed workspace: %v", err)
	}

	second, err := Prepare(appDir, model, appcfg.Config{Name: "buildtest"})
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.Dir != second.Dir {
		t.Fatalf("workspace dir = %q, want %q", second.Dir, first.Dir)
	}
	if second.NeedsTidy {
		t.Fatal("expected incremental prepare to skip go mod tidy")
	}
}

func TestPrepareUsesFingerprintSpecificWorkspaceBinary(t *testing.T) {
	t.Parallel()

	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	first, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }

func Changed() {}
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse updated app: %v", err)
	}
	second, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if second.Dir != first.Dir {
		t.Fatalf("workspace dir = %q, want %q", second.Dir, first.Dir)
	}
	if first.BuildFingerprint == "" || second.BuildFingerprint == "" {
		t.Fatalf("expected build fingerprints, got first=%q second=%q", first.BuildFingerprint, second.BuildFingerprint)
	}
	if first.BuildFingerprint == second.BuildFingerprint {
		t.Fatalf("expected source change to update build fingerprint %q", first.BuildFingerprint)
	}
	if first.Binary == second.Binary {
		t.Fatalf("expected source change to use a new binary path, got %q", second.Binary)
	}
	for _, binary := range []string{first.Binary, second.Binary} {
		if !strings.HasPrefix(filepath.Base(binary), "scenery-app-") {
			t.Fatalf("binary %q is not fingerprint-specific", binary)
		}
	}
}

func TestPrepareIncludesGoBuildFlagsInFingerprint(t *testing.T) {
	t.Parallel()

	appDir := newBuildTestApp(t)

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	firstCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_native"}},
	}
	first, err := Prepare(appDir, model, firstCfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}

	secondCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_portable"}},
	}
	second, err := Prepare(appDir, model, secondCfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if first.BuildFingerprint == second.BuildFingerprint {
		t.Fatalf("expected build flags to affect fingerprint %q", first.BuildFingerprint)
	}
	if first.Binary == second.Binary {
		t.Fatalf("expected build flags to affect binary path %q", first.Binary)
	}
	if second.ReuseCompiled {
		t.Fatal("expected changed build flags to avoid reusing compiled binary")
	}
}

func TestLoadReusableBinaryRequiresMatchingSourceFingerprint(t *testing.T) {
	t.Parallel()

	cfg := appcfg.Config{Name: "buildtest"}
	appDir, result := newReusableBinaryBuildTestWorkspace(t, cfg)

	reused, ok, err := LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() error = %v", err)
	}
	if !ok {
		t.Fatal("expected reusable binary")
	}
	if reused.Binary != result.Binary {
		t.Fatalf("reused binary = %q, want %q", reused.Binary, result.Binary)
	}

	writeBuildTestFile(t, appDir, "svc/extra.go", "package svc\n\nfunc extra() {}\n")
	reused, ok, err = LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() after source change error = %v", err)
	}
	if ok || reused != nil {
		t.Fatalf("expected source change to reject cached binary, got ok=%v result=%#v", ok, reused)
	}
}

func TestLoadReusableBinaryWithSnapshotInvalidatesStrictInputs(t *testing.T) {
	t.Parallel()

	cfg := appcfg.Config{Name: "buildtest"}
	appDir, result := newReusableBinaryBuildTestWorkspace(t, cfg)

	snapshot := sourceSnapshotForTest(t, appDir, map[string]bool{
		".scenery.json": false,
		"go.mod":        false,
		"svc/api.go":    false,
	})
	if reused, ok, err := LoadReusableBinaryWithSnapshot(appDir, cfg, snapshot); err != nil || !ok || reused == nil {
		t.Fatalf("expected reusable binary with matching snapshot, ok=%v result=%#v err=%v", ok, reused, err)
	}

	writeBuildTestFile(t, appDir, "svc/api.go", "package svc\n\nfunc changed() {}\n")
	changedSource := sourceSnapshotForTest(t, appDir, map[string]bool{
		".scenery.json": false,
		"go.mod":        false,
		"svc/api.go":    false,
	})
	if reused, ok, err := LoadReusableBinaryWithSnapshot(appDir, cfg, changedSource); err != nil || ok || reused != nil {
		t.Fatalf("expected source snapshot change to reject binary, ok=%v result=%#v err=%v", ok, reused, err)
	}

	writeBuildTestFile(t, appDir, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.1\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
	changedDependency := sourceSnapshotForTest(t, appDir, map[string]bool{
		".scenery.json": false,
		"go.mod":        false,
		"svc/api.go":    false,
	})
	if reused, ok, err := LoadReusableBinaryWithSnapshot(appDir, cfg, changedDependency); err != nil || ok || reused != nil {
		t.Fatalf("expected dependency input change to reject binary, ok=%v result=%#v err=%v", ok, reused, err)
	}
	writeBuildTestFile(t, appDir, "go.mod", "module example.com/buildtest\n\ngo 1.26.3\n\nrequire scenery.sh v0.0.0\n\nreplace scenery.sh => "+repoRoot(t)+"\n")
	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }
`)
	state, err := loadBuildState(result.Dir)
	if err != nil {
		t.Fatalf("load build state: %v", err)
	}
	state.GeneratorFingerprint = "stale-generator"
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("save stale generator state: %v", err)
	}
	if reused, ok, err := LoadReusableBinaryWithSnapshot(appDir, cfg, snapshot); err != nil || ok || reused != nil {
		t.Fatalf("expected generator fingerprint change to reject binary, ok=%v result=%#v err=%v", ok, reused, err)
	}

	state.GeneratorFingerprint = result.GeneratorFingerprint
	if err := saveBuildState(result.Dir, state); err != nil {
		t.Fatalf("restore build state: %v", err)
	}
	manifest, ok, err := ReadLatestBuildManifest(appDir)
	if err != nil || !ok {
		t.Fatalf("ReadLatestBuildManifest ok=%v err=%v", ok, err)
	}
	manifest.Build.DependencyFingerprint = "stale-deps"
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(LatestBuildPath(appDir), data, 0o644); err != nil {
		t.Fatalf("write stale latest manifest: %v", err)
	}
	if reused, ok, err := LoadReusableBinaryWithSnapshot(appDir, cfg, snapshot); err != nil || ok || reused != nil {
		t.Fatalf("expected dependency manifest mismatch to reject binary, ok=%v result=%#v err=%v", ok, reused, err)
	}
}

func TestLoadReusableBinaryRequiresMatchingGoBuildFlags(t *testing.T) {
	t.Parallel()

	cfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_native"}},
	}
	appDir, _ := newReusableBinaryBuildTestWorkspace(t, cfg)

	reused, ok, err := LoadReusableBinary(appDir, cfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() error = %v", err)
	}
	if !ok || reused == nil {
		t.Fatal("expected reusable binary with matching build flags")
	}

	changedCfg := appcfg.Config{
		Name:  "buildtest",
		Build: appcfg.BuildConfig{GoFlags: []string{"-tags=roofmapnet_portable"}},
	}
	reused, ok, err = LoadReusableBinary(appDir, changedCfg)
	if err != nil {
		t.Fatalf("LoadReusableBinary() after build flag change error = %v", err)
	}
	if ok || reused != nil {
		t.Fatalf("expected build flag change to reject cached binary, got ok=%v result=%#v", ok, reused)
	}
}

func TestPrepareReusesExistingFingerprintBinaryWhenStatePointsElsewhere(t *testing.T) {
	t.Parallel()

	appDir := newBuildTestApp(t)
	cfg := appcfg.Config{Name: "buildtest"}

	model, err := parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse app: %v", err)
	}
	first, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("first prepare: %v", err)
	}
	if err := os.WriteFile(first.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write first binary: %v", err)
	}
	first.NeedsTidy = false
	if err := savePrimedWorkspace(first); err != nil {
		t.Fatalf("save first state: %v", err)
	}
	firstBinary := first.Binary

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error {
	return nil
}
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse changed app: %v", err)
	}
	second, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("second prepare: %v", err)
	}
	if err := os.WriteFile(second.Binary, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("write second binary: %v", err)
	}
	second.NeedsTidy = false
	if err := savePrimedWorkspace(second); err != nil {
		t.Fatalf("save second state: %v", err)
	}
	if second.Binary == firstBinary {
		t.Fatal("expected source change to produce a different fingerprint binary")
	}

	writeBuildTestFile(t, appDir, "svc/api.go", `package svc

import "context"

//scenery:api public
func Hello(ctx context.Context) error { return nil }
`)
	model, err = parse.App(appDir, "buildtest")
	if err != nil {
		t.Fatalf("parse reverted app: %v", err)
	}
	reverted, err := Prepare(appDir, model, cfg)
	if err != nil {
		t.Fatalf("reverted prepare: %v", err)
	}
	if reverted.Binary != firstBinary {
		t.Fatalf("reverted binary = %q, want first binary %q", reverted.Binary, firstBinary)
	}
	if !reverted.ReuseCompiled {
		t.Fatal("expected prepare to reuse existing fingerprint binary after reverting source")
	}
}
