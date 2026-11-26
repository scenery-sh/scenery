package jsbuild

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"scenery.sh/internal/compiler"
)

func TestCacheFingerprintsInputsBelowSkippedDirectories(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	module := filepath.Join(root, "svc")
	files := []string{"svc/main.ts", "svc/target/rules.ts", "svc/dist/vendor.ts", "svc/lib/out/generated.ts", "svc/node_modules/pkg/index.js", "node_modules/shared/index.js"}
	inputs := make([]string, 0, len(files))
	for _, name := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("export {}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, path)
	}
	dependencies := cacheDependencies(root, module, inputs)
	if want := []string{"node_modules/shared/index.js", "svc/dist/vendor.ts", "svc/lib/out/generated.ts", "svc/node_modules/pkg/index.js", "svc/target/rules.ts"}; !slices.Equal(dependencies, want) {
		t.Fatalf("dependencies = %v, want %v", dependencies, want)
	}
	service := Service{JSWorkerTarget: compiler.JSWorkerTarget{Directory: "svc", Workspace: "."}, Name: "svc/service/svc"}
	before, err := buildFingerprint(root, service, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range dependencies[1:] {
		if err := os.WriteFile(filepath.Join(root, name), []byte("export const changed = true;\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		after, err := buildFingerprint(root, service, dependencies)
		if err != nil || after == before {
			t.Fatalf("editing %s left the fingerprint unchanged (%v)", name, err)
		}
		before = after
	}
}

func TestVerifyBuildCachePublishGuardsMidBuildEdits(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	depRel := filepath.Join("svc", "node_modules", "pkg", "index.js")
	depPath := filepath.Join(root, depRel)
	if err := os.MkdirAll(filepath.Dir(depPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(depPath, []byte("export const v = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := fileDigest(depPath)
	if err != nil {
		t.Fatal(err)
	}

	buildStarted := time.Now().Add(time.Second)
	sentinel := errors.New("inputs changed")
	if _, err := verifyBuildCachePublish(root, map[string]string{depRel: digest}, []string{depRel}, buildStarted, func([]string) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("verify error = %v, want sentinel", err)
	}

	if _, err := verifyBuildCachePublish(root, map[string]string{depRel: "stale"}, []string{depRel}, buildStarted, nil); !errors.Is(err, ErrSourcesChanged) {
		t.Fatalf("expected ErrSourcesChanged mismatch error when prior dependency digest changed, got %v", err)
	}

	if err := os.WriteFile(depPath, []byte("export const v = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	recentStart := time.Now().Add(-time.Second)
	cacheable, err := verifyBuildCachePublish(root, nil, []string{depRel}, recentStart, nil)
	if err != nil {
		t.Fatalf("verifyBuildCachePublish(new dep recent mtime) = %v", err)
	}
	if cacheable {
		t.Fatal("expected cacheable = false when dependency mtime is not strictly before build start")
	}

	cacheable, err = verifyBuildCachePublish(root, nil, []string{depRel}, time.Now().Add(time.Second), nil)
	if err != nil || !cacheable {
		t.Fatalf("verifyBuildCachePublish(clean) = (%v, %v), want (true, nil)", cacheable, err)
	}
}

func TestVerifyBuildDependenciesRunsVerifyFirstAndRetriesNewPostStartEdit(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	service := Service{JSWorkerTarget: compiler.JSWorkerTarget{Directory: "svc", Workspace: "."}, Name: "svc/service/svc"}
	mainPath := filepath.Join(root, "svc", "main.ts")
	depRel := filepath.Join("svc", "node_modules", "pkg", "index.js")
	depPath := filepath.Join(root, depRel)
	for _, p := range []string{mainPath, depPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(mainPath, []byte("import 'pkg';\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(depPath, []byte("export const v = 1;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	moduleInputs, err := captureModuleInputs(root, service)
	if err != nil {
		t.Fatal(err)
	}

	// verify callback runs before priorDepDigests comparison so caller mismatch wins.
	sentinel := errors.New("verify failed first")
	if _, _, _, err := verifyBuildDependencies(root, map[string]string{depRel: "stale"}, []string{depRel}, time.Now().Add(time.Second), func([]string) error {
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("verifyBuildDependencies = %v, want verify callback error first", err)
	}

	// A newly discovered dependency whose mtime is after buildStarted triggers one retry.
	buildStarted := time.Now().Add(-time.Second)
	if err := os.WriteFile(depPath, []byte("export const v = 2;\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	depDigests, retryNewDep, cacheable, err := verifyBuildDependencies(root, nil, []string{depRel}, buildStarted, nil)
	if err != nil || !retryNewDep || cacheable {
		t.Fatalf("verifyBuildDependencies(new dep edited after start) = (retry=%v, cacheable=%v, err=%v), want retry=true, cacheable=false", retryNewDep, cacheable, err)
	}
	// buildFingerprintFromDigests must match buildFingerprint identically using depDigests.
	wantFP, err := buildFingerprint(root, service, []string{depRel})
	if err != nil {
		t.Fatal(err)
	}
	gotFP, err := buildFingerprintFromDigests(root, service, moduleInputs, []string{depRel}, depDigests)
	if err != nil || gotFP != wantFP {
		t.Fatalf("buildFingerprintFromDigests = (%q, %v), want %q", gotFP, err, wantFP)
	}
	// Once priorDepDigests is seeded, retryNewDep is false even with recent mtime (non-cacheable rather than infinite loop).
	if _, retryAgain, cacheableAgain, err := verifyBuildDependencies(root, depDigests, []string{depRel}, buildStarted, nil); err != nil || retryAgain || cacheableAgain {
		t.Fatalf("second attempt = (retry=%v, cacheable=%v, err=%v), want (false, false, nil)", retryAgain, cacheableAgain, err)
	}

	// ModTime().Equal(buildStarted) on a newly discovered dependency also triggers retryNewDep.
	exactInfo, err := os.Stat(depPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, retryEqual, cacheableEqual, err := verifyBuildDependencies(root, nil, []string{depRel}, exactInfo.ModTime(), nil); err != nil || !retryEqual || cacheableEqual {
		t.Fatalf("ModTime().Equal(buildStarted) = (retry=%v, cacheable=%v, err=%v), want (true, false, nil)", retryEqual, cacheableEqual, err)
	}
}
