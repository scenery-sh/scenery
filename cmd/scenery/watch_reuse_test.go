package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
)

func TestReloadConfigConsumesCapturedBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, ".scenery.json", `{"name":"captured","envs":{"local":{"default":true}}}`)
	snapshot, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, ".scenery.json", `{"name":"later","envs":{"local":{"default":true}}}`)
	supervisor := &devSupervisor{root: root, env: app.ResolvedEnv{Name: "local"}}
	cfg, err := supervisor.reloadConfig(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "captured" {
		t.Fatalf("reloaded mutable config %q instead of captured bytes", cfg.Name)
	}
}

func TestAppearingOptionalCompilerInputInvalidatesCapturedGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"captured\" {}\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile: %v", err)
	}
	before, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !before.compilerValid {
		t.Fatalf("initial capture: %v", err)
	}
	bindSnapshotContract(&before, contract)
	if _, ok := before.compilerAbsent["app.lock.scn"]; !ok {
		t.Fatal("optional lock absence was not captured")
	}
	writeWatchFile(t, root, "app.lock.scn", "")
	after, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !after.compilerValid || snapshotsEqual(before, after) {
		t.Fatal("new optional lock did not invalidate the captured graph")
	}
	if paths := changedPaths(before, after); len(paths) != 1 || paths[0] != "app.lock.scn" {
		t.Fatalf("changed paths = %v", paths)
	}
}

func TestScanWatchedFilesReusingDetectsChanges(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	appPath := filepath.Join(root, "app.scn")
	goPath := filepath.Join(root, "main.go")
	assetPath := filepath.Join(root, "asset.txt")
	if err := os.WriteFile(appPath, []byte("application \"test\" {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(goPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(assetPath, []byte("v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	first, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := scanWatchedFilesReusing(root, first)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(first, unchanged) {
		t.Fatal("reusing scan over an unchanged tree must equal the prior snapshot")
	}

	if err := os.WriteFile(goPath, []byte("package main\n\nimport _ \"embed\"\n\n//go:embed asset.txt\nvar asset string\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	edited, err := scanWatchedFilesReusing(root, unchanged)
	if err != nil {
		t.Fatal(err)
	}
	if snapshotsEqual(unchanged, edited) {
		t.Fatal("reusing scan missed an edited go file")
	}
	if stamp, ok := edited.files["asset.txt"]; !ok || !stamp.embed {
		t.Fatalf("embed edit did not stamp asset.txt as embedded: %+v", edited.files)
	}

	stale := edited.files["asset.txt"]
	if err := os.WriteFile(assetPath, []byte("v2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, time.Now(), stale.modTime.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	touched, err := scanWatchedFilesReusing(root, edited)
	if err != nil {
		t.Fatal(err)
	}
	if touched.files["asset.txt"].hash == stale.hash {
		t.Fatal("reusing scan reused a hash for a same-size content edit with a new mtime")
	}
	preserved := touched.files["asset.txt"]
	if err := os.WriteFile(assetPath, []byte("v3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(assetPath, preserved.modTime, preserved.modTime); err != nil {
		t.Fatal(err)
	}
	changedWithPreservedMtime, err := scanWatchedFilesReusing(root, touched)
	if err != nil {
		t.Fatal(err)
	}
	if changedWithPreservedMtime.files["asset.txt"].hash == preserved.hash {
		t.Fatal("reusing scan trusted preserved size and mtime over changed content")
	}
}

// A declaration edit refreshes the snapshot's compiler membership from a
// provisional graph. Returning the declaration to the accepted bytes must
// compile the accepted graph again, never the provisional one.
func TestRevisionMembershipFromReusedListingsStillSeesEveryMembershipChange(t *testing.T) {
	settleDirectoryListings(t)
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"membership\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"**/*.go\", \"go.mod\"]\n  revision_exclude = [\"cache/**\"]\n }\n}\n")
	writeWatchFile(t, root, "go.mod", "module example.test/membership\n")
	writeWatchFile(t, root, "pkg/service/handler.go", "package service\n")
	writeWatchFile(t, root, "pkg/service/handler_test.go", "package service\n")
	writeWatchFile(t, root, "cache/mod/dep.go", "package dep\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile: %v", err)
	}
	before, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !before.compilerValid {
		t.Fatalf("initial capture: %v", err)
	}
	bindSnapshotContract(&before, contract)
	if _, ok := before.compilerFiles["pkg/service/handler_test.go"]; !ok {
		t.Fatalf("test file is not a revision input: %v", before.compilerFiles)
	}
	if _, ok := before.compilerFiles["cache/mod/dep.go"]; ok {
		t.Fatal("an excluded file became a revision input")
	}
	if revisionListings(root).Len() == 0 {
		t.Fatal("the membership walk retained no listing, so the scans below would not exercise reuse")
	}
	writeWatchFile(t, root, "pkg/service/extra_test.go", "package service\n")
	added, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := added.compilerFiles["pkg/service/extra_test.go"]; !ok {
		t.Fatalf("a new revision input went unseen: %v", slices.Sorted(maps.Keys(added.compilerFiles)))
	}
	if err := os.Remove(filepath.Join(root, "pkg/service/extra_test.go")); err != nil {
		t.Fatal(err)
	}
	removed, err := scanWatchedFilesReusing(root, added)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := removed.compilerFiles["pkg/service/extra_test.go"]; ok {
		t.Fatal("a removed revision input stayed a member")
	}
	// An independent observation agrees with the membership reuse produced.
	fresh, err := scanWatchedFilesFresh(root, removed)
	if err != nil {
		t.Fatal(err)
	}
	if reused, read := slices.Sorted(maps.Keys(removed.compilerFiles)), slices.Sorted(maps.Keys(fresh.compilerFiles)); !slices.Equal(reused, read) {
		t.Fatalf("reused listings give membership %v, a fresh read %v", reused, read)
	}
}
