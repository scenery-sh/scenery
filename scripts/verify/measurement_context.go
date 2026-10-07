package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"scenery.sh/internal/harnessreport"
	"scenery.sh/internal/testsuite"
)

func measurementContext(root, runner, version, workload, cache string, packages, builds int) harnessreport.MeasurementContext {
	host, _ := os.Hostname()
	result := harnessreport.MeasurementContext{Environment: "local", Host: host, OS: runtime.GOOS, Architecture: runtime.GOARCH,
		Runner: runner, RunnerVersion: version, CPUs: runtime.NumCPU(), PackageParallelism: packages, BuildParallelism: builds, Workload: workload, ResultCache: cache}
	data, err := os.ReadFile(filepath.Join(root, ".scenery", "harness", "ci-context.json"))
	if err == nil {
		var ci harnessreport.CIContext
		if json.Unmarshal(data, &ci) == nil && ci.RunID != "" && ci.Attempt > 0 {
			result.Environment, result.CI = "ci", &ci
		}
	}
	return result
}

func qualifyGoTimingContext(ctx context.Context, root string, report *harnessTestTimingReport, artifacts harnessArtifactContext, fresh bool) {
	report.Provenance = harnessreport.CurrentProvenance()
	version, err := exec.CommandContext(ctx, "go", "version").Output()
	if err != nil {
		report.Discovery.Complete = false
		report.Discovery.ParserErrors = append(report.Discovery.ParserErrors, "Go runner identity unavailable")
	}
	cache := "Go result cache allowed; replay is classified per package"
	if fresh {
		cache = "reusable binaries; fresh isolated test processes; no result replay"
	}
	workload := fmt.Sprintf("./...; test roots; GOWORK=off; default test.parallel=%d", runtime.GOMAXPROCS(0))
	if fresh {
		workload += "; binary preparation buildvcs=false; isolated confirmation test.parallel=1"
	}
	report.Context = measurementContext(root, "go", strings.TrimSpace(string(version)), workload, cache, runtime.GOMAXPROCS(0), runtime.GOMAXPROCS(0))
	if fresh {
		report.Context.PackageParallelism, report.Context.BuildParallelism = testsuite.DefaultPackageParallelism, testsuite.DefaultBuildParallelism
	}
	// CI run IDs vary between observations and are not part of the cohort.
	report.RunID = artifacts.RunID
	commit, commitErr := runHarnessGit(ctx, root, "rev-parse", "HEAD")
	report.SourceCommit = strings.TrimSpace(commit)
	input, inputErr := harnessInputRevision(ctx, root)
	report.InputRevision = input
	if commitErr != nil || inputErr != nil {
		report.Discovery.Complete = false
		report.Discovery.ParserErrors = append(report.Discovery.ParserErrors, "source identity unavailable")
	}
}
