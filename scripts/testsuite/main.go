package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/testsuite"
)

func main() {
	var opts testsuite.Options
	flag.StringVar(&opts.RepoRoot, "repo-root", ".", "repository root")
	flag.StringVar(&opts.CacheDir, "cache", ".scenery/harness/test-binaries", "linked test binary cache")
	flag.StringVar(&opts.RunPattern, "run", ".*", "test name pattern")
	flag.IntVar(&opts.PackageParallelism, "p", testsuite.DefaultPackageParallelism, "parallel test packages")
	flag.IntVar(&opts.BuildParallelism, "build-p", testsuite.DefaultBuildParallelism, "parallel missing binary builds")
	flag.BoolVar(&opts.RefreshManifest, "refresh", false, "force Go build-ID refresh")
	flag.BoolVar(&opts.RecordTimings, "record-timings", true, "record package durations for longest-first scheduling")
	builds := flag.Int("builds", 0, "print the N slowest test-binary builds to stderr")
	flag.Parse()
	root, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts.RepoRoot = root
	opts.Output = os.Stdout
	result, runErr := testsuite.Run(context.Background(), opts)
	reportPrepare(result, *builds)
	if runErr != nil {
		fmt.Fprintln(os.Stderr, runErr)
		os.Exit(1)
	}
}

// reportPrepare prints the pre-execution breakdown on stderr so it stays out of
// the Go JSON event stream on stdout.
func reportPrepare(result testsuite.Result, builds int) {
	fmt.Fprintf(os.Stderr, "prepare %.3fs (list %.3fs, %d/%d binaries built at build-p=%d, summed build elapsed %.3fs, manifest_hit=%v)\n",
		result.Prepare.Elapsed.Seconds(), result.Prepare.ListElapsed.Seconds(),
		len(result.Prepare.Builds), result.TestPackageCount, result.BuildParallelism,
		result.Prepare.AggregateBuildElapsed().Seconds(), result.ManifestHit)
	for i, build := range result.Prepare.Builds {
		if i >= builds {
			break
		}
		fmt.Fprintf(os.Stderr, "  build %6.3fs %s %s\n", build.Elapsed.Seconds(), build.Package, build.BuildID)
	}
}
