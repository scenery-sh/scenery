package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/build"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/envpolicy"
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
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
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
				SuggestedAction: "Fix the native contract application boundary, then rerun `scenery harness self --release --summary --write`.",
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
	defer os.RemoveAll(probeRoot)
	appRoot := filepath.Join(probeRoot, "app")
	devCacheRoot := filepath.Join(probeRoot, "devcache")
	restoreDevCache := devcache.SetRoot(devCacheRoot)
	defer restoreDevCache()

	if err := segments.run("copy fixture", func() error {
		return copyHarnessNativeContractFixture(repoRoot, appRoot)
	}); err != nil {
		return summary, nil, err
	}

	var result *build.Result
	if err := segments.run("prepare generated application", func() error {
		var prepareErr error
		result, prepareErr = build.Prepare(appRoot, nil, appcfg.Config{Name: "nativeapp"})
		return prepareErr
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("verify prepared build manifest", func() error {
		manifest, ok, readErr := build.ReadLatestBuildManifest(appRoot)
		if readErr != nil {
			return readErr
		}
		if !ok || manifest.Build.Phase != "prepared" || manifest.Build.BuildStateExists {
			return fmt.Errorf("prepared build manifest = %+v, exists = %t", manifest, ok)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}
	if err := segments.run("verify generated entrypoint", func() error {
		return verifyHarnessNativeContractEntrypoint(result.Dir)
	}); err != nil {
		return summary, nil, err
	}

	configuredBuildFlags := slices.Clone(result.GoBuildFlags)
	result.GraphFingerprint = "graph-fingerprint-release-probe"
	result.Metadata = json.RawMessage(`{"app":"nativeapp"}`)
	result.APIEncoding = json.RawMessage(`{"services":[]}`)
	if err := segments.run("compile generated application", func() error {
		return build.CompileContext(ctx, result)
	}); err != nil {
		return summary, nil, err
	}
	if !slices.Equal(result.GoBuildFlags, configuredBuildFlags) {
		return summary, nil, fmt.Errorf("compile mutated configured go build flags: %v", result.GoBuildFlags)
	}
	if len(result.RuntimeLinkerMetadata) == 0 {
		return summary, nil, fmt.Errorf("compile did not record runtime linker metadata")
	}
	if _, err := os.Stat(result.Binary); err != nil {
		return summary, nil, fmt.Errorf("compiled native application binary: %w", err)
	}
	if err := segments.run("verify compiled build manifest", func() error {
		manifest, ok, readErr := build.ReadLatestBuildManifest(appRoot)
		if readErr != nil {
			return readErr
		}
		if !ok || manifest.Build.Phase != "compiled" || !manifest.Build.BinaryExists || !manifest.Build.BuildStateExists {
			return fmt.Errorf("compiled build manifest = %+v, exists = %t", manifest, ok)
		}
		return nil
	}); err != nil {
		return summary, nil, err
	}

	var bundle build.RuntimeBundleDescriptor
	localReplaceBuildInputs := 0
	if err := segments.run("verify reusable build state", func() error {
		cached, ok, cacheErr := build.LoadCachedGraph(appRoot, appcfg.Config{Name: "nativeapp"}, result.GraphFingerprint)
		if cacheErr != nil {
			return cacheErr
		}
		if !ok || cached == nil {
			return fmt.Errorf("compiled graph is not immediately reusable")
		}
		if !cached.Result.ReuseCompiled {
			reused, refreshErr := build.RefreshCachedWorkspace(appRoot, cached.Result)
			if refreshErr != nil {
				return refreshErr
			}
			if !reused || !cached.Result.ReuseCompiled {
				return fmt.Errorf("refreshed workspace does not reuse the compiled binary")
			}
		}
		bundle, cacheErr = build.ReadRuntimeBundle(appRoot, "development")
		if cacheErr != nil {
			return cacheErr
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

	if err := segments.run("start application and run generated client", func() error {
		return runHarnessGeneratedTypeScriptClient(ctx, appRoot, devCacheRoot, result.Binary)
	}); err != nil {
		return summary, nil, err
	}

	summary["proof"] = "generated_native_contract_application_compiled_started_and_called"
	summary["contract_revision"] = bundle.ContractRevision
	summary["implementation_revision"] = bundle.ImplementationRevision
	summary["build_input_digest"] = bundle.BuildInput.Digest
	summary["local_replace_build_inputs"] = localReplaceBuildInputs
	summary["latest_build_manifest_proof"] = "prepare_and_compile_phases_published"
	return summary, nil, nil
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
	return nil
}

var nativeContractEntrypointFragments = []string{
	`scenerycomposition "example.test/nativeapp/internal/scenerygen/composition"`,
	"sceneryruntime.VerifyLinkedContractBundle(scenerycomposition.ContractRevision)",
	"sceneryruntime.NewContractRegistry",
	"scenerycomposition.Register(contractRegistry)",
	"contractRegistry.Seal()",
}

func runHarnessGeneratedTypeScriptClient(parent context.Context, appRoot, devCacheRoot, binary string) error {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		return err
	}

	logPath := filepath.Join(appRoot, "reference-server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return err
	}
	defer logFile.Close()
	serverCtx, stopServerContext := context.WithCancel(parent)
	defer stopServerContext()
	server := exec.CommandContext(serverCtx, binary)
	server.Dir = appRoot
	server.Env = envWithOverrides(envpolicy.Environ(),
		"SCENERY_LISTEN_ADDR="+address,
		"SCENERY_DEV_CACHE_DIR="+devCacheRoot,
	)
	server.Stdout = logFile
	server.Stderr = logFile
	if err := server.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- server.Wait() }()
	stopped := false
	stopServer := func() {
		if stopped {
			return
		}
		stopped = true
		stopServerContext()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			_ = server.Process.Kill()
			<-done
		}
		_ = logFile.Sync()
	}
	defer stopServer()
	serverOutput := func() string {
		_ = logFile.Sync()
		data, _ := os.ReadFile(logPath)
		return string(data)
	}

	baseURL := "http://" + address
	if err := os.WriteFile(filepath.Join(appRoot, "typescript_reference_server_url.txt"), []byte(baseURL), 0o600); err != nil {
		return err
	}
	client := &http.Client{Timeout: 500 * time.Millisecond}
	deadline := time.NewTimer(30 * time.Second)
	defer deadline.Stop()
	for {
		request, requestErr := http.NewRequestWithContext(parent, http.MethodGet, baseURL+"/__scenery_reference_ready", nil)
		if requestErr != nil {
			return requestErr
		}
		response, requestErr := client.Do(request)
		if requestErr == nil {
			_ = response.Body.Close()
			// Any response proves the generated server is accepting HTTP. The
			// reserved probe path intentionally has no application route.
			break
		}
		select {
		case <-parent.Done():
			return fmt.Errorf("generated reference server readiness: %w\n%s", parent.Err(), serverOutput())
		case <-deadline.C:
			return fmt.Errorf("generated reference server did not become ready: %v\n%s", requestErr, serverOutput())
		case serverErr := <-done:
			stopped = true
			return fmt.Errorf("generated reference server exited before readiness: %v\n%s", serverErr, serverOutput())
		case <-time.After(25 * time.Millisecond):
		}
	}

	bun := exec.CommandContext(parent, "bun", "test", "./typescript_reference_server.test.ts")
	bun.Dir = appRoot
	bunOutput, err := bun.CombinedOutput()
	if err != nil {
		return fmt.Errorf("generated TypeScript client against generated Go server: %w\n%s\nserver:\n%s", err, bunOutput, serverOutput())
	}
	if !bytes.Contains(bunOutput, []byte("1 pass")) {
		return fmt.Errorf("generated TypeScript client proof did not report one pass:\n%s", bunOutput)
	}
	return nil
}
