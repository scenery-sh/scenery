package generate

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/compiler"
)

func checkScopeWrite(t *testing.T, root, path string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestGeneratedDescriptorScanFindsWhatAWalkFinds(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, path := range []string{
		"a/scenery.generated.json",
		"a/b/scenery.package-generated.json",
		"clients/web/" + typescriptGeneratedDescriptorName,
		"clients/web/other.json",
		"node_modules/pkg/scenery.generated.json",
		".scenery/gen/scenery.generated.json",
		".git/scenery.generated.json",
	} {
		checkScopeWrite(t, root, path)
	}
	if err := os.Symlink(filepath.Join(root, "a/scenery.generated.json"), filepath.Join(root, "a/b/scenery.generated.json")); err != nil {
		t.Fatal(err)
	}
	descriptors, err := scanGeneratedDescriptors(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, descriptor := range descriptors {
		relative, _ := filepath.Rel(root, descriptor.path)
		if descriptor.symlink {
			relative += " (symlink)"
		}
		got = append(got, filepath.ToSlash(relative))
	}
	want := []string{"a/b/scenery.generated.json (symlink)", "a/b/scenery.package-generated.json", "a/scenery.generated.json", "clients/web/" + typescriptGeneratedDescriptorName}
	if !slices.Equal(got, want) {
		t.Fatalf("descriptors = %q, want %q", got, want)
	}
	// A descriptor that appears later is found by the next scan, whatever
	// listings the first one retained.
	checkScopeWrite(t, root, "a/c/scenery.generated.json")
	again, err := scanGeneratedDescriptors(root)
	if err != nil || len(again) != len(descriptors)+1 {
		t.Fatalf("second scan = %d descriptors, %v", len(again), err)
	}
}

func TestArtifactCheckScopeSharesOnlyWhileTheCheckRuns(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	checkScopeWrite(t, root, "a/scenery.generated.json")
	result := &compiler.Result{Root: root}
	if artifactCheckScopeFor(result) != nil {
		t.Fatal("a result had a scope before its check began")
	}
	end := beginArtifactCheckScope(result)
	first, err := generatedDescriptorsBeneath(result, root)
	if err != nil || len(first) != 1 {
		t.Fatalf("scan = %v, %v", first, err)
	}
	// Within one check the tree is observed once: the check writes nothing, and
	// what another writer adds meanwhile belongs to the next check.
	checkScopeWrite(t, root, "b/scenery.generated.json")
	shared, err := generatedDescriptorsBeneath(result, root)
	if err != nil || len(shared) != 1 {
		t.Fatalf("a second scan within the check = %v, %v", shared, err)
	}
	input := sharedProjectionInput(result)
	if again := sharedProjectionInput(result); again.digest != input.digest {
		t.Fatal("the shared projection input changed within one check")
	}
	// A nested check of the same result shares the outer scope and must not end
	// it.
	beginArtifactCheckScope(result)()
	if artifactCheckScopeFor(result) == nil {
		t.Fatal("a nested check ended the outer scope")
	}
	end()
	if artifactCheckScopeFor(result) != nil {
		t.Fatal("the scope outlived its check")
	}
	after, err := generatedDescriptorsBeneath(result, root)
	if err != nil || len(after) != 2 {
		t.Fatalf("scan after the check = %v, %v", after, err)
	}
}
