package testsuite

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeGoModeEnv = "TESTSUITE_FAKE_GO"

func TestMain(m *testing.M) {
	if os.Getenv(fakeGoModeEnv) == "1" && filepath.Base(os.Args[0]) == "go" {
		os.Exit(runFakeGo())
	}
	os.Exit(m.Run())
}

func TestTestsuiteHelperProcess(t *testing.T) {
	if os.Getenv(fakeGoModeEnv) != "1" {
		t.Skip("helper process")
	}
	if err := os.WriteFile(os.Getenv("TESTSUITE_MARKER"), []byte("ran"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Run("child", func(t *testing.T) {})
}

func TestRunCachesLinkedBinariesAndExecutesTestsFresh(t *testing.T) {
	repoRoot := t.TempDir()
	writeFixtureFile(t, repoRoot, "go.mod", "module example.com/testsuitefixture\n\ngo 1.26.3\n")
	writeFixtureFile(t, repoRoot, "a/a.go", "package a\n\nfunc Value() int { return 1 }\n")
	writeFixtureFile(t, repoRoot, "b/b.go", "package b\n\nfunc Value() int { return 2 }\n")
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "testsuite@example.com")
	runGit(t, repoRoot, "config", "user.name", "Testsuite")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "fixture")

	marker := filepath.Join(t.TempDir(), "marker")
	cacheDir := filepath.Join(t.TempDir(), "cache")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	fakeBin := t.TempDir()
	copyExecutable(t, self, filepath.Join(fakeBin, "go"))
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(fakeGoModeEnv, "1")
	t.Setenv("TESTSUITE_FAKE_REPO", repoRoot)
	t.Setenv("TESTSUITE_FAKE_SELF", self)
	t.Setenv("TESTSUITE_MARKER", marker)
	env := os.Environ()
	var firstOutput bytes.Buffer
	first, err := Run(context.Background(), Options{
		RepoRoot: repoRoot, CacheDir: cacheDir, RunPattern: "^TestTestsuiteHelperProcess$",
		PackageParallelism: 2, BuildParallelism: 2, RefreshManifest: true,
		RecordTimings: true, Output: &firstOutput, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ManifestHit || first.BuiltCount != 1 || first.PackageCount != 2 || first.TestResultCount != 2 {
		t.Fatalf("first result = %+v", first)
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "ran" {
		t.Fatalf("marker = %q, %v", data, err)
	}
	assertTestEvents(t, firstOutput.Bytes(), true)

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	var secondOutput bytes.Buffer
	second, err := Run(context.Background(), Options{
		RepoRoot: repoRoot, CacheDir: cacheDir, RunPattern: "^TestTestsuiteHelperProcess$",
		PackageParallelism: 2, BuildParallelism: 2,
		RecordTimings: true, Output: &secondOutput, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !second.ManifestHit || second.BuiltCount != 0 || second.TestResultCount != 2 {
		t.Fatalf("second result = %+v", second)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("fresh test execution did not recreate marker: %v", err)
	}

}

func TestRunPatternCanCompileWithoutExecutingTests(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(t.TempDir(), "marker")
	env := append(os.Environ(), fakeGoModeEnv+"=1", "TESTSUITE_MARKER="+marker)
	runs := runPackages(context.Background(), Options{
		RunPattern: "a^", PackageParallelism: 1, Env: env,
	}, []testPackage{{Dir: t.TempDir(), ImportPath: "example.com/compileonly", Binary: self}})
	if len(runs) != 1 || runs[0].Err != nil {
		t.Fatalf("compile-only runs = %+v", runs)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("compile-only run executed test, marker stat = %v", err)
	}
}

func TestWorkspaceFingerprintChangesForDirtyTrackedSource(t *testing.T) {
	repoRoot := t.TempDir()
	writeFixtureFile(t, repoRoot, "go.mod", "module example.com/fingerprint\n\ngo 1.26.3\n")
	writeFixtureFile(t, repoRoot, "value.go", "package fingerprint\n\nconst Value = 1\n")
	runGit(t, repoRoot, "init")
	runGit(t, repoRoot, "config", "user.email", "testsuite@example.com")
	runGit(t, repoRoot, "config", "user.name", "Testsuite")
	runGit(t, repoRoot, "add", ".")
	runGit(t, repoRoot, "commit", "-m", "fixture")
	before, err := workspaceFingerprint(context.Background(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, repoRoot, "value.go", "package fingerprint\n\nconst Value = 2\n")
	after, err := workspaceFingerprint(context.Background(), repoRoot)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("dirty tracked source did not invalidate workspace fingerprint")
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

func runFakeGo() int {
	args := os.Args[1:]
	if len(args) > 0 && args[0] == "list" {
		root := os.Getenv("TESTSUITE_FAKE_REPO")
		encoder := json.NewEncoder(os.Stdout)
		for _, pkg := range []listedPackage{
			{Dir: filepath.Join(root, "a"), ImportPath: "example.com/testsuitefixture/a"},
			{Dir: filepath.Join(root, "b"), ImportPath: "example.com/testsuitefixture/b"},
			{Dir: filepath.Join(root, "a"), ImportPath: "example.com/testsuitefixture/a.test", BuildID: "fixture-build-id"},
		} {
			if err := encoder.Encode(pkg); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
		return 0
	}
	if len(args) > 0 && args[0] == "test" {
		for i := 1; i+1 < len(args); i++ {
			if args[i] == "-o" {
				if err := linkOrCopyFile(os.Getenv("TESTSUITE_FAKE_SELF"), args[i+1]); err != nil {
					fmt.Fprintln(os.Stderr, err)
					return 1
				}
				return 0
			}
		}
	}
	fmt.Fprintf(os.Stderr, "unexpected fake go command: %v\n", args)
	return 1
}

func copyExecutable(t *testing.T, source, target string) {
	t.Helper()
	if err := linkOrCopyFile(source, target); err != nil {
		t.Fatal(err)
	}
}

func linkOrCopyFile(source, target string) error {
	if err := os.Remove(target); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Link(source, target); err == nil {
		return nil
	}
	return copyFile(source, target)
}

func copyFile(source, target string) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
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

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v: %s", strings.Join(args, " "), err, output)
	}
}
