package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/envpolicy"
)

const harnessCoreSeparationName = "core responsibility separation"

func runHarnessCoreSeparationStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessCoreSeparationStepWithCheck(ctx, repoRoot, runHarnessCoreSeparation)
}

func runHarnessCoreSeparationStepWithCheck(ctx context.Context, repoRoot string, check func(context.Context, string) (map[string]any, error)) harnessStep {
	started := time.Now()
	step := harnessStep{Name: harnessCoreSeparationName, Command: []string{filepath.Join(repoRoot, ".scenery/harness/bin/verify"), "--release", "--summary", "--write"}}
	var err error
	step.Summary, err = check(ctx, repoRoot)
	step.DurationMS, step.OK = time.Since(started).Milliseconds(), err == nil
	if err != nil {
		step.Error = err.Error()
		step.Diagnostics = []checkDiagnostic{{Stage: step.Name, Severity: "error", Message: step.Error, SuggestedAction: "Restore repository/product separation and rerun `go run ./scripts/verify --probe core-separation --summary --write`."}}
	}
	return step
}

// This is an explicit real-toolchain/runtime acceptance, never an ordinary Go
// test or a product hook. Every filesystem/process target is owned by this run.
func runHarnessCoreSeparation(parent context.Context, repoRoot string) (summary map[string]any, returnErr error) {
	ctx, cancel := context.WithTimeout(parent, 12*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-core-separation-*")
	if err != nil {
		return nil, err
	}
	summary = map[string]any{"fixture_root": root, "assertions": map[string]any{}}
	assertions := summary["assertions"].(map[string]any)
	defer func() {
		if returnErr == nil {
			returnErr = os.RemoveAll(root)
		}
	}()
	source := filepath.Join(root, "source")
	if err := copyHarnessSourceOnlySnapshot(ctx, repoRoot, source); err != nil {
		return summary, err
	}
	run := func(dir string, args ...string) ([]byte, error) {
		cmd := commandTreeContext(ctx, args[0], args[1:]...)
		cmd.Dir, cmd.Env = dir, envWithOverrides(envpolicy.Environ(), "GOWORK=off")
		output, err := cmd.CombinedOutput()
		if err != nil {
			return output, fmt.Errorf("%v: %w: %s", args, err, tailString(string(output), 8192))
		}
		return output, nil
	}
	verifier := filepath.Join(root, "bin", "verify")
	if _, err := run(source, "go", "build", "-o", verifier, "./scripts/verify"); err != nil {
		return summary, err
	}
	verifierSHA, err := worktreeProbeFileSHA(verifier)
	if err != nil {
		return summary, err
	}
	assertions["A4_source_only_verifier_build"] = map[string]any{"binary": verifier, "sha256": verifierSHA}
	deps, err := run(source, "go", "list", "-deps", "./cmd/scenery")
	if err != nil {
		return summary, err
	}
	for _, dependency := range strings.Fields(string(deps)) {
		if dependency == "scenery.sh/internal/testsuite" || dependency == "scenery.sh/internal/schemacheck" || strings.HasPrefix(dependency, "scenery.sh/scripts/") {
			return summary, fmt.Errorf("product retains repository execution dependency %s", dependency)
		}
	}
	assertions["A3_product_dependency_closure"] = true
	sdkDeps, err := run(source, "go", "list", "-deps", ".")
	if err != nil {
		return summary, err
	}
	sdkPackages := strings.Fields(string(sdkDeps))
	if slices.Contains(sdkPackages, "scenery.sh/runtime") {
		return summary, fmt.Errorf("public SDK dependency closure retains scenery.sh/runtime")
	}
	assertions["A3_public_sdk_dependency_closure"] = map[string]any{
		"package_count": len(sdkPackages), "runtime_linked": false,
	}
	missingToolchain := filepath.Join(root, "unavailable-toolchain")
	if err := writeHarnessToolchainSourceFile(filepath.Join(missingToolchain, "go.mod"), "module example.test/unavailable\n\ngo 1.999.99\n", 0o600); err != nil {
		return summary, err
	}
	toolchainDiagnostics := checkDeclaredGoToolchainAvailable(ctx, missingToolchain)
	if len(toolchainDiagnostics) != 1 || toolchainDiagnostics[0].Severity != "error" || !strings.Contains(toolchainDiagnostics[0].SuggestedAction, "GOTOOLCHAIN=go1.999.99 go version") {
		return summary, fmt.Errorf("unavailable real toolchain did not fail with exact restore guidance: %+v", toolchainDiagnostics)
	}
	assertions["A1_unavailable_toolchain_rejected"] = toolchainDiagnostics

	// Deliberately compile an obsolete asset-name set into the product. The
	// independently built verifier must inspect the product's actual HTTP hash.
	embed := filepath.Join(source, "cmd/scenery/dashboard_static/dist")
	if err := writeHarnessToolchainSourceFile(filepath.Join(embed, "index.html"), "<!doctype html><title>Owned stale bundle</title>\n", 0o600); err != nil {
		return summary, err
	}
	if err := writeHarnessToolchainSourceFile(filepath.Join(embed, "stale-owned-asset.js"), "// Owned negative fixture.\n", 0o600); err != nil {
		return summary, err
	}
	if err := copyHarnessDirectory(filepath.Join(repoRoot, dashboardUIRootRel, "dist"), filepath.Join(source, dashboardUIRootRel, "dist")); err != nil {
		return summary, err
	}
	product := harnessLocalSceneryBinaryPath(source)
	buildProduct := func() error {
		producer, err := build.FrameworkSourceManifest(source)
		if err != nil {
			return err
		}
		linkerFlags, err := build.FrameworkProducerLinkerFlags(producer.Digest)
		if err != nil {
			return err
		}
		_, err = run(source, "go", "build", "-ldflags="+linkerFlags, "-o", product, "./cmd/scenery")
		return err
	}
	if err := buildProduct(); err != nil {
		return summary, err
	}
	staleSHA, err := worktreeProbeFileSHA(product)
	if err != nil {
		return summary, err
	}
	stale, failure := harnessDashboardFreshness(ctx, source)
	if failure != "the prepared product dashboard bundle is stale" || stale["stale"] != true || stale["dashboard_http"] != 200 {
		return summary, fmt.Errorf("stale-product acceptance did not reject the actual HTTP bundle: %s (%v)", failure, stale)
	}
	assertions["A4_stale_product_rejected"] = stale
	stale["sha256"] = staleSHA
	if err := os.RemoveAll(embed); err != nil {
		return summary, err
	}
	if err := copyHarnessDirectory(filepath.Join(repoRoot, "cmd/scenery/dashboard_static/dist"), embed); err != nil {
		return summary, err
	}
	if err := buildProduct(); err != nil {
		return summary, err
	}
	matched, failure := harnessDashboardFreshness(ctx, source)
	if failure != "" {
		return summary, fmt.Errorf("matched product rejected: %s", failure)
	}
	assertions["A4_matched_product_accepted"] = matched
	matchedSHA, err := worktreeProbeFileSHA(product)
	if err != nil {
		return summary, err
	}
	if matchedSHA == staleSHA {
		return summary, errors.New("stale and matched product builds have identical binary identity")
	}
	matched["sha256"] = matchedSHA
	// Remove repository execution sources/cache from this disposable SDK copy.
	// Subsequent app generation and runtime commands have only the product.
	for _, rel := range []string{"scripts/verify", "scripts/testsuite", "internal/testsuite"} {
		if err := os.RemoveAll(filepath.Join(source, rel)); err != nil {
			return summary, err
		}
	}
	if err := buildProduct(); err != nil {
		return summary, fmt.Errorf("build product without repository execution sources: %w", err)
	}
	if err := verifyHarnessProductWithoutRepositoryTools(ctx, source, root, assertions); err != nil {
		return summary, err
	}
	if err := verifyHarnessSlowTimingEnforcement(ctx, root, assertions); err != nil {
		return summary, err
	}
	return summary, nil
}

func copyHarnessSourceOnlySnapshot(ctx context.Context, source, destination string) error {
	output, err := runHarnessGit(ctx, source, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	for _, rel := range strings.Split(output, "\x00") {
		if rel == "" || strings.HasSuffix(rel, "_test.go") || strings.Contains(rel, "/scenerycontract/") || strings.Contains(rel, "/internal/scenerygen/") {
			continue
		}
		if !filepath.IsLocal(rel) {
			return fmt.Errorf("non-local snapshot path %q", rel)
		}
		info, err := os.Lstat(filepath.Join(source, rel))
		if errors.Is(err, os.ErrNotExist) {
			continue // Deleted tracked input in the candidate worktree.
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(filepath.Join(source, rel))
			if err != nil {
				return err
			}
			if filepath.IsAbs(target) || !filepath.IsLocal(filepath.Join(filepath.Dir(rel), target)) {
				return fmt.Errorf("source snapshot symlink escapes the repository: %s", rel)
			}
			path := filepath.Join(destination, rel)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				return err
			}
			if err := os.Symlink(target, path); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("source snapshot requires a regular file: %s", rel)
		}
		if err := copyHarnessFile(filepath.Join(source, rel), filepath.Join(destination, rel), info.Mode().Perm()); err != nil {
			return err
		}
	}
	return nil
}

func copyHarnessDirectory(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(filepath.Join(destination, rel), 0o700)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("non-regular fixture asset %s", path)
		}
		return copyHarnessFile(path, filepath.Join(destination, rel), 0o600)
	})
}

