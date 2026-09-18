package main

import (
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
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

func TestCapturedHandlerEditReusesGraphWithoutReadingLaterTreeBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"captured\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"*.go\", \"go.mod\"]\n  revision_exclude = []\n }\n}\n")
	writeWatchFile(t, root, "go.mod", "module example.test/captured\n")
	writeWatchFile(t, root, "handler.go", "package captured\nconst value = 1\n")
	writeWatchFile(t, root, "handler_test.go", "package captured\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile: %v", err)
	}
	initial, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !initial.compilerValid {
		t.Fatalf("initial capture: %v", err)
	}
	bindSnapshotContract(&initial, contract)
	if _, ok := initial.compilerFiles["handler_test.go"]; !ok {
		t.Fatal("complete compiler capture omitted a revision-matched test file")
	}
	initialFingerprint := snapshotFingerprint(initial)
	writeWatchFile(t, root, "added_test.go", "package captured\n")
	testAdded, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(initial, testAdded) || len(changedPaths(initial, testAdded)) != 0 || snapshotFingerprint(testAdded) != initialFingerprint {
		t.Fatal("new captured compiler test input independently invalidated the runtime")
	}
	if err := os.Remove(filepath.Join(root, "added_test.go")); err != nil {
		t.Fatal(err)
	}
	testRemoved, err := scanWatchedFilesReusing(root, testAdded)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(testAdded, testRemoved) || len(changedPaths(testAdded, testRemoved)) != 0 || snapshotFingerprint(testRemoved) != initialFingerprint {
		t.Fatal("removed captured compiler test input independently invalidated the runtime")
	}
	initial = testRemoved

	writeWatchFile(t, root, "handler_test.go", "package captured\n// current test-only bytes\n")
	testEdited, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	if !snapshotsEqual(initial, testEdited) || len(changedPaths(initial, testEdited)) != 0 || snapshotFingerprint(testEdited) != initialFingerprint {
		t.Fatal("captured compiler test input independently invalidated the runtime")
	}
	initial = testEdited

	writeWatchFile(t, root, "handler.go", "package captured\nconst value = 2\n")
	edited, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	captured := buildSourceSnapshot(edited)
	writeWatchFile(t, root, "handler.go", "package captured\nconst value = 3\n")
	result, err := build.CompileContractWithSnapshot(root, captured)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Sources) == 0 || len(contract.Sources) == 0 || &result.Sources[0].Bytes[0] != &contract.Sources[0].Bytes[0] || result.WorkspaceRevision == contract.WorkspaceRevision {
		t.Fatalf("handler edit did not reuse the graph with a fresh captured identity: revision=%s", result.WorkspaceRevision)
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

func TestSuccessfulDeclarationBuildBecomesNextCapturedGraphBaseline(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"before\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"*.go\", \"go.mod\"]\n  revision_exclude = []\n }\n}\n")
	writeWatchFile(t, root, "go.mod", "module example.test/captured\n")
	writeWatchFile(t, root, "handler.go", "package captured\nconst value = 1\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile initial graph: %v", err)
	}
	initial, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !initial.compilerValid {
		t.Fatalf("capture initial graph: %v", err)
	}
	bindSnapshotContract(&initial, contract)

	writeWatchFile(t, root, "app.scn", "application \"after\" {}\nworkspace {\n implementation_root \"go\" {\n  path = \".\"\n  revision_include = [\"*.go\", \"go.mod\"]\n  revision_exclude = []\n }\n}\n")
	declarationEdit, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := build.CompileContractWithSnapshot(root, buildSourceSnapshot(declarationEdit))
	if err != nil || !updated.Valid() || updated.Manifest.Application.Name != "after" {
		t.Fatalf("compile declaration edit: graph=%#v err=%v", updated, err)
	}
	refreshSnapshotContract(root, &declarationEdit, updated)
	if declarationEdit.contract != updated {
		t.Fatal("successful declaration graph was not retained as the next watch baseline")
	}

	writeWatchFile(t, root, "handler.go", "package captured\nconst value = 2\n")
	handlerEdit, err := scanWatchedFilesReusing(root, declarationEdit)
	if err != nil {
		t.Fatal(err)
	}
	captured := buildSourceSnapshot(handlerEdit)
	writeWatchFile(t, root, "app.scn", "this is not valid scenery source\n")
	reused, err := build.CompileContractWithSnapshot(root, captured)
	if err != nil {
		t.Fatal(err)
	}
	if !reused.Valid() || reused.Manifest.Application.Name != "after" || len(reused.Sources) == 0 || &reused.Sources[0].Bytes[0] != &updated.Sources[0].Bytes[0] {
		t.Fatalf("handler edit reread the later authored tree instead of reusing the promoted graph: %#v", reused)
	}
}

