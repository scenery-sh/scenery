package onlava_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestIntegrationProcessSlotCountCapsDefaultFanout(t *testing.T) {
	t.Setenv("ONLAVA_INTEGRATION_PROCESS_SLOTS", "")
	old := runtime.GOMAXPROCS(10)
	t.Cleanup(func() { runtime.GOMAXPROCS(old) })

	if got, want := integrationProcessSlotCount(), 12; got != want {
		t.Fatalf("integrationProcessSlotCount() = %d, want %d", got, want)
	}
}

func TestIntegrationProcessSlotCountHonorsOverride(t *testing.T) {
	t.Setenv("ONLAVA_INTEGRATION_PROCESS_SLOTS", "9")

	if got, want := integrationProcessSlotCount(), 9; got != want {
		t.Fatalf("integrationProcessSlotCount() = %d, want %d", got, want)
	}
}

func TestLatestIntegrationSourceModTimeIncludesEmbeddedNonGoInputs(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeIntegrationHelperTestFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n")
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
	writeIntegrationHelperTestFile(t, root, "go.mod", "module github.com/pbrazdil/onlava\n")
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