func copyHarnessFile(source, destination string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	return errors.Join(copyErr, out.Close())
}

func verifyHarnessSlowTimingEnforcement(ctx context.Context, root string, assertions map[string]any) error {
	fixture := filepath.Join(root, "timing")
	if err := writeHarnessToolchainSourceFile(filepath.Join(fixture, "go.mod"), "module example.test/timing\n\ngo 1.27.0\n", 0o600); err != nil {
		return err
	}
	if err := writeHarnessToolchainSourceFile(filepath.Join(fixture, "slow_test.go"), "package timing\nimport (\"testing\"; \"time\")\nfunc TestSlow(t *testing.T) { time.Sleep(110*time.Millisecond) }\n", 0o600); err != nil {
		return err
	}
	report := &harnessTestTimingReport{Budgets: harnessTestTimingBudgetsForMode(harnessSelfModeRelease, true), ObservedSlowTests: []harnessTestTiming{{Name: "TestSlow", Package: "example.test/timing", Seconds: 0.11, BudgetSeconds: harnessFastTestBudgetSeconds, TargetSeconds: harnessFastTestTargetSeconds}}}
	confirmHarnessTimingOutliers(ctx, fixture, report, runHarnessTimingConfirmationCommand)
	if !hasErrorDiagnostics(report.Diagnostics) || len(report.SlowTests) != 1 || len(report.SlowTests[0].IsolatedSamples) != 20 || report.SlowTests[0].IsolatedP95 == nil || *report.SlowTests[0].IsolatedP95 < harnessFastTestBudgetSeconds {
		return fmt.Errorf("slow disposable root escaped mandatory isolated enforcement: %+v", report)
	}
	assertions["A2_real_slow_root_rejected"] = report.SlowTests[0]
	return nil
}

