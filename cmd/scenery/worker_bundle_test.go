package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	goruntime "runtime"
	"slices"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/workerplan"
)

func TestDeclaredDatabaseSetupRunsApplyThenSeedsBeforeWorkers(t *testing.T) {
	t.Parallel()

	root := writeDBSeedCommandFixture(t)
	_, cfg, err := discoverConfiguredApp(root)
	if err != nil {
		t.Fatal(err)
	}
	cfg.Database.Apply = appcfg.DatabaseApplyConfig{Command: "schema-apply"}
	contract, err := compileSQLContract(root)
	if err != nil {
		t.Fatal(err)
	}
	const dsn = "postgres://user:secret@localhost/seedapp?search_path=catalog%2Cscenery"
	var phases []string
	store := newFakeSeedStore()
	setup := declaredDatabaseSetup{
		root: root, cfg: cfg, contract: contract, env: []string{"CATALOG_DATABASE_URL=" + dsn},
		lifecycle: lifecycleHooks{runExec: func(_ context.Context, request lifecycleExecRequest) error {
			phases = append(phases, "apply "+envValueFromList(request.Env, "CATALOG_DATABASE_URL"))
			return nil
		}},
		seed: dbSeedHooks{
			openStore: func(context.Context, string) (databaseSeedStore, error) { return store, nil },
			runCommand: func(_ context.Context, request lifecycleExecRequest) (dbSeedCommandOutput, error) {
				phases = append(phases, "seed "+envValueFromList(request.Env, appDatabaseURLEnv))
				return dbSeedCommandOutput{}, nil
			},
		},
	}
	if err := setup.run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{"apply " + dsn, "seed " + dsn}; !slices.Equal(phases, want) {
		t.Fatalf("setup phases = %q, want %q", phases, want)
	}
	// An application that declares no setup starts without touching a database.
	setup.cfg.Database, phases = appcfg.DatabaseConfig{}, nil
	if err := setup.run(context.Background()); err != nil || len(phases) != 0 {
		t.Fatalf("undeclared setup ran %q: %v", phases, err)
	}
}

func TestWorkerBundleBuildRejectsUndeclaredGoTargetBeforeBuilding(t *testing.T) {
	fixture := filepath.Join(repoRootForTest(t), "testdata", "apps", "ts-only")
	err := buildCommand(io.Discard, []string{"--app-root", fixture, "--target", "missing"})
	if err == nil || cliExitCode(err) != 2 || !strings.Contains(err.Error(), `go target "missing" is unavailable; declare a go_target`) {
		t.Fatalf("error = %v (exit %d)", err, cliExitCode(err))
	}
}

func TestWorkerBundleLinksGoServicesFromTheSelectedTarget(t *testing.T) {
	root := t.TempDir()
	writeTestAppFile(t, root, ".scenery.json", `{"name":"targets"}`)
	compiled := &compiler.Result{Root: root, ContractStatus: "valid", Manifest: &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/go_target/development", Kind: "scenery.go-target", Name: "development", Spec: map[string]any{"role": "development"}},
		{Address: "jobs/service/jobs", Module: "jobs", Kind: "scenery.service", Name: "jobs", Spec: map[string]any{"runtime": "go"}},
	}}}
	bundle := workerBundle{Host: workerHostPlan{Workers: []workerplan.WorkerServicePlan{{Address: "jobs/service/jobs", Process: "worker-jobs"}}}}
	// A production bundle asks for the artifact target; the named target wins.
	for _, test := range []struct{ role, target string }{{"artifact", ""}, {"artifact", "linux"}} {
		_, err := bundle.addGoWorkers(context.Background(), compiled, test.role, test.target, nil, nil)
		selection, ok := errors.AsType[*compiler.GoTargetSelectionError](err)
		if !ok || selection.DefaultRole != test.role || selection.Name != test.target {
			t.Fatalf("role %q target %q: %v", test.role, test.target, err)
		}
	}
	// An application without Go services needs no go_target at all.
	compiled.Manifest.Resources[1].Spec["runtime"] = "rust"
	if preparation, err := bundle.addGoWorkers(context.Background(), compiled, "artifact", "", nil, nil); preparation != nil || err != nil {
		t.Fatalf("no Go service: %v, %v", preparation, err)
	}
}

