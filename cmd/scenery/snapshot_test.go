package main

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/spec"
)

func TestSnapshotParsersRequireExplicitDestructiveChoices(t *testing.T) {
	for _, test := range []struct {
		name string
		load bool
		args []string
		want string
	}{
		{name: "save sections", args: []string{"--output", "app.zip"}, want: "--db and/or --storage"},
		{name: "save output", args: []string{"--storage"}, want: "--output"},
		{name: "save zip", args: []string{"--storage", "--output", "app.tar"}, want: "end in .zip"},
		{name: "load sections", load: true, args: []string{"--input", "app.zip", "--mode", "merge"}, want: "--db and/or --storage"},
		{name: "load mode", load: true, args: []string{"--storage", "--input", "app.zip"}, want: "--mode"},
		{name: "overwrite approval", load: true, args: []string{"--storage", "--input", "app.zip", "--mode", "overwrite"}, want: "--yes"},
		{name: "conflict scope", load: true, args: []string{"--db", "--input", "app.zip", "--mode", "merge", "--on-conflict", "skip"}, want: "valid only"},
		{name: "conflict overwrite approval", load: true, args: []string{"--storage", "--input", "app.zip", "--mode", "merge", "--on-conflict", "overwrite"}, want: "--yes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var err error
			if test.load {
				_, err = parseSnapshotLoadArgs(test.args)
			} else {
				_, err = parseSnapshotSaveArgs(test.args)
			}
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want %q", err, test.want)
			}
		})
	}
	opts, err := parseSnapshotLoadArgs([]string{"--storage", "--input", "app.zip", "--mode", "merge"})
	if err != nil || opts.OnConflict != "fail" {
		t.Fatalf("default conflict policy = %q, error = %v", opts.OnConflict, err)
	}
}

func TestSnapshotVerifyRequiresInput(t *testing.T) {
	if _, err := parseSnapshotVerifyArgs(nil); err == nil || !strings.Contains(err.Error(), "--input") {
		t.Fatalf("parseSnapshotVerifyArgs error = %v", err)
	}
}

