package main

import (
	"path/filepath"
	"testing"
)

func TestPreferredDevPortStableForAppRoot(t *testing.T) {
	root := filepath.Join(t.TempDir(), "app")
	first, err := preferredDevPort(root, 4001, 4999)
	if err != nil {
		t.Fatal(err)
	}
	second, err := preferredDevPort(root, 4001, 4999)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("preferred port not stable: %d then %d", first, second)
	}
	if first < 4001 || first > 4999 {
		t.Fatalf("preferred port %d outside range", first)
	}
}
