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
	"sync/atomic"
	"time"

	"scenery.sh/internal/envpolicy"
)

const (
	defaultPackageParallelism = 3
	defaultBuildParallelism   = 8
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
	PackageCount    int
	TestResultCount int
	BuiltCount      int
	ManifestHit     bool
	Packages        []PackageTiming
}

type PackageTiming struct {
	Package string
	Elapsed time.Duration
}

type packageRun struct {
	Package testPackage
	Elapsed time.Duration
	Output  []byte
	Action  string
	Err     error
}

func Run(ctx context.Context, opts Options) (Result, error) {
	opts, err := normalizeOptions(opts)
	if err != nil {
		return Result{}, err
	}
	manifest, hit, built, err := prepare(ctx, opts)
	if err != nil {
		return Result{}, err
	}

	estimates := loadTimingEstimates(filepath.Join(opts.CacheDir, "timings.json"))
	sortTestPackages(manifest.Packages, estimates)
	runs := runPackages(ctx, opts, manifest.Packages)
	result := Result{
		PackageCount: len(manifest.Packages) + len(manifest.NoTestPackages),
		BuiltCount:   built,
		ManifestHit:  hit,
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
		if err := writeTimingEstimates(filepath.Join(opts.CacheDir, "timings.json"), estimates); err != nil {
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
		opts.PackageParallelism = defaultPackageParallelism
	}
	if opts.BuildParallelism <= 0 {
		opts.BuildParallelism = defaultBuildParallelism
	}
	if opts.Output == nil {
		opts.Output = io.Discard
	}
	if opts.Env == nil {
		opts.Env = envpolicy.Environ()
	}
	if err := os.MkdirAll(opts.CacheDir, 0o755); err != nil {
		return Options{}, err
	}
	return opts, nil
}

func prepare(ctx context.Context, opts Options) (cacheManifest, bool, int, error) {
	unlock, err := lockCache(ctx, filepath.Join(opts.CacheDir, "cache.lock"))
	if err != nil {
		return cacheManifest{}, false, 0, err
	}
	defer unlock()
	fingerprint, err := workspaceFingerprint(ctx, opts.RepoRoot)
	if err != nil {
		return cacheManifest{}, false, 0, err
	}
	manifestPath := filepath.Join(opts.CacheDir, "manifest.json")
	manifest, hit := readManifest(manifestPath, fingerprint, opts.RefreshManifest)
	if !hit {
		manifest, err = listTestPackages(ctx, opts.RepoRoot, opts.CacheDir, fingerprint, opts.Env)
		if err != nil {
			return cacheManifest{}, false, 0, err
		}
	}
	built, err := buildMissingBinaries(ctx, opts, manifest.Packages)
	if err != nil {
		return cacheManifest{}, false, built, err
	}
	if !hit || built > 0 {
		if current, err := workspaceFingerprint(ctx, opts.RepoRoot); err != nil {
			return cacheManifest{}, false, built, err
		} else if current != fingerprint {
			return cacheManifest{}, false, built, fmt.Errorf("repository inputs changed while preparing test binaries")
		}
	}
	if !hit {
		if err := writeManifest(manifestPath, manifest); err != nil {
			return cacheManifest{}, false, built, err
		}
		pruneUnreferencedBinaries(opts.CacheDir, manifest.Packages)
	}
	return manifest, hit, built, nil
}

func buildMissingBinaries(ctx context.Context, opts Options, packages []testPackage) (int, error) {
	var missing []testPackage
	for _, pkg := range packages {
		if _, err := os.Stat(pkg.Binary); err != nil {
			missing = append(missing, pkg)
		}
	}
	var built atomic.Int64
	errs := parallelPackages(missing, opts.BuildParallelism, func(pkg testPackage) error {
		temp, err := os.CreateTemp(opts.CacheDir, ".test-binary-*.tmp")
		if err != nil {
			return err
		}
		tempPath := temp.Name()
		if err := temp.Close(); err != nil {
			return err
		}
		defer os.Remove(tempPath)
		cmd := exec.CommandContext(ctx, "go", "test", "-c", "-o", tempPath, pkg.ImportPath)
		configureCommandCancellation(cmd)
		cmd.Dir = opts.RepoRoot
		cmd.Env = opts.Env
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("build %s: %w: %s", pkg.ImportPath, err, strings.TrimSpace(string(output)))
		}
		if err := os.Chmod(tempPath, 0o755); err != nil {
			return err
		}
		if err := os.Rename(tempPath, pkg.Binary); err != nil {
			return err
		}
		built.Add(1)
		return nil
	})
	return int(built.Load()), errors.Join(errs...)
}

func runPackages(ctx context.Context, opts Options, packages []testPackage) []packageRun {
	jobs := make(chan testPackage)
	results := make(chan packageRun, len(packages))
	var wg sync.WaitGroup
	for range opts.PackageParallelism {
		wg.Add(1)
		go func() {
			defer wg.Done()
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
		}()
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
		wg.Add(1)
		go func() {
			defer wg.Done()
			for pkg := range jobs {
				if err := fn(pkg); err != nil {
					errs <- err
				}
			}
		}()
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
