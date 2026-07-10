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
	flag.IntVar(&opts.PackageParallelism, "p", 3, "parallel test packages")
	flag.IntVar(&opts.BuildParallelism, "build-p", 8, "parallel missing binary builds")
	flag.BoolVar(&opts.RefreshManifest, "refresh", false, "force Go build-ID refresh")
	flag.BoolVar(&opts.RecordTimings, "record-timings", true, "record package durations for longest-first scheduling")
	flag.Parse()
	root, err := filepath.Abs(opts.RepoRoot)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	opts.RepoRoot = root
	opts.Output = os.Stdout
	if _, err := testsuite.Run(context.Background(), opts); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
