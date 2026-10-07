package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunReusesPreparedManifestButExecutesPackagesFreshInProcess(t *testing.T) {
	t.Parallel()

	prepareCalls := 0
	executionCalls := 0
	timingWrites := 0
	manifest := cacheManifest{
		Packages:       []testPackage{{Dir: "/repo/a", ImportPath: "example.com/testsuitefixture/a", BuildID: "fixture-build-id", Binary: "/cache/a.test"}},
		NoTestPackages: []string{"example.com/testsuitefixture/b"},
	}
	deps := runDependencies{
		prepare: func(context.Context, Options) (cacheManifest, bool, PrepareTiming, error) {
			prepareCalls++
			if prepareCalls == 1 {
				return manifest, false, PrepareTiming{Builds: []BinaryBuild{{Package: "example.com/testsuitefixture/a", BuildID: "fixture-build-id"}}}, nil
			}
			return manifest, true, PrepareTiming{}, nil
		},
		runPackages: func(_ context.Context, _ Options, packages []testPackage) []packageRun {
			executionCalls++
			if len(packages) != 1 || packages[0].ImportPath != "example.com/testsuitefixture/a" {
				t.Fatalf("packages = %+v", packages)
			}
			return []packageRun{{
				Package: packages[0],
				Output:  fixtureNativeEvents(t, packages[0].ImportPath),
				Action:  "pass",
			}}
		},
		loadTimingEstimates: func(string) map[string]float64 { return nil },
		writeTimings: func(path string, estimates map[string]float64) error {
			timingWrites++
			if path != "/cache/timings.json" || estimates["example.com/testsuitefixture/a"] != 0 {
				t.Fatalf("timing write path=%q estimates=%v", path, estimates)
			}
			return nil
		},
	}
	run := func() (Result, []byte) {
		var output bytes.Buffer
		result, err := runWithDependencies(context.Background(), Options{
			CacheDir: "/cache", BuildParallelism: 2, RecordTimings: true, Output: &output,
		}, deps)
		if err != nil {
			t.Fatal(err)
		}
		return result, output.Bytes()
	}
	first, firstOutput := run()
	if first.ManifestHit || first.BuiltCount != 1 || first.BuildParallelism != 2 || first.PackageCount != 2 || first.TestPackageCount != 1 || first.TestResultCount != 2 {
		t.Fatalf("first result = %+v", first)
	}
	assertTestEvents(t, firstOutput, true)
	second, secondOutput := run()
	if !second.ManifestHit || second.BuiltCount != 0 || second.TestResultCount != 2 {
		t.Fatalf("second result = %+v", second)
	}
	assertTestEvents(t, secondOutput, true)
	if prepareCalls != 2 || executionCalls != 2 || timingWrites != 2 {
		t.Fatalf("calls prepare=%d execute=%d timing_writes=%d, want 2 each", prepareCalls, executionCalls, timingWrites)
	}
}

func TestNormalizeOptionsPinsDefaultParallelism(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(Options{RepoRoot: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if DefaultPackageParallelism != 6 {
		t.Fatalf("default package parallelism = %d, want 6", DefaultPackageParallelism)
	}
	if opts.PackageParallelism != DefaultPackageParallelism {
		t.Fatalf("package parallelism = %d, want pinned default 6", opts.PackageParallelism)
	}
	if DefaultBuildParallelism != 4 {
		t.Fatalf("default build parallelism = %d, want 4", DefaultBuildParallelism)
	}
	if opts.BuildParallelism != DefaultBuildParallelism {
		t.Fatalf("build parallelism = %d, want pinned default 4", opts.BuildParallelism)
	}
}

func TestWorkspaceFingerprintTracksContentNotCommitStateInProcess(t *testing.T) {
	repoRoot := t.TempDir()
	writeFixtureFile(t, repoRoot, "go.mod", "module example.com/fingerprint\n\ngo 1.26.3\n")
	writeFixtureFile(t, repoRoot, "value.go", "package fingerprint\n\nconst Value = 1\n")
	paths := []byte("go.mod\x00value.go\x00")
	before, err := workspaceFingerprintFromPaths(repoRoot, paths)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repoRoot, "value.go", "package fingerprint\n\nconst Value = 2\n")
	after, err := workspaceFingerprintFromPaths(repoRoot, paths)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("dirty tracked source did not invalidate workspace fingerprint")
	}
	committed, err := workspaceFingerprintFromPaths(repoRoot, paths)
	if err != nil {
		t.Fatal(err)
	}
	if committed != after {
		t.Fatal("committing unchanged workspace content changed its fingerprint")
	}
}

func TestSortTestPackagesUsesLongestFirstThenName(t *testing.T) {
	packages := []testPackage{{ImportPath: "z"}, {ImportPath: "a"}, {ImportPath: "b"}}
	sortTestPackages(packages, map[string]float64{"z": 1, "a": 2, "b": 2})
	got := []string{packages[0].ImportPath, packages[1].ImportPath, packages[2].ImportPath}
	if strings.Join(got, ",") != "a,b,z" {
		t.Fatalf("order = %v", got)
	}
}

