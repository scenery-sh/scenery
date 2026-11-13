package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestPrepareWorktreeInputsReceivesOnlyIgnoredDeclaredFiles(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	source, target := filepath.Join(root, "main"), filepath.Join(root, "linked")
	write := func(path, data string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(source, "data", "ignored.csv"), "ignored")
	write(filepath.Join(source, "data", "tracked.csv"), "tracked")
	write(filepath.Join(source, "data", "present.csv"), "main")
	write(filepath.Join(target, "data", "present.csv"), "linked")
	write(filepath.Join(root, "outside.csv"), "outside")
	if err := os.Symlink(filepath.Join(root, "outside.csv"), filepath.Join(source, "data", "link.csv")); err != nil {
		t.Fatal(err)
	}
	cfg := appcfg.Config{}
	cfg.Database.Seed.Commands = []appcfg.DatabaseSeedCommandConfig{{Name: "catalog", Inputs: []string{
		"data/ignored.csv", "./data/ignored.csv", "data/tracked.csv", "data/present.csv", "data/link.csv", "data/absent.csv", "../outside.csv", "/etc/hosts",
	}}}
	ignored := func(_ context.Context, _, relative string) bool { return relative != filepath.Join("data", "tracked.csv") }

	prepared, err := prepareWorktreeInputs(context.Background(), target, source, cfg, ignored)
	if err != nil {
		t.Fatal(err)
	}
	if len(prepared) != 1 || prepared[0].Path != "data/ignored.csv" {
		t.Fatalf("prepared = %+v, want only the ignored regular file the checkout lacks", prepared)
	}
	if data, err := os.ReadFile(filepath.Join(target, "data", "ignored.csv")); err != nil || string(data) != "ignored" {
		t.Fatalf("received input = %q, %v", data, err)
	}
	if data, _ := os.ReadFile(filepath.Join(target, "data", "present.csv")); string(data) != "linked" {
		t.Fatalf("an input the checkout already held was replaced: %q", data)
	}
	for _, absent := range []string{"data/tracked.csv", "data/link.csv", "outside.csv"} {
		if _, err := os.Lstat(filepath.Join(target, absent)); !os.IsNotExist(err) {
			t.Fatalf("%s was created: %v", absent, err)
		}
	}
	// The received file is the checkout's own: changing it leaves the origin.
	if err := os.WriteFile(filepath.Join(target, "data", "ignored.csv"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(filepath.Join(source, "data", "ignored.csv")); string(data) != "ignored" {
		t.Fatalf("origin changed with the received input: %q", data)
	}
	again, err := prepareWorktreeInputs(context.Background(), target, source, cfg, ignored)
	if err != nil || len(again) != 0 {
		t.Fatalf("repeated preparation = %+v, %v; want nothing left to receive", again, err)
	}
}

func TestCorrespondingAppRootMapsNestedApplicationRoots(t *testing.T) {
	t.Parallel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	linked, main := filepath.Join(root, "linked"), filepath.Join(root, "main")
	for _, dir := range []string{filepath.Join(linked, "apps", "demo"), main} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := correspondingAppRoot(linked, main, filepath.Join(linked, "apps", "demo"))
	if err != nil || got != filepath.Join(main, "apps", "demo") {
		t.Fatalf("corresponding root = %q, %v", got, err)
	}
	if got, err := correspondingAppRoot(main, main, main); err != nil || got != "" {
		t.Fatalf("main checkout maps to %q, %v; want none", got, err)
	}
	if _, err := correspondingAppRoot(linked, main, root); err == nil {
		t.Fatal("an app root outside its checkout was mapped")
	}
}
