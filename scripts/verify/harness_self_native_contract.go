package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/build"
)

const harnessNativeContractApplicationProbeName = "native contract application probe"

type harnessNativeContractApplicationCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessNativeContractApplicationProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessNativeContractApplicationCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessNativeContractApplicationProbeName,
		Command: []string{"go", "run", "./scripts/verify", "--repo-root", repoRoot, "--probe", "native-contract", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the native contract application boundary, then rerun `go run ./scripts/verify --probe native-contract --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

type harnessNativeContractProbeSegments struct {
	entries []map[string]any
}

func (s *harnessNativeContractProbeSegments) run(name string, fn func() error) error {
	started := time.Now()
	err := fn()
	s.entries = append(s.entries, map[string]any{
		"name":        name,
		"duration_ms": time.Since(started).Milliseconds(),
		"ok":          err == nil,
	})
	return err
}

func runHarnessNativeContractApplicationProbeCheckWithArtifacts(parent context.Context, repoRoot string, artifacts harnessArtifactContext) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	// Cold compilation of scenery-kernel on a 2-vCPU CI runner can take ~2 minutes
	// before the fixture application starts.
	ctx, cancel := context.WithTimeout(parent, 5*time.Minute)
	defer cancel()

	segments := &harnessNativeContractProbeSegments{}
	summary = map[string]any{
		"proof":    "pending",
		"segments": segments.entries,
	}
	defer func() { summary["segments"] = segments.entries }()

	probeRoot, err := os.MkdirTemp("", "scenery-native-contract-probe-*")
	if err != nil {
		return summary, nil, err
	}
	summary["fixture_root"] = probeRoot
	defer func() {
		if err == nil {
			_ = os.RemoveAll(probeRoot)
		}
	}()
	appRoot := filepath.Join(probeRoot, "app")
	devCacheRoot := filepath.Join(probeRoot, "devcache")

	if err := segments.run("copy fixture", func() error {
		return copyHarnessNativeContractFixture(repoRoot, appRoot)
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("place service in nested directory group", func() error {
		return groupHarnessNativeContractFixture(appRoot)
	}); err != nil {
		return summary, nil, err
	}

	env := envWithOverrides(harnessAppEnv(filepath.Join(probeRoot, "state")), "SCENERY_DEV_CACHE_DIR="+devCacheRoot)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, cleanupErr := runHarnessAppCLIWithEnv(cleanup, repoRoot, appRoot, env, "down", "-o", "json")
		err = errors.Join(err, cleanupErr)
	}()
	if err := segments.run("prepare generated application", func() error {
		_, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "generate", "-o", "json")
		return err
	}); err != nil {
		return summary, nil, err
	}
	var started detachedDevResult
	if err := segments.run("link generated process entrypoints", func() error {
		output, err := runHarnessAppCLIWithEnv(ctx, repoRoot, appRoot, env, "up", "--detach", "--wait", "ready", "-o", "json")
		if err != nil {
			return err
		}
		if err := decodeCLIJSON(output, &started); err != nil {
			return err
		}
		// Readiness can precede terminal build telemetry. Wait for the queued
		// operation's completion before evaluating link/reuse evidence.
		if err := harnessWaitBuildRequest(ctx, started.LogPath, 0, true); err != nil {
			return err
		}
		events, err := harnessWatchEvents(started.LogPath, 0)
		if err != nil {
			return err
		}
		evidence, err := harnessPrivateExternalBuildEvidence(events)
		summary["external_source_build"] = evidence
		return err
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("verify compiled build manifest and entrypoints", func() error {
		manifest, ok, readErr := build.ReadLatestBuildManifest(appRoot)
		if readErr != nil {
			return readErr
		}
		if !ok || manifest.Build.Phase != "compiled" || !manifest.Build.BuildStateExists {
			return fmt.Errorf("compiled build manifest = %+v, exists = %t", manifest, ok)
		}
		return verifyHarnessNativeContractEntrypoint(manifest.Build.WorkspaceDir)
	}); err != nil {
		return summary, nil, err
	}

	var served harnessServedIdentity
	if err := segments.run("read the host-attested serving identity", func() error {
		var err error
		served, err = harnessWorktreeServing(ctx, repoRoot, appRoot, env)
		return err
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("call grouped routes and run generated client against the session", func() error {
		baseURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
		testStep, issue := runHarnessGeneratedTypeScriptClient(ctx, appRoot, baseURL, started.Session.AppPID, served, artifacts)
		summary["generated_client_tests"] = testStep
		return issue
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("verify reusable build state through public restart", func() error {
		return verifyHarnessNativeRuntimeReuse(ctx, repoRoot, appRoot, env, served)
	}); err != nil {
		return summary, nil, err
	}

	summary["proof"] = "generated_native_contract_process_entrypoints_linked_started_attested_and_called"
	summary["contract_revision"] = served.ContractRevision
	summary["implementation_revision"] = served.ImplementationRevision
	summary["build_input_digest"] = served.BuildInputDigest
	summary["latest_build_manifest_proof"] = "compiled_phase_and_public_restart_reusing_every_process_executable"
	summary["prepared_phase_assertion"] = "internal/build.TestPrepareAndCompileWriteLatestBuildManifestInProcess"
	summary["configured_flags_assertion"] = "internal/build.TestCompilePassesConfiguredGoBuildFlags"
	summary["grouped_route"] = "/api/group1/nested/house/process"
	summary["old_route_assertion"] = "POST /api/house/process returns 404 without an alias"
	return summary, nil, nil
}

// Move only this probe's disposable source; retain the module/service identities.
func groupHarnessNativeContractFixture(appRoot string) error {
	groupRoot := filepath.Join(appRoot, "group1", "nested")
	if err := os.MkdirAll(groupRoot, 0o755); err != nil {
		return err
	}
	if err := os.Rename(filepath.Join(appRoot, "house"), filepath.Join(groupRoot, "house")); err != nil {
		return err
	}
	for _, relative := range []string{"app.scn", "group1/nested/house/package.scn", "group1/nested/house/service.go"} {
		filename := filepath.Join(appRoot, relative)
		data, err := os.ReadFile(filename)
		if err != nil {
			return err
		}
		updated := strings.NewReplacer(
			"example.test/nativeapp/house", "example.test/nativeapp/group1/nested/house",
			`"house/scenerycontract"`, `"group1/nested/house/scenerycontract"`,
			`source = "./house"`, `source = "./group1/nested/house"`,
		).Replace(string(data))
		if err := os.WriteFile(filename, []byte(updated), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func copyHarnessNativeContractFixture(repoRoot, appRoot string) error {
	fixtureRoot := filepath.Join(repoRoot, "internal", "compiler", "testdata", "native")
	if err := filepath.WalkDir(fixtureRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() && appwalk.SkipDir(fixtureRoot, path) {
			return filepath.SkipDir
		}
		relative, err := filepath.Rel(fixtureRoot, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		target := filepath.Join(appRoot, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("native contract fixture contains symlink %s", relative)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("native contract fixture contains non-regular file %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode().Perm())
	}); err != nil {
		return err
	}

	goModPath := filepath.Join(appRoot, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		return err
	}
	updated := []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(repoRoot), 1))
	if bytes.Equal(updated, goMod) {
		return fmt.Errorf("native contract fixture does not contain the expected local scenery replacement")
	}
	return os.WriteFile(goModPath, updated, 0o644)
}

// verifyHarnessNativeContractEntrypoint checks the application entrypoint a
// production build links and the service entrypoint of the development process
// model: both verify the linked contract bundle before registering and sealing
// the contract registry.
func verifyHarnessNativeContractEntrypoint(workspaceRoot string) error {
	services, err := filepath.Glob(filepath.Join(workspaceRoot, "scenery_internal_processes", "services", "*", "main.go"))
	if err != nil || len(services) != 1 {
		return fmt.Errorf("generated worker entrypoints = %v, %v; want one", services, err)
	}
	serviceSource, err := os.ReadFile(services[0])
	if err != nil {
		return err
	}
	for _, fragment := range nativeContractServiceEntrypointFragments {
		if !bytes.Contains(serviceSource, []byte(fragment)) {
			return fmt.Errorf("generated worker entrypoint %s is missing %q", services[0], fragment)
		}
	}
	return nil
}

var nativeContractServiceEntrypointFragments = []string{
	`scenerycomposition "example.test/nativeapp/internal/scenerygen/`,
	"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
	"sceneryruntime.NewContractRegistry",
	"scenerycomposition.Register(contractRegistry)",
	"contractRegistry.Seal()",
	"sceneryruntime.MainGoWorker(",
}

