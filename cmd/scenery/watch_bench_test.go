package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// benchWatchTree builds a synthetic app tree shaped like a steady-state watch
// target: several packages of Go files, nested .gitignore files, and a
// .scenery.json with watch.ignore patterns.
func benchWatchTree(b *testing.B) string {
	b.Helper()
	root := b.TempDir()
	writeBench := func(rel, content string) {
		b.Helper()
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			b.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			b.Fatal(err)
		}
	}
	writeBench("app.scn", "application \"bench\" {}\n")
	writeBench(".scenery.json", `{"name":"bench","watch":{"ignore":["tmp/","*.bak"]}}`)
	writeBench(".gitignore", "dist/\n*.log\n")
	for pkg := 0; pkg < 8; pkg++ {
		dir := fmt.Sprintf("pkg%d", pkg)
		writeBench(dir+"/.gitignore", "testdata/out/\n")
		for i := 0; i < 15; i++ {
			writeBench(fmt.Sprintf("%s/file%d.go", dir, i),
				fmt.Sprintf("package pkg%d\n\nvar V%d = %d\n", pkg, i, i))
		}
	}
	return root
}

func BenchmarkScanWatchedFilesReusing(b *testing.B) {
	root := benchWatchTree(b)
	previous, err := scanWatchedFiles(root)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		next, err := scanWatchedFilesReusing(root, previous)
		if err != nil {
			b.Fatal(err)
		}
		previous = next
	}
}

func BenchmarkSnapshotFingerprint(b *testing.B) {
	snapshot := fileSnapshot{files: make(map[string]fileStamp, 200)}
	base := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 200; i++ {
		snapshot.files[fmt.Sprintf("pkg%d/file%d.go", i%8, i)] = fileStamp{
			modTime: base.Add(time.Duration(i) * time.Second),
			size:    int64(1000 + i),
			mode:    0o644,
			hash:    fmt.Sprintf("%064x", i),
			embed:   i%7 == 0,
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = snapshotFingerprint(snapshot)
	}
}
