package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSignBuiltBinaryIfNeededSkipsOutsideDarwin(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := execLookPath
	prevCommand := execCommand
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		execLookPath = prevLookPath
		execCommand = prevCommand
	})

	currentGOOS = func() string { return "linux" }
	execLookPath = func(file string) (string, error) {
		t.Fatalf("execLookPath should not be called on non-darwin")
		return "", nil
	}

	if err := signBuiltBinaryIfNeeded("/tmp/onlava-app"); err != nil {
		t.Fatalf("signBuiltBinaryIfNeeded returned error: %v", err)
	}
}

func TestCopyBinarySkipsUnchangedOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("binary"), 0o644); err != nil {
		t.Fatal(err)
	}

	copied, err := copyBinary(src, dst)
	if err != nil {
		t.Fatalf("copyBinary: %v", err)
	}
	if copied {
		t.Fatal("copyBinary copied unchanged output")
	}
	info, err := os.Stat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Fatalf("dst mode = %v, want 0755", got)
	}
}

func TestCopyBinaryRewritesChangedOutput(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}

	copied, err := copyBinary(src, dst)
	if err != nil {
		t.Fatalf("copyBinary: %v", err)
	}
	if !copied {
		t.Fatal("copyBinary did not copy changed output")
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("dst = %q, want new", data)
	}
}

func TestSignBuiltBinaryIfNeededRunsCodesignOnDarwin(t *testing.T) {
	prevGOOS := currentGOOS
	prevLookPath := execLookPath
	prevCommand := execCommand
	t.Cleanup(func() {
		currentGOOS = prevGOOS
		execLookPath = prevLookPath
		execCommand = prevCommand
	})

	currentGOOS = func() string { return "darwin" }
	execLookPath = func(file string) (string, error) {
		if file != "codesign" {
			t.Fatalf("unexpected lookup: %q", file)
		}
		return "/usr/bin/codesign", nil
	}

	var gotName string
	var gotArgs []string
	execCommand = func(name string, args ...string) *exec.Cmd {
		gotName = name
		gotArgs = append([]string(nil), args...)
		return exec.Command("sh", "-c", "exit 0")
	}

	target := filepath.Join(t.TempDir(), "onlava-app")
	if err := signBuiltBinaryIfNeeded(target); err != nil {
		t.Fatalf("signBuiltBinaryIfNeeded returned error: %v", err)
	}

	if gotName != "/usr/bin/codesign" {
		t.Fatalf("codesign command = %q", gotName)
	}
	want := []string{"--force", "--sign", "-", target}
	if strings.Join(gotArgs, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("codesign args = %q, want %q", gotArgs, want)
	}
}