func TestWorkspaceFingerprintTracksSelectedGoTool(t *testing.T) {
	root := t.TempDir()
	tool := filepath.Join(root, "go")
	if err := os.WriteFile(tool, []byte("first tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root)
	before, err := workspaceFingerprintFromPaths(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, []byte("replacement tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	after, err := workspaceFingerprintFromPaths(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("replacing the selected Go tool did not invalidate the fingerprint")
	}
}

func TestTestBinaryCommandsDisableVCSStamping(t *testing.T) {
	if got := strings.Join(testPackageListArgs(), " "); got != "list -buildvcs=false -test -export -json ./..." {
		t.Fatalf("go list args = %q", got)
	}
	if got := strings.Join(testBinaryBuildArgs("/tmp/pkg.test", "example.com/pkg"), " "); got != "test -c -buildvcs=false -o /tmp/pkg.test example.com/pkg" {
		t.Fatalf("go test args = %q", got)
	}
}

func assertTestEvents(t *testing.T, output []byte, wantNoTestPackage bool) {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(output))
	seenTest, seenNoTest := false, false
	for decoder.More() {
		var event testEvent
		if err := decoder.Decode(&event); err != nil {
			t.Fatal(err)
		}
		if event.Package == "example.com/testsuitefixture/a" && event.Action == "pass" && event.Test == "TestTestsuiteHelperProcess" {
			seenTest = true
		}
		if event.Package == "example.com/testsuitefixture/b" && event.Action == "skip" {
			seenNoTest = true
		}
	}
	if !seenTest || seenNoTest != wantNoTestPackage {
		t.Fatalf("events missing: test=%v no_test=%v", seenTest, seenNoTest)
	}
}

func writeFixtureFile(t *testing.T, root, relativePath, contents string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fixtureNativeEvents(t *testing.T, pkg string) []byte {
	t.Helper()
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	now := time.Date(2026, 10, 7, 0, 0, 0, 0, time.UTC)
	for _, event := range []testEvent{
		{Time: now, Package: pkg, Action: "start"},
		{Time: now, Package: pkg, Action: "run", Test: "TestTestsuiteHelperProcess"},
		{Time: now, Package: pkg, Action: "run", Test: "TestTestsuiteHelperProcess/child"},
		{Time: now, Package: pkg, Action: "pass", Test: "TestTestsuiteHelperProcess/child"},
		{Time: now, Package: pkg, Action: "pass", Test: "TestTestsuiteHelperProcess"},
		{Time: now, Package: pkg, Action: "pass"},
	} {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return output.Bytes()
}

func TestFailedPreparationRetainsMeasuredCosts(t *testing.T) {
	t.Parallel()
	failure := errors.New("link failed")
	result, err := runWithDependencies(context.Background(), Options{BuildParallelism: 4}, runDependencies{
		prepare: func(context.Context, Options) (cacheManifest, bool, PrepareTiming, error) {
			return cacheManifest{Packages: []testPackage{{ImportPath: "example.com/a"}}}, false, PrepareTiming{Elapsed: 2 * time.Second, ListElapsed: time.Second, Builds: []BinaryBuild{{Package: "example.com/a", BuildID: "id", Elapsed: time.Second, Outcome: "failed", Error: failure.Error()}}}, failure
		},
	})
	if !errors.Is(err, failure) || result.BuildParallelism != 4 || result.Prepare.Elapsed != 2*time.Second || len(result.Prepare.Builds) != 1 || result.BuiltCount != 0 || result.TestPackageCount != 1 {
		t.Fatalf("failed preparation=%+v err=%v", result, err)
	}
}

func TestNativeEventOutputPreservesSplitRecordsAndTimestamps(t *testing.T) {
	t.Parallel()
	var output bytes.Buffer
	stream := testEventOutput{sink: &lockedEventOutput{writer: &output}}
	events := fixtureNativeEvents(t, "example.com/a")
	for _, chunk := range [][]byte{events[:13], events[13:31], events[31:]} {
		if _, err := stream.Write(chunk); err != nil {
			t.Fatal(err)
		}
	}
	if err := stream.finish(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), events) {
		t.Fatalf("changed native records: %s", output.Bytes())
	}
	if _, err := writePackageEvents(json.NewEncoder(&output), packageRun{Package: testPackage{ImportPath: "example.com/a"}, Output: events, Streamed: true}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(output.Bytes(), events) {
		t.Fatal("streamed records were duplicated")
	}
	if _, err := stream.Write([]byte("{")); err != nil {
		t.Fatal(err)
	}
	if err := stream.finish(); err == nil {
		t.Fatal("truncated record accepted")
	}
}
