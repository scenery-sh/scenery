package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/snapshotarchive"
	"scenery.sh/internal/spec"
)

func TestSnapshotParsersRequireExplicitDestructiveChoices(t *testing.T) {
	for _, test := range []struct {
		load bool
		args []string
		want string
	}{
		{false, []string{"--output", "app.zip"}, "--db and/or --storage"},
		{false, []string{"--storage"}, "--output"},
		{false, []string{"--storage", "--output", "app.tar"}, "end in .zip"},
		{true, []string{"--input", "app.zip", "--mode", "merge"}, "--db and/or --storage"},
		{true, []string{"--storage", "--input", "app.zip"}, "--mode"},
		{true, []string{"--storage", "--input", "app.zip", "--mode", "overwrite"}, "--yes"},
		{true, []string{"--db", "--input", "app.zip", "--mode", "merge", "--on-conflict", "skip"}, "valid only"},
		{true, []string{"--storage", "--input", "app.zip", "--mode", "merge", "--on-conflict", "overwrite"}, "--yes"},
		{true, []string{"--storage", "--db", "--input", "app.zip", "--mode", "merge"}, "not replay-safe"},
		{true, []string{"--storage", "--input", "app.zip", "--mode", "merge", "--expect-sha256", "invalid"}, "64 lowercase"},
	} {
		var err error
		if test.load {
			_, err = parseSnapshotLoadArgs(test.args)
		} else {
			_, err = parseSnapshotSaveArgs(test.args)
		}
		if err == nil || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%v: %v; want %q", test.args, err, test.want)
		}
	}
	opts, err := parseSnapshotLoadArgs([]string{"--storage", "--input", "app.zip", "--mode", "merge"})
	if err != nil || opts.OnConflict != "fail" {
		t.Fatalf("options=%+v error=%v", opts, err)
	}
	if _, err := parseSnapshotVerifyArgs(nil); err == nil {
		t.Fatal("verify accepted no input")
	}
}

func emptySnapshotFixture(t *testing.T) string {
	t.Helper()
	var data bytes.Buffer
	w := snapshotarchive.NewWriter(&data, snapshotManifest{App: snapshotManifestApp{Name: "app", ID: "app"}, CreatedAt: time.Unix(1, 0).UTC(), Storage: &snapshotManifestStorage{}})
	if _, err := w.AddStore(context.Background(), t.TempDir(), "app", func(func(snapshotarchive.Object, io.Reader) error) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(file, data.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return file
}

func TestSnapshotDryRunAndVerifyDoNotAllocateTarget(t *testing.T) {
	home := t.TempDir()
	_ = isolateCommandAgentHomeAt(t, home)
	root := t.TempDir()
	cfg := appcfg.Config{Name: "app", ID: "app", Storage: appcfg.StorageConfig{Stores: map[string]appcfg.StorageStoreConfig{"app": {Kind: "local"}}}}
	archive := emptySnapshotFixture(t)
	verified, err := verifySnapshot(archive)
	if err != nil {
		t.Fatal(err)
	}
	result, err := loadSnapshot(context.Background(), root, cfg, snapshotLoadOptions{Input: archive, ExpectSHA256: verified.SHA256, Storage: true, Mode: "overwrite", Yes: true, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Storage == nil || result.Storage.Scope.Incarnation != nil || !result.DryRun {
		t.Fatalf("bad dry-run result: %+v", result)
	}
	plan, err := resolveStorageNamespacePlan(cfg, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Worktree.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dry-run allocated state: %v", err)
	}
}

func TestSnapshotWrongPinCannotChangeTarget(t *testing.T) {
	home := t.TempDir()
	_ = isolateCommandAgentHomeAt(t, home)
	root := t.TempDir()
	cfg := appcfg.Config{Name: "app", ID: "app"}
	_, err := loadSnapshot(context.Background(), root, cfg, snapshotLoadOptions{Input: emptySnapshotFixture(t), ExpectSHA256: strings.Repeat("0", 64), Storage: true, Mode: "overwrite", Yes: true})
	if err == nil || !strings.Contains(err.Error(), "SHA-256 mismatch") {
		t.Fatalf("wrong pin: %v", err)
	}
	plan, err := resolveStorageNamespacePlan(cfg, root, home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(plan.Worktree.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid archive mutated target")
	}
}

func TestSnapshotContainerDatabaseURLUsesContainerPort(t *testing.T) {
	got, err := snapshotContainerDatabaseURL(localagent.WorktreePostgres{Container: "postgres", Port: 54378, User: "scenery", Password: "secret"}, "app")
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

func TestSnapshotVerifyOutputMatchesCurrentSchema(t *testing.T) {
	archive := emptySnapshotFixture(t)
	var output bytes.Buffer
	if err := runSnapshotVerify(context.Background(), &output, []string{"--input", archive, "-o", "json"}); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	schemas := filepath.Join(repoRootForTest(t), "docs", "schemas")
	if diagnostics := validateHarnessJSONSchemaFile(filepath.Join(schemas, "scenery.cli.schema.json"), envelope); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	payload, ok := envelope["data"].(map[string]any)
	if !ok {
		t.Fatalf("missing command data: %s", output.Bytes())
	}
	schema := filepath.Join(schemas, "scenery.snapshot.verify.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schema, payload); len(diagnostics) != 0 {
		t.Fatal(diagnostics)
	}
	data, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(data)
	if payload["sha256"] != hex.EncodeToString(digest[:]) {
		t.Fatalf("wrong archive digest: %v", payload["sha256"])
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		payload["sha256"] = invalid
		if diagnostics := validateHarnessJSONSchemaFile(schema, payload); len(diagnostics) == 0 {
			t.Fatalf("schema accepted invalid digest %q", invalid)
		}
	}
	delete(payload, "sha256")
	if diagnostics := validateHarnessJSONSchemaFile(schema, payload); len(diagnostics) == 0 {
		t.Fatal("schema accepted missing digest")
	}
}
