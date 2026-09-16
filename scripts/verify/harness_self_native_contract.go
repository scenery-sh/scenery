package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	localagent "scenery.sh/internal/agent"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/build"
)

const harnessNativeContractApplicationProbeName = "native contract application probe"

type harnessNativeContractApplicationCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessNativeContractApplicationProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessNativeContractApplicationProbeStepWithCheck(ctx, repoRoot, runHarnessNativeContractApplicationProbeCheck)
}

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

func runHarnessNativeContractApplicationProbeCheck(parent context.Context, repoRoot string) (summary map[string]any, diagnostics []checkDiagnostic, err error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Minute)
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

	var bundle build.RuntimeBundleDescriptor
	localReplaceBuildInputs := 0
	if err := segments.run("verify linked runtime bundle", func() error {
		var err error
		bundle, err = build.ReadRuntimeBundle(appRoot, "development")
		if err != nil {
			return err
		}
		if bundle.ContractRevision == "" || bundle.ImplementationRevision == "" || bundle.BuildInput == nil || bundle.BuildInput.Digest == "" {
			return fmt.Errorf("runtime bundle is incomplete")
		}
		for _, entry := range bundle.BuildInput.Entries {
			if strings.HasPrefix(entry.Identity, "package/scenery.sh/") {
				localReplaceBuildInputs++
			}
		}
		if localReplaceBuildInputs == 0 {
			return fmt.Errorf("runtime bundle did not include source bytes from the local scenery.sh replacement")
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("call grouped routes and run generated client against the session", func() error {
		baseURL := strings.TrimRight(started.Session.RouteManifest.Routes[localagent.RouteAPI].URL, "/")
		return runHarnessGeneratedTypeScriptClient(ctx, appRoot, baseURL, started.Session.AppPID, bundle)
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("verify reusable build state through public restart", func() error {
		return verifyHarnessNativeRuntimeReuse(ctx, repoRoot, appRoot, env, bundle)
	}); err != nil {
		return summary, nil, err
	}

	summary["proof"] = "generated_native_contract_process_entrypoints_linked_started_attested_and_called"
	summary["contract_revision"] = bundle.ContractRevision
	summary["implementation_revision"] = bundle.ImplementationRevision
	summary["build_input_digest"] = bundle.BuildInput.Digest
	summary["local_replace_build_inputs"] = localReplaceBuildInputs
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
	mainSource, err := os.ReadFile(filepath.Join(workspaceRoot, "scenery_internal_main", "main.go"))
	if err != nil {
		return err
	}
	for _, fragment := range nativeContractEntrypointFragments {
		if !bytes.Contains(mainSource, []byte(fragment)) {
			return fmt.Errorf("generated native application entrypoint is missing %q", fragment)
		}
	}
	services, err := filepath.Glob(filepath.Join(workspaceRoot, "scenery_internal_processes", "services", "*", "main.go"))
	if err != nil || len(services) != 1 {
		return fmt.Errorf("generated service process entrypoints = %v, %v; want one", services, err)
	}
	serviceSource, err := os.ReadFile(services[0])
	if err != nil {
		return err
	}
	for _, fragment := range nativeContractServiceEntrypointFragments {
		if !bytes.Contains(serviceSource, []byte(fragment)) {
			return fmt.Errorf("generated service process entrypoint %s is missing %q", services[0], fragment)
		}
	}
	host, err := os.ReadFile(filepath.Join(workspaceRoot, "scenery_internal_processes", "host", "main.go"))
	if err != nil {
		return err
	}
	if !bytes.Contains(host, []byte("sceneryruntime.MainProcessHost(")) || bytes.Contains(host, []byte("scenerycomposition")) {
		return fmt.Errorf("generated process host entrypoint does not run a host without application adapters")
	}
	return nil
}

var nativeContractEntrypointFragments = []string{
	`scenerycomposition "example.test/nativeapp/internal/scenerygen/composition"`,
	"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
	"sceneryruntime.NewContractRegistry",
	"scenerycomposition.Register(contractRegistry)",
	"contractRegistry.Seal()",
}

var nativeContractServiceEntrypointFragments = []string{
	`scenerycomposition "example.test/nativeapp/internal/scenerygen/`,
	"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
	"sceneryruntime.NewContractRegistry",
	"scenerycomposition.Register(contractRegistry)",
	"contractRegistry.Seal()",
}

// runHarnessGeneratedTypeScriptClient calls the grouped route and its removed
// ungrouped spelling through the session, requires every answer to attest the
// linked runtime bundle and the session's host process, and runs the generated
// TypeScript client against the same session.
func runHarnessGeneratedTypeScriptClient(parent context.Context, appRoot, baseURL, hostPID string, bundle build.RuntimeBundleDescriptor) error {
	if baseURL == "" || hostPID == "" {
		return fmt.Errorf("the session published no API route or host process")
	}
	if err := os.WriteFile(filepath.Join(appRoot, "typescript_reference_server_url.txt"), []byte(baseURL), 0o600); err != nil {
		return err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	for _, route := range []string{"/api/house/process", "/api/group1/nested/house/process"} {
		request, err := http.NewRequestWithContext(parent, http.MethodPost, baseURL+route, strings.NewReader(`{"scene_id":"grouped-probe"}`))
		if err != nil {
			return err
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := client.Do(request)
		if err != nil {
			return err
		}
		_ = response.Body.Close()
		want := http.StatusOK
		if route == "/api/house/process" {
			want = http.StatusNotFound
		}
		if response.StatusCode != want {
			return fmt.Errorf("grouped native route %s returned %d, want %d", route, response.StatusCode, want)
		}
		for header, value := range map[string]string{
			"X-Scenery-Contract-Revision": bundle.ContractRevision, "X-Scenery-Implementation-Revision": bundle.ImplementationRevision,
			"X-Scenery-Build-Input-Digest": bundle.BuildInput.Digest, "X-Scenery-Go-Target": bundle.Target, "X-Scenery-Process-ID": hostPID,
		} {
			if got := response.Header.Get(header); got != value {
				return fmt.Errorf("%s answered %s %q, want the linked runtime bundle's %q", route, header, got, value)
			}
		}
		if want == http.StatusOK && (response.Header.Get("X-Scenery-Service-Process-ID") == "" || response.Header.Get("X-Scenery-Service-Process-ID") == hostPID) {
			return fmt.Errorf("%s did not name its answering service process", route)
		}
	}
	bun := exec.CommandContext(parent, "bun", "test", "./typescript_reference_server.test.ts")
	bun.Dir = appRoot
	bunOutput, err := bun.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generated TypeScript client against the development session: %w\n%s", err, bunOutput)
	}
	if !bytes.Contains(bunOutput, []byte("1 pass")) {
		return fmt.Errorf("generated TypeScript client proof did not report one pass:\n%s", bunOutput)
	}
	return nil
}