func verifyHarnessProductWithoutRepositoryTools(ctx context.Context, source, root string, assertions map[string]any) (returnErr error) {
	appRoot, home := filepath.Join(root, "basic"), filepath.Join(root, "state")
	if err := copyHarnessBasicFixture(source, appRoot); err != nil {
		return err
	}
	for _, args := range [][]string{{"generate", "-o", "json"}, {"check", "-o", "json"}, {"harness", "-o", "json", "--write"}, {"inspect", "app", "-o", "json"}, {"inspect", "harness", "-o", "json"}} {
		output, err := runHarnessAppCLI(ctx, source, appRoot, home, args...)
		if err != nil {
			return err
		}
		var payload map[string]any
		if err := decodeCLIJSON(output, &payload); err != nil {
			return err
		}
		name := args[0]
		if name == "inspect" {
			name += "_" + args[1]
		}
		assertions["A3_product_"+name] = true
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		_, cleanupErr := runHarnessAppCLI(cleanup, source, appRoot, home, "down", "-o", "json")
		returnErr = errors.Join(returnErr, cleanupErr)
	}()
	if _, err := runHarnessAppCLI(ctx, source, appRoot, home, "up", "--detach", "--wait", "ready", "-o", "json"); err != nil {
		return err
	}
	assertions["A3_product_up_without_repository_tools"] = true
	output, err := runHarnessAppCLI(ctx, source, appRoot, home, "harness", "self", "-o", "json")
	var envelope struct {
		OK          bool `json:"ok"`
		Diagnostics []struct {
			Code string `json:"code"`
		} `json:"diagnostics"`
	}
	if err == nil || json.Unmarshal(output, &envelope) != nil || envelope.OK || len(envelope.Diagnostics) != 1 || envelope.Diagnostics[0].Code != "SCN8001" {
		return fmt.Errorf("removed repository grammar did not fail as invalid_request: %s (%v)", tailString(string(output), 2048), err)
	}
	assertions["A3_removed_grammar_rejected"] = true
	return nil
}
