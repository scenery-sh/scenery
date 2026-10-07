package rotatinglog

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotationRetainsBoundedContinuousTail(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "run.log")
	w, err := open(path, 16, 3)
	if err != nil {
		t.Fatal(err)
	}
	input := strings.Repeat("0123456789", 10)
	for _, part := range []string{input[:23], input[23:]} {
		if n, err := w.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write: %d %v", n, err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var retained strings.Builder
	for _, segment := range Segments(path) {
		data, err := os.ReadFile(segment)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 16 {
			t.Fatalf("segment size %d", len(data))
		}
		retained.Write(data)
	}
	if got := retained.String(); got != input[48:] {
		t.Fatalf("tail = %q", got)
	}
}

func TestPruneKeepsActiveAndLatestSessions(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"run-1.log", "run-1.log.1", "run-2.log", "run-3.log", "run-4.log", "run-5.log", "unrelated.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("log"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prune(filepath.Join(dir, "run-2.log")); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"run-1.log", "run-1.log.1"} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Fatalf("old file retained: %s", name)
		}
	}
	for _, name := range []string{"run-2.log", "run-3.log", "run-4.log", "run-5.log", "unrelated.txt"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatal(err)
		}
	}
}
