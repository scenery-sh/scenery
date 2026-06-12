package scenery_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLatestIntegrationSourceModTimeIncludesEmbeddedNonGoInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIntegrationHelperTestFile(t, root, "go.mod", "module scenery.sh\n")
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions.json", `{"grafana":"1.0.0"}`)
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions_test.go", "package devtools\n")
	writeIntegrationHelperTestFile(t, root, "internal/devtools/node_modules/ignored.json", `{"ignored":true}`)

	oldTime := time.Unix(1_700_000_000, 0)
	embedTime := oldTime.Add(1 * time.Hour)
	testTime := embedTime.Add(1 * time.Hour)
	ignoredTime := testTime.Add(1 * time.Hour)
	for path, modTime := range map[string]time.Time{
		filepath.Join(root, "go.mod"):                                      oldTime,
		filepath.Join(root, "internal/devtools/versions.json"):             embedTime,
		filepath.Join(root, "internal/devtools/versions_test.go"):          testTime,
		filepath.Join(root, "internal/devtools/node_modules/ignored.json"): ignoredTime,
	} {
		if err := os.Chtimes(path, modTime, modTime); err != nil {
			t.Fatalf("Chtimes(%s): %v", path, err)
		}
	}

	latest, ok, err := latestIntegrationSourceModTime(root)
	if err != nil {
		t.Fatalf("latestIntegrationSourceModTime() error = %v", err)
	}
	if !ok {
		t.Fatal("latestIntegrationSourceModTime() ok = false")
	}
	if !latest.Equal(embedTime) {
		t.Fatalf("latest source time = %s, want embedded non-Go time %s", latest, embedTime)
	}
}

func TestIntegrationSourceFingerprintIncludesEmbeddedNonGoInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIntegrationHelperTestFile(t, root, "go.mod", "module scenery.sh\n")
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions.json", `{"grafana":"1.0.0"}`)
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions_test.go", "package devtools\n")
	writeIntegrationHelperTestFile(t, root, "docs/readme.md", "ignored docs\n")

	first, err := integrationSourceFingerprint(root)
	if err != nil {
		t.Fatalf("integrationSourceFingerprint(first) error = %v", err)
	}
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions_test.go", "package devtools\n// ignored\n")
	writeIntegrationHelperTestFile(t, root, "docs/readme.md", "ignored docs changed\n")
	second, err := integrationSourceFingerprint(root)
	if err != nil {
		t.Fatalf("integrationSourceFingerprint(second) error = %v", err)
	}
	if second != first {
		t.Fatalf("fingerprint changed for ignored files: first=%s second=%s", first, second)
	}
	writeIntegrationHelperTestFile(t, root, "internal/devtools/versions.json", `{"grafana":"2.0.0"}`)
	third, err := integrationSourceFingerprint(root)
	if err != nil {
		t.Fatalf("integrationSourceFingerprint(third) error = %v", err)
	}
	if third == first {
		t.Fatalf("fingerprint did not change for embedded input: %s", third)
	}
}

func TestInstalledSceneryBinaryMatchesRepoUsesBuildInfoWithTrimpath(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIntegrationHelperTestFile(t, root, "go.mod", "module scenery.sh\n\ngo 1.26\n")
	writeIntegrationHelperTestFile(t, root, "cmd/scenery/main.go", "package main\n\nfunc main() {}\n")
	runIntegrationHelperCommand(t, root, "git", "init")
	runIntegrationHelperCommand(t, root, "git", "add", ".")
	runIntegrationHelperCommand(t, root, "git", "-c", "user.name=Scenery Test", "-c", "user.email=scenery@example.test", "commit", "-m", "initial")

	bin := filepath.Join(t.TempDir(), "scenery")
	runIntegrationHelperCommand(t, root, "go", "build", "-trimpath", "-o", bin, "./cmd/scenery")
	if !installedSceneryBinaryMatchesRepo(bin, root) {
		t.Fatal("trimpath-built scenery binary did not match repo via build info")
	}

	writeIntegrationHelperTestFile(t, root, "internal/app/root.go", "package app\n")
	runIntegrationHelperCommand(t, root, "git", "add", ".")
	runIntegrationHelperCommand(t, root, "git", "-c", "user.name=Scenery Test", "-c", "user.email=scenery@example.test", "commit", "-m", "change")
	if installedSceneryBinaryMatchesRepo(bin, root) {
		t.Fatal("binary still matched after repo revision changed")
	}
}

func writeIntegrationHelperTestFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}

func runIntegrationHelperCommand(t *testing.T, dir, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}
