package snapshotarchive

import (
	"archive/zip"
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
)

func testObject(value string) Object {
	sum := sha256.Sum256([]byte(value))
	return Object{Store: "files", Tenant: "tenant", Key: "photos/žluťoučký.jpg", SizeBytes: int64(len(value)), SHA256: hex.EncodeToString(sum[:]), ModifiedAt: time.Unix(1, 0).UTC(), ContentType: "image/jpeg", Metadata: map[string]string{"Case-Sensitive": "český text", "nested/key": "value"}}
}

func archiveBytes(t *testing.T, objects ...Object) []byte {
	t.Helper()
	var data bytes.Buffer
	w := NewWriter(&data, Manifest{App: App{ID: "app", Name: "App"}, CreatedAt: time.Unix(1, 0).UTC(), Storage: &Storage{}})
	_, err := w.AddStore(context.Background(), t.TempDir(), "files", func(visit func(Object, io.Reader) error) error {
		for _, object := range objects {
			if err := visit(object, strings.NewReader("value")); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func writeArchive(t *testing.T, data []byte) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "fixture.zip")
	if err := os.WriteFile(name, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestArchiveLogicalRoundTripAndPinnedOpen(t *testing.T) {
	object := testObject("value")
	data := archiveBytes(t, object)
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	r, err := Open(context.Background(), writeArchive(t, data), digest)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	count := 0
	if err := r.VisitStore(context.Background(), "files", func(got Object, body io.Reader) error {
		count++
		if got.Key != object.Key || got.Metadata["Case-Sensitive"] != object.Metadata["Case-Sensitive"] || got.SHA256 != object.SHA256 {
			t.Fatalf("lost logical metadata: %+v", got)
		}
		value, err := io.ReadAll(body)
		if string(value) != "value" {
			t.Fatal("lost body")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 || r.Manifest.Storage.Stores[0].Files != 1 || len(r.Manifest.Files) != 0 {
		t.Fatal("wrong logical inventory")
	}
	if r.SHA256 != digest {
		t.Fatal("wrong archive digest")
	}
}

func TestArchiveRejectsDuplicateLogicalTuple(t *testing.T) {
	object := testObject("value")
	if r, err := Open(context.Background(), writeArchive(t, archiveBytes(t, object, object)), ""); err == nil {
		_ = r.Close()
		t.Fatal("accepted duplicate tuple")
	}
}

func TestArchiveRejectsWrongPinBeforeApply(t *testing.T) {
	if r, err := Open(context.Background(), writeArchive(t, archiveBytes(t, testObject("value"))), strings.Repeat("a", 64)); err == nil {
		_ = r.Close()
		t.Fatal("accepted wrong checksum")
	}
}

func TestOpenedArchiveSurvivesPathReplacement(t *testing.T) {
	data := archiveBytes(t, testObject("value"))
	name := writeArchive(t, data)
	r, err := Open(context.Background(), name, "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := os.Rename(name, name+".old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	var copy bytes.Buffer
	if err := r.CopyTo(context.Background(), &copy); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(copy.Bytes(), data) {
		t.Fatal("reopened untrusted pathname")
	}
}

func TestArchiveRejectsUndeclaredAndMalformedInventory(t *testing.T) {
	for _, mode := range []string{"undeclared", "trailing-json", "old-format", "wrong-size", "unsafe-path"} {
		t.Run(mode, func(t *testing.T) {
			data := archiveBytes(t, testObject("value"))
			z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
			if err != nil {
				t.Fatal(err)
			}
			var changed bytes.Buffer
			w := zip.NewWriter(&changed)
			for _, entry := range z.File {
				body, err := entry.Open()
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(body)
				if err := errors.Join(err, body.Close()); err != nil {
					t.Fatal(err)
				}
				if entry.Name == "manifest.json" {
					var manifest Manifest
					if err := json.Unmarshal(content, &manifest); err != nil {
						t.Fatal(err)
					}
					if mode == "old-format" {
						manifest.SchemaRevision = "old"
					}
					if mode == "wrong-size" {
						manifest.Storage.Stores[0].Bytes++
					}
					content, err = json.Marshal(manifest)
					if err != nil {
						t.Fatal(err)
					}
					if mode == "trailing-json" {
						content = append(content, []byte(" {}")...)
					}
				}
				writer, err := w.Create(entry.Name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := writer.Write(content); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "undeclared" {
				if _, err := w.Create("unknown"); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unsafe-path" {
				if _, err := w.Create("../escape"); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if r, err := Open(context.Background(), writeArchive(t, changed.Bytes()), ""); err == nil {
				_ = r.Close()
				t.Fatalf("accepted %s", mode)
			}
		})
	}
}

func TestArchiveWriterRejectsTruncatedPayload(t *testing.T) {
	var data bytes.Buffer
	w := NewWriter(&data, Manifest{Storage: &Storage{}})
	_, err := w.AddStore(context.Background(), t.TempDir(), "files", func(visit func(Object, io.Reader) error) error {
		return visit(testObject("value"), strings.NewReader("bad"))
	})
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("expected truncated payload, got %v", err)
	}
}
