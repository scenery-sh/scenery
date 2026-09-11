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

func TestSourceAdmissionKeepsAuthoredDecisionsWithoutGeneratedInventory(t *testing.T) {
	for _, change := range []string{"unchanged", "edit", "add", "delete", "embed edit", "embed add", "embed delete", "generated edit", "embedded generated edit", "managed authored edit"} {
		t.Run(change, func(t *testing.T) {
			root := t.TempDir()
			writeWatchFile(t, root, "svc/api.go", "package svc\n//go:embed assets/*.txt\nconst value = 1\n")
			writeWatchFile(t, root, "svc/assets/a.txt", "first")
			writeWatchFile(t, root, "svc/scenerycontract/scenery.package-generated.json", `{"kind":"scenery.package-generated","files":["types.gen.go"]}`)
			writeWatchFile(t, root, "svc/scenerycontract/types.gen.go", "package scenerycontract\nconst generated = 1\n")
			writeWatchFile(t, root, "svc/scenerycontract/notes.go", "package scenerycontract\nconst authored = 1\n")
			if change == "embedded generated edit" {
				writeWatchFile(t, root, "svc/api.go", "package svc\n//go:embed scenerycontract/types.gen.go\n")
			}
			before, err := scanWatchedFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "edit":
				path := filepath.Join(root, "svc/api.go")
				info, err := os.Stat(path)
				if err != nil {
					t.Fatal(err)
				}
				writeWatchFile(t, root, "svc/api.go", "package svc\n//go:embed assets/*.txt\nconst value = 2\n")
				if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "add":
				writeWatchFile(t, root, "svc/added.go", "package svc\n")
			case "delete":
				if err := os.Remove(filepath.Join(root, "svc/api.go")); err != nil {
					t.Fatal(err)
				}
			case "embed edit":
				writeWatchFile(t, root, "svc/assets/a.txt", "other")
			case "embed add":
				writeWatchFile(t, root, "svc/assets/b.txt", "new")
			case "embed delete":
				if err := os.Remove(filepath.Join(root, "svc/assets/a.txt")); err != nil {
					t.Fatal(err)
				}
			case "generated edit", "embedded generated edit":
				writeWatchFile(t, root, "svc/scenerycontract/types.gen.go", "package scenerycontract\nconst generated = 2\n")
			case "managed authored edit":
				writeWatchFile(t, root, "svc/scenerycontract/notes.go", "package scenerycontract\nconst authored = 2\n")
			}
			full, err := scanWatchedFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			admission, err := scanSourceAdmissionFiles(root)
			if err != nil {
				t.Fatal(err)
			}
			if snapshotFingerprint(full) != snapshotFingerprint(admission) {
				t.Fatal("admission changed authored identity")
			}
			if len(admission.generatedContent) != 0 {
				t.Fatal("admission computed an unused generated inventory")
			}
			unchanged := snapshotFingerprint(before) == snapshotFingerprint(admission)
			if unchanged != (change == "unchanged" || change == "generated edit") {
				t.Fatalf("unexpected admission decision for %s", change)
			}
			if len(full.generatedContent) == 0 {
				t.Fatal("ordinary watcher lost generated repair state")
			}
		})
	}
}

func TestDevPreparationKeepsEditsPendingAfterFailedAdmission(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "main.go")
	writeWatchFile(t, root, "main.go", "package main\n")
	before, err := scanWatchedFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	p := &devBuildPreparation{root: root, snapshot: before, result: &build.Result{GraphFingerprint: snapshotFingerprint(before)}}
	err = p.compile(context.Background(), func(context.Context, *build.Result) error {
		return os.WriteFile(file, []byte("package main\nconst edit=1\n"), 0600)
	})
	if err == nil {
		t.Fatal("admitted source changed after compilation")
	}
	current, err := scanWatchedFilesReusing(root, p.snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(changedPaths(p.snapshot, current), []string{"main.go"}) {
		t.Fatal("final admission consumed a pending edit")
	}
	failure := errors.New("invalid candidate")
	if err := p.compile(context.Background(), func(context.Context, *build.Result) error { return failure }); !errors.Is(err, failure) {
		t.Fatal("lost compile failure")
	}
	p.snapshot = current
	if err := p.compile(context.Background(), func(context.Context, *build.Result) error { return nil }); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.compile(ctx, func(context.Context, *build.Result) error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal("admitted canceled preparation")
	}
}
