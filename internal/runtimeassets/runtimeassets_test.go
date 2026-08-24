package runtimeassets

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/spec"
)

func TestSchemaRevisionMatchesDescriptor(t *testing.T) {
	if got := string(spec.SchemaRevision(schemaDescriptor)); got != SchemaRevision {
		t.Fatalf("schema revision = %q, want %q", got, SchemaRevision)
	}
}

func TestAssistantAssetSchemaRevisionMatchesDescriptor(t *testing.T) {
	if got := string(spec.SchemaRevision(assistantAssetSchemaDescriptor)); got != AssistantAssetSchemaRevision {
		t.Fatalf("assistant asset schema revision = %q, want %q", got, AssistantAssetSchemaRevision)
	}
}

func TestBuildArchiveIsDeterministicAndRestoresModes(t *testing.T) {
	left := t.TempDir()
	right := t.TempDir()
	for _, root := range []string{left, right} {
		if err := os.Mkdir(filepath.Join(root, "bin"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "bin", "run"), []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "readme"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("../readme", filepath.Join(root, "bin", "readme")); err != nil {
			t.Fatal(err)
		}
	}
	// Source mtimes are intentionally different; they do not enter the
	// canonical descriptor or archive headers.
	if err := os.Chtimes(filepath.Join(left, "readme"), time.Unix(10, 0), time.Unix(10, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(right, "readme"), time.Unix(20, 0), time.Unix(20, 0)); err != nil {
		t.Fatal(err)
	}

	a, err := BuildArchive(left)
	if err != nil {
		t.Fatal(err)
	}
	b, err := BuildArchive(right)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Data, b.Data) || a.ArchiveDigest != b.ArchiveDigest || a.Descriptor.Digest != b.Descriptor.Digest {
		t.Fatalf("deterministic archive mismatch: archive %q/%q tree %q/%q", a.ArchiveDigest, b.ArchiveDigest, a.Descriptor.Digest, b.Descriptor.Digest)
	}
	if got := a.Descriptor.Entries[0].Path; got != "bin" {
		t.Fatalf("first lexical entry = %q, want bin", got)
	}

	destination := filepath.Join(t.TempDir(), "runtime")
	got, err := ExtractArchive(a.Data, destination)
	if err != nil {
		t.Fatal(err)
	}
	if got.Digest != a.Descriptor.Digest {
		t.Fatalf("extracted digest = %q, want %q", got.Digest, a.Descriptor.Digest)
	}
	info, err := os.Stat(filepath.Join(destination, "bin", "run"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("executable mode = %04o, want 0755", info.Mode().Perm())
	}
	target, err := os.Readlink(filepath.Join(destination, "bin", "readme"))
	if err != nil || target != "../readme" {
		t.Fatalf("symlink target = %q, err = %v", target, err)
	}
}

func TestDescribeTreeRejectsUnsafeModesAndReservedPaths(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "unsafe")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := DescribeTree(root); err == nil || !strings.Contains(err.Error(), "unsafe mode") {
		t.Fatalf("DescribeTree error = %v, want unsafe mode", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, DescriptorFilename), []byte("reserved"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := DescribeTree(root); err == nil || !strings.Contains(err.Error(), "reserved") {
		t.Fatalf("DescribeTree reserved error = %v", err)
	}
}

func TestExtractArchiveRejectsUnsafeEntries(t *testing.T) {
	tests := []struct {
		name string
		defs []archiveDefinition
		want string
	}{
		{name: "absolute", defs: []archiveDefinition{{name: "/outside", kind: tar.TypeReg, mode: 0o644, body: "x"}}, want: "absolute"},
		{name: "traversal", defs: []archiveDefinition{{name: "../outside", kind: tar.TypeReg, mode: 0o644, body: "x"}}, want: "unsafe"},
		{name: "duplicate", defs: []archiveDefinition{{name: "x", kind: tar.TypeReg, mode: 0o644, body: "x"}, {name: "x", kind: tar.TypeReg, mode: 0o644, body: "y"}}, want: "duplicate"},
		{name: "device", defs: []archiveDefinition{{name: "tty", kind: tar.TypeChar, mode: 0o644}}, want: "unsupported"},
		{name: "hardlink", defs: []archiveDefinition{{name: "alias", kind: tar.TypeLink, mode: 0o644, link: "target"}}, want: "unsupported"},
		{name: "escaping symlink", defs: []archiveDefinition{{name: "escape", kind: tar.TypeSymlink, mode: 0, link: "../../outside"}}, want: "escapes"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := testArchive(t, test.defs)
			outside := filepath.Join(t.TempDir(), "outside")
			destination := filepath.Join(filepath.Dir(outside), "destination")
			if _, err := ExtractArchive(data, destination); err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("ExtractArchive error = %v, want substring %q", err, test.want)
			}
			if _, err := os.Lstat(outside); !os.IsNotExist(err) {
				t.Fatalf("outside path exists after rejection: %v", err)
			}
		})
	}
}

func TestExtractArchiveCleansPartialFilesInExistingDestination(t *testing.T) {
	data := testArchive(t, []archiveDefinition{
		{name: "created", kind: tar.TypeReg, mode: 0o644, body: "partial"},
		{name: "../rejected", kind: tar.TypeReg, mode: 0o644, body: "escape"},
	})
	destination := t.TempDir()
	if _, err := ExtractArchive(data, destination); err == nil {
		t.Fatal("ExtractArchive succeeded for traversal archive")
	}
	entries, err := os.ReadDir(destination)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("partial entries remained in existing destination: %#v", entries)
	}
}