func TestDeclarationCompileConsumesCapturedBytesAfterLiveTreeChanges(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"before\" {}\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile initial graph: %v", err)
	}
	initial, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !initial.compilerValid {
		t.Fatalf("capture initial graph: %v", err)
	}
	bindSnapshotContract(&initial, contract)

	writeWatchFile(t, root, "app.scn", "application \"captured\" {}\n")
	declarationEdit, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	writeWatchFile(t, root, "app.scn", "this later tree content is invalid\n")
	result, err := build.CompileContractWithSnapshot(root, buildSourceSnapshot(declarationEdit))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || result.Manifest.Application.Name != "captured" || result.Root != root {
		t.Fatalf("compiled graph did not use captured declaration bytes: root=%q graph=%#v", result.Root, result)
	}
	if len(result.Sources) != 1 || result.Sources[0].Path != filepath.Join(root, "app.scn") {
		t.Fatalf("captured source paths were not rebound to authored root: %#v", result.Sources)
	}
}

func TestDeclarationEditRefreshesNewCompilerInputBeforeCapturedCompile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"before\" {}\nworkspace {}\n")
	writeWatchFile(t, root, "native.input", "captured\n")
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile initial graph: %v", err)
	}
	initial, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !initial.compilerValid {
		t.Fatalf("capture initial graph: %v", err)
	}
	bindSnapshotContract(&initial, contract)
	if _, ok := initial.compilerFiles["native.input"]; ok {
		t.Fatal("undeclared arbitrary input was unexpectedly watched")
	}

	writeWatchFile(t, root, "app.scn", "application \"after\" {}\nworkspace {\n revision_input \"native\" { paths = [\"native.input\"] }\n}\n")
	declarationEdit, err := scanWatchedFilesReusing(root, initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshBuildCompilerMembership(root, &declarationEdit); err != nil {
		t.Fatal(err)
	}
	input, ok := declarationEdit.compilerFiles["native.input"]
	if !ok || string(input.data) != "captured\n" {
		t.Fatalf("new declaration input was not captured: %#v", input)
	}
	current, err := scanWatchedFilesReusing(root, declarationEdit)
	if err != nil || !buildInputSnapshotsEqual(declarationEdit, current) {
		t.Fatalf("freshness rescan lost the discovered compiler membership: %v", err)
	}
	writeWatchFile(t, root, "native.input", "later-live\n")
	result, err := build.CompileContractWithSnapshot(root, buildSourceSnapshot(declarationEdit))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() || result.Manifest.Application.Name != "after" {
		t.Fatalf("captured graph compile failed: %#v", result)
	}
	want := *result
	if err := compiler.BindCapturedWorkspaceRevision(&want, map[string][]byte{"native.input": []byte("captured\n")}); err != nil {
		t.Fatal(err)
	}
	if result.WorkspaceRevision != want.WorkspaceRevision {
		t.Fatalf("workspace revision did not use captured new input: got %s want %s", result.WorkspaceRevision, want.WorkspaceRevision)
	}
}

// Steady-state rescans must reuse prior content hashes for stat-identical
// files while still detecting real edits, including //go:embed additions
// whose patterns come from the per-process embed cache.
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
func TestRefreshedCompilerMembershipNeverBecomesTheSnapshotGraph(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	const accepted = "application \"captured\" {}\n"
	const edited = "application \"captured\" {}\n\nhttp_gateway \"public_api\" {\n  exposure        = \"internet\"\n  base_path       = \"/\"\n  cors            = std.cors.none\n  trusted_proxies = std.trusted_proxies.none\n  forwarded       = std.forwarded_headers.reject\n}\n"
	writeWatchFile(t, root, "app.scn", accepted)
	contract, err := compiler.Compile(root)
	if err != nil || !contract.Valid() {
		t.Fatalf("compile: %v", err)
	}
	snapshot, err := scanWatchedFilesReusing(root, fileSnapshot{contract: contract})
	if err != nil || !snapshot.compilerValid {
		t.Fatalf("initial capture: %v", err)
	}
	bindSnapshotContract(&snapshot, contract)

	writeWatchFile(t, root, "app.scn", edited)
	changed, err := scanWatchedFilesReusing(root, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if err := refreshBuildCompilerMembership(root, &changed); err != nil {
		t.Fatal(err)
	}
	provisional, err := build.CompileContractWithSnapshot(root, buildSourceSnapshot(changed))
	if err != nil || !provisional.Valid() {
		t.Fatalf("compile the edited declaration: %v", err)
	}
	if provisional.Manifest.ContractRevision == contract.Manifest.ContractRevision {
		t.Fatal("the edited declaration did not change the contract revision")
	}

	writeWatchFile(t, root, "app.scn", accepted)
	returned, err := scanWatchedFilesReusing(root, changed)
	if err != nil {
		t.Fatal(err)
	}
	result, err := build.CompileContractWithSnapshot(root, buildSourceSnapshot(returned))
	if err != nil || !result.Valid() {
		t.Fatalf("compile the returned declaration: %v", err)
	}
	if result.Manifest.ContractRevision != contract.Manifest.ContractRevision {
		t.Fatalf("returning the declaration compiled %s, want the accepted %s", result.Manifest.ContractRevision, contract.Manifest.ContractRevision)
	}
	if changed.contract != contract || returned.contract != contract {
		t.Fatal("the provisional membership graph became the snapshot's bound graph")
	}
}

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
