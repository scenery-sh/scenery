package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"scenery.sh/internal/build"
)

func TestDevPreparationRejectsConcurrentEditWithoutReplacingAcceptedGraph(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, []byte("package main\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	accepted := &build.Result{GraphFingerprint: "previous"}
	native := &nativeDevPreparation{accepted: accepted}
	p := &devBuildPreparation{root: root, snapshot: before, native: native, result: &build.Result{GraphFingerprint: snapshotFingerprint(before)}}
	err = p.compile(context.Background(), func(context.Context, *build.Result) error {
		return os.WriteFile(file, []byte("package main\nconst edit = 1\n"), 0600)
	})
	if err == nil || native.accepted != accepted {
		t.Fatal("concurrent edit admitted or replaced accepted generation")
	}
	current, err := scanWatchedFilesReusing(root, before)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changedPaths(before, current), []string{"main.go"}) {
		t.Fatal("pending edit lost")
	}
	p.snapshot = current
	failure := errors.New("candidate rejected")
	if err := p.compile(context.Background(), func(context.Context, *build.Result) error { return failure }); !errors.Is(err, failure) || native.accepted != accepted {
		t.Fatal("failed candidate changed accepted ownership")
	}
	if err := p.compile(context.Background(), func(context.Context, *build.Result) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if native.accepted != p.result {
		t.Fatal("verified replacement not accepted")
	}
}