func TestInstallConcurrentReuseRecoveryAndTamperRejection(t *testing.T) {
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime"), []byte("runtime"), 0o755); err != nil {
		t.Fatal(err)
	}
	archive, err := BuildArchive(source)
	if err != nil {
		t.Fatal(err)
	}
	state := t.TempDir()
	base := filepath.Join(state, AssetDirectory)
	if err := os.MkdirAll(filepath.Join(base, ".stage-"+strings.TrimPrefix(archive.Descriptor.Digest, "sha256:")+"-abandoned"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, ".stage-"+strings.TrimPrefix(archive.Descriptor.Digest, "sha256:")+"-abandoned", "junk"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Two simultaneous callers are enough to prove the per-digest lock and the
	// verified-reuse path. More callers only serialize identical tree checks.
	const workers = 2
	results := make(chan InstallResult, workers)
	errorsCh := make(chan error, workers)
	var ready sync.WaitGroup
	ready.Add(workers)
	start := make(chan struct{})
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			ready.Done()
			<-start
			result, err := Install(state, archive)
			if err != nil {
				errorsCh <- err
				return
			}
			results <- result
		})
	}
	ready.Wait()
	close(start)
	wait.Wait()
	close(results)
	close(errorsCh)
	for err := range errorsCh {
		t.Fatal(err)
	}
	var count, reused int
	for result := range results {
		count++
		if result.Reused {
			reused++
		}
		if result.Path == "" || result.Descriptor.Digest != archive.Descriptor.Digest {
			t.Fatalf("bad install result: %#v", result)
		}
	}
	if count != workers {
		t.Fatalf("install results = %d, want %d", count, workers)
	}
	if reused != workers-1 {
		t.Fatalf("reused installs = %d, want %d", reused, workers-1)
	}
	installed, err := InstalledPath(state, archive.Descriptor.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(installed); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(state, archive); err != nil {
		t.Fatalf("verified reuse failed: %v", err)
	}

	if err := os.WriteFile(filepath.Join(installed, "runtime"), []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := Install(state, archive); !errors.Is(err, ErrExistingInstallTampered) {
		t.Fatalf("tamper error = %v, want ErrExistingInstallTampered", err)
	}
	contents, err := os.ReadFile(filepath.Join(installed, "runtime"))
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "tampered" {
		t.Fatalf("tampered final was replaced: %q", contents)
	}
}

func TestInstallContextHonorsCancellation(t *testing.T) {
	state := t.TempDir()
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "runtime"), []byte("runtime"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, err := BuildArchive(source)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(state, AssetDirectory)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	lockPath := filepath.Join(base, "."+strings.TrimPrefix(archive.Descriptor.Digest, "sha256:")+".lock")
	lock, err := acquireAssetLock(context.Background(), lockPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := InstallContext(ctx, state, archive); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("InstallContext error = %v, want cancellation", err)
	}
}

func TestSyncTreeDirectoriesHandlesNestedTree(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "one", "two", "three")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "asset"), []byte("asset"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := syncTreeDirectories(root); err != nil {
		t.Fatalf("syncTreeDirectories: %v", err)
	}
}

type archiveDefinition struct {
	name string
	kind byte
	mode int64
	body string
	link string
}

func testArchive(t *testing.T, definitions []archiveDefinition) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	gz.ModTime = time.Unix(0, 0)
	tarWriter := tar.NewWriter(gz)
	for _, definition := range definitions {
		header := &tar.Header{Name: definition.name, Typeflag: definition.kind, Mode: definition.mode, Linkname: definition.link, ModTime: time.Unix(0, 0)}
		if definition.kind == tar.TypeReg {
			header.Size = int64(len(definition.body))
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if definition.kind == tar.TypeReg {
			if _, err := io.WriteString(tarWriter, definition.body); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func ExampleInstalledPath() {
	path, err := InstalledPath("/var/lib/scenery", "sha256:"+strings.Repeat("a", 64))
	if err != nil {
		panic(err)
	}
	fmt.Println(path)
	// Output: /var/lib/scenery/runtime-assets/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
}