// runHarnessGeneratedTypeScriptClient calls the grouped route and its removed
// ungrouped spelling through the session, requires every answer to attest the
// linked runtime bundle and the session's host process, and runs the generated
// TypeScript client against the same session.
func runHarnessGeneratedTypeScriptClient(parent context.Context, appRoot, baseURL, hostPID string, served harnessServedIdentity, artifacts harnessArtifactContext) (harnessStep, error) {
	if baseURL == "" || hostPID == "" {
		return harnessStep{}, fmt.Errorf("the session published no API route or host process")
	}
	if err := os.WriteFile(filepath.Join(appRoot, "typescript_reference_server_url.txt"), []byte(baseURL), 0o600); err != nil {
		return harnessStep{}, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, route := range []string{"/api/house/process", "/api/group1/nested/house/process"} {
		request, err := http.NewRequestWithContext(parent, http.MethodPost, baseURL+route, strings.NewReader(`{"scene_id":"grouped-probe"}`))
		if err != nil {
			return harnessStep{}, err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return harnessStep{}, err
		}
		_ = response.Body.Close()
		want := http.StatusOK
		if route == "/api/house/process" {
			want = http.StatusNotFound
		}
		if response.StatusCode != want {
			return harnessStep{}, fmt.Errorf("grouped native route %s returned %d, want %d", route, response.StatusCode, want)
		}
		if want != http.StatusOK {
			continue
		}
		if got := harnessResponseIdentity(response); got != served || response.Header.Get("X-App-Host-Pid") != hostPID {
			return harnessStep{}, fmt.Errorf("%s answered build %+v from host %q, want the served %+v from host %q", route, got, response.Header.Get("X-App-Host-Pid"), served, hostPID)
		}
		if worker := response.Header.Get("X-App-Worker-Pid"); worker == "" || worker == hostPID {
			return harnessStep{}, fmt.Errorf("%s did not name its answering worker process", route)
		}
	}
	expected := 1
	step := runHarnessBunStep(parent, appRoot, "native generated client", []string{"typescript_reference_server.test.ts"}, &expected, artifacts)
	if !step.OK {
		return step, fmt.Errorf("generated TypeScript client evidence failed: %s", firstNonEmpty(step.Error, fmt.Sprint(step.Diagnostics)))
	}
	return step, nil
}