func TestWorkerBundleRejectsCrossPlatformGoTarget(t *testing.T) {
	otherOS := "linux"
	if goruntime.GOOS == "linux" {
		otherOS = "darwin"
	}
	otherArch := "amd64"
	if goruntime.GOARCH == "amd64" {
		otherArch = "arm64"
	}
	manifest := &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/go_target/cross", Kind: "scenery.go-target", Name: "cross", Spec: map[string]any{
			"role": "artifact", "goos": otherOS, "goarch": otherArch, "cgo": "disabled",
		}},
		{Address: "jobs/service/jobs", Module: "jobs", Kind: "scenery.service", Name: "jobs", Origin: graph.Origin{Kind: "authored"}, Spec: map[string]any{
			"runtime": "go", "implementation": map[string]any{"kind": "go"},
		}},
		{Address: "jobs/operation/run", Module: "jobs", Kind: "scenery.operation", Name: "run", Spec: map[string]any{
			"service": map[string]any{"$ref": "service.jobs"}, "handler": map[string]any{"method": "Run"},
		}},
	}}
	compiled := &compiler.Result{Root: t.TempDir(), ContractStatus: "valid", Manifest: manifest}

	for _, target := range []string{"cross", ""} {
		err := validateWorkerBundleTargetPlatform(compiled, "artifact", target)
		if err == nil || cliExitCode(err) != 3 || !strings.Contains(err.Error(), "does not match host platform") {
			t.Fatalf("target=%q: error = %v (exit %d), want exit 3 cross-platform error", target, err, cliExitCode(err))
		}
		_, buildErr := buildWorkerBundle(context.Background(), compiled, "artifact", target, false)
		if buildErr == nil || cliExitCode(buildErr) != 3 || !strings.Contains(buildErr.Error(), "does not match host platform") {
			t.Fatalf("buildWorkerBundle(target=%q) = %v (exit %d), want exit 3 cross-platform error", target, buildErr, cliExitCode(buildErr))
		}
	}

	// An explicit cross-platform Go target is rejected even without Go services.
	compiled.Manifest.Resources[1].Spec["runtime"] = "rust"
	if err := validateWorkerBundleTargetPlatform(compiled, "artifact", "cross"); err == nil || cliExitCode(err) != 3 {
		t.Fatalf("explicit cross target without Go service: %v (exit %d)", err, cliExitCode(err))
	}
	if err := validateWorkerBundleTargetPlatform(compiled, "artifact", ""); err != nil {
		t.Fatalf("implicit empty target without Go service should pass: %v", err)
	}
}

// A rebuild replaces the previous bundle at its destination, while any other
// existing entry is refused untouched.
func TestWorkerBundlePublicationReplacesOnlyAPreviousBundle(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	stage := func(marker string) string {
		staging := filepath.Join(root, "staging-"+marker)
		for _, name := range []string{"manifest.json", "scenery-host"} {
			writeWatchFile(t, staging, name, marker)
		}
		return staging
	}
	destination := filepath.Join(root, "bundle")
	for _, marker := range []string{"first", "second"} {
		if err := publishWorkerBundle(stage(marker), destination); err != nil {
			t.Fatalf("publish %s: %v", marker, err)
		}
	}
	if data, err := os.ReadFile(filepath.Join(destination, "manifest.json")); err != nil || string(data) != "second" {
		t.Fatalf("published manifest = %q, %v", data, err)
	}
	other := filepath.Join(root, "other")
	writeWatchFile(t, other, "notes.txt", "keep")
	if err := publishWorkerBundle(stage("third"), other); cliExitCode(err) != 3 {
		t.Fatalf("publish over another directory = %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(other, "notes.txt")); err != nil || string(data) != "keep" {
		t.Fatalf("other directory = %q, %v", data, err)
	}
}
