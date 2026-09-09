package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/snapshotarchive"
)

func legacyDirectory(t *testing.T, physical, metadata string) string {
	t.Helper()
	root := t.TempDir()
	for name, value := range map[string]string{physical: "payload", metadataPrefix + physical + ".json": metadata} {
		path := filepath.Join(root, "objects", "files", filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(value), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func TestLegacyTenantExportPreservesLogicalMetadataAndSource(t *testing.T) {
	tenant := "tenant žluťoučký"
	physical := tenantPrefix + base64.RawURLEncoding.EncodeToString([]byte(tenant)) + "/photos/a.jpg"
	root := legacyDirectory(t, physical, `{"content_type":"image/jpeg","metadata":{"Case-Sensitive":"value"}}`)
	source, err := openDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	path := filepath.Join(root, "objects", "files", filepath.FromSlash(physical))
	before, _ := os.Stat(path)
	count := 0
	if err := exportStore(context.Background(), source, "files", true, func(object snapshotarchive.Object, body io.Reader) error {
		count++
		if object.Tenant != tenant || object.Key != "photos/a.jpg" || object.Metadata["Case-Sensitive"] != "value" || !object.ModifiedAt.Equal(before.ModTime()) {
			t.Fatalf("wrong logical metadata: %+v", object)
		}
		data, err := io.ReadAll(body)
		if string(data) != "payload" {
			t.Fatal("changed payload")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(path)
	data, _ := os.ReadFile(path)
	if count != 1 || !os.SameFile(before, after) || before.Mode() != after.Mode() || !before.ModTime().Equal(after.ModTime()) || string(data) != "payload" {
		t.Fatal("export mutated source")
	}
}

func TestLegacyExportRejectsMalformedMetadata(t *testing.T) {
	for _, data := range []string{"null", "[]", `{"unknown":1}`, `{"metadata":{"a":"one","a":"two"}}`, "{"} {
		if err := strictJSON([]byte(data), &sidecar{}); err == nil {
			t.Fatalf("accepted %s", data)
		}
	}
}

func TestLegacyExportRejectsAmbiguousTenantAndReservedKeys(t *testing.T) {
	for _, test := range []struct {
		key    string
		scoped bool
	}{
		{tenantPrefix + "dGVuYW50/key", false}, {"ordinary", true}, {tenantPrefix + "bad=/key", true}, {".scenery-put-incomplete", false}, {"__scenery/unknown", false},
	} {
		if _, err := logicalObject("files", test.key, test.scoped); err == nil {
			t.Fatalf("accepted %+v", test)
		}
	}
}

func TestLegacyExportRequiresQuiescenceBeforeOutput(t *testing.T) {
	root := legacyDirectory(t, "a", "{}")
	output := filepath.Join(t.TempDir(), "out.zip")
	err := export(context.Background(), options{source: root, output: output, appID: "app", appName: "App"}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "--confirm-source-quiesced") {
		t.Fatal(err)
	}
	if _, err := os.Stat(output); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published without quiescence")
	}
}

func TestLegacyDryRunGrammarReadsWithoutPublishing(t *testing.T) {
	root := legacyDirectory(t, "a", "{}")
	var report bytes.Buffer
	if err := run(context.Background(), []string{"--source", root, "--app-id", "app", "--dry-run"}, &report); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(report.String(), "Validated 1 objects; no output written") {
		t.Fatal(report.String())
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 1 || entries[0].Name() != "objects" {
		t.Fatalf("dry-run changed source: %v %v", entries, err)
	}
	for _, args := range [][]string{
		{"--source", root, "--app-id", "app", "--dry-run", "--output", filepath.Join(root, "out.zip")},
		{"--source", root, "--input", root, "--dry-run"},
		{"--input", root, "--dry-run"},
	} {
		if err := run(context.Background(), args, io.Discard); err == nil {
			t.Fatalf("accepted invalid grammar: %v", args)
		}
	}
}

func TestLegacyExportRejectsSymlinkAndOrphanedSidecar(t *testing.T) {
	root := legacyDirectory(t, "a", "{}")
	path := filepath.Join(root, "objects", "files", "a")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	source, err := openDirectory(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	if err := exportStore(context.Background(), source, "files", false, func(snapshotarchive.Object, io.Reader) error { return nil }); err == nil {
		t.Fatal("accepted orphaned sidecar")
	}
	if err := os.Symlink("/etc/hosts", path); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Open("files", "a"); err == nil {
		t.Fatal("followed source symlink")
	}
}
