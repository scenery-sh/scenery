// Package testsuite executes every repository test from content-addressed Go
// test binaries. It preserves fresh test execution while avoiding repeated
// linking when package build IDs have not changed.
package testsuite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/envpolicy"
)

const (
	// DefaultPackageParallelism is shared by the harness and manual adapter so
	// warm execution timings remain comparable across entrypoints.
	DefaultPackageParallelism = 6
	// DefaultBuildParallelism is shared by the harness and manual adapter so
	// cold-prepare wall timings remain comparable across entrypoints.
	DefaultBuildParallelism = 4
)

type Options struct {
	RepoRoot           string
	CacheDir           string
	RunPattern         string
	PackageParallelism int
	BuildParallelism   int
	RefreshManifest    bool
	RecordTimings      bool
	Output             io.Writer
	Env                []string
}

type Result struct {
	PackageCount     int
	TestPackageCount int
	TestResultCount  int
	BuiltCount       int
	BuildParallelism int
	ManifestHit      bool
	Packages         []PackageTiming
	Prepare          PrepareTiming
}

type PackageTiming struct {
	Package string
	Elapsed time.Duration
}

// PrepareTiming attributes the pre-execution cost of a fresh run. A cold run
// pays for package listing and for linking one test binary per package; both
// are invisible in Go's per-package test elapsed times, so they must be
// measured here to be attributable at all.
type PrepareTiming struct {
	Elapsed     time.Duration
	ListElapsed time.Duration
	Builds      []BinaryBuild
}

// BinaryBuild records one linked test binary. BuildID is the Go test build ID
// the binary is content-addressed by, so a rebuild can be traced to the input
// change that produced a new identity.
type BinaryBuild struct {
	Package string
	BuildID string
	Elapsed time.Duration
}

// AggregateBuildElapsed sums the elapsed duration observed for each build.
// Concurrent builds overlap, so this is attribution data, not wall time or CPU
// time. PrepareTiming.Elapsed is the comparable user-visible preparation cost.
func (t PrepareTiming) AggregateBuildElapsed() time.Duration {
	var total time.Duration
	for _, build := range t.Builds {
		total += build.Elapsed
	}
	return total
}

type packageRun struct {
	Package testPackage
	Elapsed time.Duration
	Output  []byte
	Action  string
	Err     error
}

type runDependencies struct {
	prepare             func(context.Context, Options) (cacheManifest, bool, PrepareTiming, error)
	runPackages         func(context.Context, Options, []testPackage) []packageRun
	loadTimingEstimates func(string) map[string]float64
	writeTimings        func(string, map[string]float64) error
}

func Run(ctx context.Context, opts Options) (Result, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}
	return runWithDependencies(ctx, opts, runDependencies{
		prepare:             prepare,
		runPackages:         runPackages,
		loadTimingEstimates: loadTimingEstimates,
		writeTimings:        writeTimingEstimates,
	})
}

func runWithDependencies(ctx context.Context, opts Options, deps runDependencies) (Result, error) {
	manifest, hit, prepared, err := deps.prepare(ctx, opts)
	if err != nil {
		return Result{}, err
	}

	estimates := deps.loadTimingEstimates(filepath.Join(opts.CacheDir, "timings.json"))
	sortTestPackages(manifest.Packages, estimates)
	runs := deps.runPackages(ctx, opts, manifest.Packages)
	result := Result{
		PackageCount:     len(manifest.Packages) + len(manifest.NoTestPackages),
		TestPackageCount: len(manifest.Packages),
		BuiltCount:       len(prepared.Builds),
		BuildParallelism: opts.BuildParallelism,
		ManifestHit:      hit,
		Prepare:          prepared,
	}
	var runErrors []error
	for _, run := range runs {
		result.Packages = append(result.Packages, PackageTiming{Package: run.Package.ImportPath, Elapsed: run.Elapsed})
		if run.Err != nil {
			runErrors = append(runErrors, fmt.Errorf("test %s: %w", run.Package.ImportPath, run.Err))
		}
	}
	sort.Slice(result.Packages, func(i, j int) bool { return result.Packages[i].Package < result.Packages[j].Package })
	result.TestResultCount, err = writeJSONOutput(opts.Output, runs, manifest.NoTestPackages)
	if err != nil {
		runErrors = append(runErrors, err)
	}
	if len(runErrors) == 0 && opts.RecordTimings {
		estimates = make(map[string]float64, len(runs))
		for _, run := range runs {
			estimates[run.Package.ImportPath] = run.Elapsed.Seconds()
		}
		if err := deps.writeTimings(filepath.Join(opts.CacheDir, "timings.json"), estimates); err != nil {
			runErrors = append(runErrors, err)
		}
	}
	return result, errors.Join(runErrors...)
}

