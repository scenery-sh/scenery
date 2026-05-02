package main

import (
	"path/filepath"
	"testing"
)

func TestResolveAppRoot(t *testing.T) {
	if got, err := resolveAppRoot(""); err != nil || got != "." {
		t.Fatalf("resolveAppRoot(\"\") = %q, %v; want \".\", nil", got, err)
	}

	root := t.TempDir()
	got, err := resolveAppRoot(root)
	if err != nil {
		t.Fatalf("resolveAppRoot returned error: %v", err)
	}
	if got != filepath.Clean(root) {
		t.Fatalf("resolveAppRoot(%q) = %q, want %q", root, got, filepath.Clean(root))
	}
}