func TestSnapshotStorageRoundTripRecoversInterruptedReplacement(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	cfg := snapshotTestConfig()
	plan, err := resolveStorageCellPlan(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	store := plan.storageStoreObjectsDir("app")
	writeSnapshotTestFile(t, filepath.Join(store, "reports", "one.txt"), "saved object")
	writeSnapshotTestFile(t, filepath.Join(store, "__scenery", "metadata", "reports", "one.txt.json"), `{"content_type":"text/plain"}`)
	archivePath := filepath.Join(t.TempDir(), "app.zip")
	result, err := saveSnapshot(context.Background(), t.TempDir(), cfg, snapshotSaveOptions{Output: archivePath, Storage: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 2 || result.Storage == nil || result.Storage.Files != 2 {
		t.Fatalf("save result = %#v", result)
	}
	verified, err := verifySnapshot(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if !verified.Storage || verified.DB || verified.Files != 2 || verified.Bytes != result.Bytes {
		t.Fatalf("verify result = %#v", verified)
	}
	archive, err := openSnapshotArchive(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if len(archive.manifest.Files) != 2 || !validSnapshotSHA256(archive.manifest.Files[0].SHA256) {
		t.Fatalf("manifest files = %#v", archive.manifest.Files)
	}
	_ = archive.reader.Close()

	writeSnapshotTestFile(t, filepath.Join(store, "reports", "one.txt"), "mutated object")
	writeSnapshotTestFile(t, filepath.Join(store, "stale.txt"), "remove me")
	_, stage, trash := snapshotStoreSwapPaths(plan, "app")
	if err := os.Rename(store, trash); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, filepath.Join(stage, "partial.txt"), "interrupted")
	loaded, err := loadSnapshot(context.Background(), t.TempDir(), cfg, snapshotLoadOptions{Input: archivePath, Storage: true, Mode: "overwrite", Yes: true})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Storage == nil || loaded.Storage.Files != 2 {
		t.Fatalf("load result = %#v", loaded)
	}
	assertSnapshotTestFile(t, filepath.Join(store, "reports", "one.txt"), "saved object")
	assertSnapshotTestFile(t, filepath.Join(store, "__scenery", "metadata", "reports", "one.txt.json"), `{"content_type":"text/plain"}`)
	for _, missing := range []string{filepath.Join(store, "stale.txt"), stage, trash} {
		if _, err := os.Stat(missing); !os.IsNotExist(err) {
			t.Fatalf("%s still exists: %v", missing, err)
		}
	}
}

func TestSnapshotStorageMergeConflictPolicies(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	cfg := snapshotTestConfig()
	plan, _ := resolveStorageCellPlan(cfg, "")
	store := plan.storageStoreObjectsDir("app")
	object := filepath.Join(store, "one.txt")
	writeSnapshotTestFile(t, object, "saved")
	archivePath := filepath.Join(t.TempDir(), "app.zip")
	if _, err := saveSnapshot(context.Background(), t.TempDir(), cfg, snapshotSaveOptions{Output: archivePath, Storage: true}); err != nil {
		t.Fatal(err)
	}
	writeSnapshotTestFile(t, object, "current")
	if _, err := loadSnapshot(context.Background(), t.TempDir(), cfg, snapshotLoadOptions{Input: archivePath, Storage: true, Mode: "merge", OnConflict: "fail"}); err == nil {
		t.Fatal("merge conflict unexpectedly succeeded")
	}
	assertSnapshotTestFile(t, object, "current")
	if _, err := loadSnapshot(context.Background(), t.TempDir(), cfg, snapshotLoadOptions{Input: archivePath, Storage: true, Mode: "merge", OnConflict: "skip"}); err != nil {
		t.Fatal(err)
	}
	assertSnapshotTestFile(t, object, "current")
	if _, err := loadSnapshot(context.Background(), t.TempDir(), cfg, snapshotLoadOptions{Input: archivePath, Storage: true, Mode: "merge", OnConflict: "overwrite", Yes: true}); err != nil {
		t.Fatal(err)
	}
	assertSnapshotTestFile(t, object, "saved")
}

func TestSnapshotRejectsChecksumMismatchBeforeLoad(t *testing.T) {
	manifest := snapshotManifest{
		Kind: snapshotManifestKind, SchemaRevision: snapshotManifestSchemaRevision,
		App:     snapshotManifestApp{Name: "app", ID: "app"},
		Storage: &snapshotManifestStorage{CellID: "cell", Stores: []snapshotManifestStore{{Name: "app", Files: 1, Bytes: 7}}},
		Files:   []snapshotManifestFile{{Path: "storage/app/one.txt", Bytes: 7, SHA256: "sha256:" + strings.Repeat("0", 64)}},
	}
	archivePath := filepath.Join(t.TempDir(), "corrupt.zip")
	writeSnapshotTestArchive(t, archivePath, manifest, map[string]string{"storage/app/one.txt": "corrupt"})
	if _, err := openSnapshotArchive(archivePath); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("error = %v", err)
	}
}

func TestSnapshotRejectsUnsafeArchivePath(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "unsafe.zip")
	writeSnapshotTestArchive(t, archivePath, snapshotManifest{Kind: snapshotManifestKind, SchemaRevision: snapshotManifestSchemaRevision}, map[string]string{"../outside": "nope"})
	if _, err := openSnapshotArchive(archivePath); err == nil || !strings.Contains(err.Error(), "invalid snapshot archive path") {
		t.Fatalf("error = %v", err)
	}
}

func TestSnapshotSaveFailurePreservesExistingArchive(t *testing.T) {
	home := t.TempDir()
	t.Setenv("SCENERY_AGENT_HOME", home)
	cfg := snapshotTestConfig()
	plan, err := resolveStorageCellPlan(cfg, "")
	if err != nil {
		t.Fatal(err)
	}
	store := plan.storageStoreObjectsDir("app")
	if err := os.MkdirAll(store, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("missing", filepath.Join(store, "unsafe")); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "app.zip")
	if err := os.WriteFile(archivePath, []byte("previous archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := saveSnapshot(context.Background(), t.TempDir(), cfg, snapshotSaveOptions{Output: archivePath, Storage: true}); err == nil {
		t.Fatal("save unexpectedly succeeded")
	}
	assertSnapshotTestFile(t, archivePath, "previous archive")
}

func TestSnapshotContainerDatabaseURLUsesContainerPort(t *testing.T) {
	got, err := snapshotContainerDatabaseURL(postgresServerState{Container: "postgres", Port: 54378, User: "scenery", Password: "secret"}, "app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "127.0.0.1:5432/app") || strings.Contains(got, "54378") {
		t.Fatalf("container URL = %s", got)
	}
}

func TestSnapshotManifestSchemaRevisionMatchesCheckedSchema(t *testing.T) {
	encoded, err := os.ReadFile(filepath.Join("..", "..", "docs", "schemas", "scenery.snapshot.manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	revision, err := spec.SchemaDocumentRevision(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(revision) != snapshotManifestSchemaRevision {
		t.Fatalf("schema revision = %s, want %s", revision, snapshotManifestSchemaRevision)
	}
}

func snapshotTestConfig() appcfg.Config {
	return appcfg.Config{
		Name: "app", ID: "app",
		Storage: appcfg.StorageConfig{CellID: "cell", Stores: map[string]appcfg.StorageStoreConfig{"app": {Kind: "local"}}},
	}
}

func writeSnapshotTestFile(t *testing.T, filePath, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(filePath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSnapshotTestFile(t *testing.T, filePath, want string) {
	t.Helper()
	got, err := os.ReadFile(filePath)
	if err != nil || string(got) != want {
		t.Fatalf("%s = %q, %v; want %q", filePath, got, err, want)
	}
}

func writeSnapshotTestArchive(t *testing.T, filePath string, manifest snapshotManifest, files map[string]string) {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for name, contents := range files {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(entry, contents); err != nil {
			t.Fatal(err)
		}
	}
	entry, err := writer.Create("manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(entry).Encode(manifest); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filePath, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}