func normalizeOptions(opts Options) (Options, error) {
	if strings.TrimSpace(opts.RepoRoot) == "" {
		return Options{}, fmt.Errorf("repository root is required")
	}
	root, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		return Options{}, err
	}
	opts.RepoRoot = root
	if strings.TrimSpace(opts.CacheDir) == "" {
		opts.CacheDir = filepath.Join(root, ".scenery", "harness", "test-binaries")
	} else if !filepath.IsAbs(opts.CacheDir) {
		opts.CacheDir = filepath.Join(root, opts.CacheDir)
	}
	if opts.RunPattern == "" {
		opts.RunPattern = ".*"
	}
	if opts.PackageParallelism <= 0 {
		opts.PackageParallelism = DefaultPackageParallelism
	}
	if opts.BuildParallelism <= 0 {
		opts.BuildParallelism = DefaultBuildParallelism
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Env == nil {
		opts.Env = envpolicy.Environ()
	}
	opts.Env = environmentWithOverride(opts.Env, "GOWORK", "off")
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func environmentWithOverride(environment []string, name, value string) []string {
	prefix := name + "="
	result := make([]string, 0, len(environment)+1)
	for _, entry := range environment {
		if !strings.HasPrefix(entry, prefix) {
			result = append(result, entry)
		}
	}
	return append(result, prefix+value)
}

func prepare(ctx context.Context, opts Options) (cacheManifest, bool, PrepareTiming, error) {
	started := time.Now()
	timing := PrepareTiming{}
	fail := func(err error) (cacheManifest, bool, PrepareTiming, error) {
		timing.Elapsed = time.Since(started)
		return cacheManifest{}, false, timing, err
	}
	unlock, err := lockCache(ctx, filepath.Join(opts.CacheDir, "cache.lock"))
	if err != nil {
		return fail(err)
	}
	defer unlock()
	fingerprint, err := workspaceFingerprint(ctx, opts.RepoRoot)
	if err != nil {
		return fail(err)
	}
	manifestPath := filepath.Join(opts.CacheDir, "manifest.json")
	manifest, hit := readManifest(manifestPath, fingerprint, opts.RefreshManifest)
	if !hit {
		listStarted := time.Now()
		manifest, err = listTestPackages(ctx, opts.RepoRoot, opts.CacheDir, fingerprint, opts.Env)
		timing.ListElapsed = time.Since(listStarted)
		if err != nil {
			return fail(err)
		}
	}
	timing.Builds, err = buildMissingBinaries(ctx, opts, manifest.Packages)
	if err != nil {
		return fail(err)
	}
	if !hit || len(timing.Builds) > 0 {
		if current, err := workspaceFingerprint(ctx, opts.RepoRoot); err != nil {
			return fail(err)
		} else if current != fingerprint {
			return fail(fmt.Errorf("repository inputs changed while preparing test binaries"))
		}
	}
	if !hit {
		if err := writeManifest(manifestPath, manifest); err != nil {
			return fail(err)
		}
		pruneUnreferencedBinaries(opts.CacheDir, manifest.Packages)
	}
	timing.Elapsed = time.Since(started)
	return manifest, hit, timing, nil
}

func buildMissingBinaries(ctx context.Context, opts Options, packages []testPackage) ([]BinaryBuild, error) {
	var missing []testPackage
	for _, pkg := range packages {
		if _, err := os.Stat(pkg.Binary); err != nil {
			missing = append(missing, pkg)
		}
	}
	var mu sync.Mutex
	builds := make([]BinaryBuild, 0, len(missing))
	errs := parallelPackages(missing, opts.BuildParallelism, func(pkg testPackage) error {
		temp, err := os.CreateTemp(opts.CacheDir, ".test-binary-*.tmp")
		if err != nil {
			return err
		}
		tempPath := temp.Name()
		if err := temp.Close(); err != nil {
			return err
		}
		defer func() { _ = os.Remove(tempPath) }()
		started := time.Now()
		cmd := exec.CommandContext(ctx, "go", testBinaryBuildArgs(tempPath, pkg.ImportPath)...)
		configureCommandCancellation(cmd)
		cmd.Dir = opts.RepoRoot
		cmd.Env = opts.Env
		output, err := cmd.CombinedOutput()
		elapsed := time.Since(started)
		if err != nil {
			return fmt.Errorf("build %s: %w: %s", pkg.ImportPath, err, strings.TrimSpace(string(output)))
		}
		if err := os.Chmod(tempPath, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tempPath, pkg.Binary); err != nil {
			return err
		}
		mu.Lock()
		builds = append(builds, BinaryBuild{Package: pkg.ImportPath, BuildID: pkg.BuildID, Elapsed: elapsed})
		mu.Unlock()
		return nil
	})
	sort.Slice(builds, func(i, j int) bool {
		if builds[i].Elapsed == builds[j].Elapsed {
			return builds[i].Package < builds[j].Package
		}
		return builds[i].Elapsed > builds[j].Elapsed
	})
	return builds, errors.Join(errs...)
}

func testBinaryBuildArgs(output, importPath string) []string {
	return []string{"test", "-c", "-buildvcs=false", "-o", output, importPath}
}

func runPackages(ctx context.Context, opts Options, packages []testPackage) []packageRun {
	jobs := make(chan testPackage)
	results := make(chan packageRun, len(packages))
	var wg sync.WaitGroup
	for range opts.PackageParallelism {
		wg.Go(func() {
			for pkg := range jobs {
				started := time.Now()
				cmd := exec.CommandContext(ctx, pkg.Binary,
					"-test.v",
					"-test.run", opts.RunPattern,
					"-test.count=1",
					"-test.timeout=10m",
					"-test.paniconexit0",
				)
				configureCommandCancellation(cmd)
				cmd.Dir = pkg.Dir
				cmd.Env = opts.Env
				var output bytes.Buffer
				cmd.Stdout = &output
				cmd.Stderr = &output
				err := cmd.Run()
				action := "pass"
				if err != nil {
					action = "fail"
				}
				results <- packageRun{
					Package: pkg,
					Elapsed: time.Since(started),
					Output:  append([]byte(nil), output.Bytes()...),
					Action:  action,
					Err:     err,
				}
			}
		})
	}
	go func() {
		for _, pkg := range packages {
			jobs <- pkg
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()
	runs := make([]packageRun, 0, len(packages))
	for result := range results {
		runs = append(runs, result)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].Package.ImportPath < runs[j].Package.ImportPath })
	return runs
}

func parallelPackages(packages []testPackage, limit int, fn func(testPackage) error) []error {
	jobs := make(chan testPackage)
	errs := make(chan error, len(packages))
	var wg sync.WaitGroup
	for range limit {
		wg.Go(func() {
			for pkg := range jobs {
				if err := fn(pkg); err != nil {
					errs <- err
				}
			}
		})
	}
	for _, pkg := range packages {
		jobs <- pkg
	}
	close(jobs)
	wg.Wait()
	close(errs)
	var collected []error
	for err := range errs {
		collected = append(collected, err)
	}
	return collected
}
