package scenery_test

import (
	"errors"
	"os"
	"path/filepath"
	"runtime/debug"
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

func TestSceneryBuildInfoMatchesRepoRevisionInProcess(t *testing.T) {
	t.Parallel()

	const revision = "0123456789abcdef0123456789abcdef01234567"
	info := &debug.BuildInfo{
		Main: debug.Module{Path: "scenery.sh"},
		Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: revision},
			{Key: "vcs.modified", Value: "false"},
		},
	}
	if !sceneryBuildInfoMatchesRepoRevision(info, revision, nil) {
		t.Fatal("matching clean scenery build info was rejected")
	}
	for name, mutate := range map[string]func(*debug.BuildInfo) (string, error){
		"other module": func(copy *debug.BuildInfo) (string, error) {
			copy.Main.Path = "example.test/other"
			return revision, nil
		},
		"missing revision": func(copy *debug.BuildInfo) (string, error) {
			copy.Settings[0].Value = ""
			return revision, nil
		},
		"modified": func(copy *debug.BuildInfo) (string, error) {
			copy.Settings[1].Value = "true"
			return revision, nil
		},
		"different revision": func(*debug.BuildInfo) (string, error) { return "different", nil },
		"repo error":         func(*debug.BuildInfo) (string, error) { return revision, errors.New("git failed") },
	} {
		t.Run(name, func(t *testing.T) {
			copy := *info
			copy.Settings = append([]debug.BuildSetting(nil), info.Settings...)
			repoRevision, repoErr := mutate(&copy)
			if sceneryBuildInfoMatchesRepoRevision(&copy, repoRevision, repoErr) {
				t.Fatal("non-matching build info was accepted")
			}
		})
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
