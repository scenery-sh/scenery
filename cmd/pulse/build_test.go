package main

import (
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

	if err := signBuiltBinaryIfNeeded("/tmp/pulse-app"); err != nil {
		t.Fatalf("signBuiltBinaryIfNeeded returned error: %v", err)
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

	target := filepath.Join(t.TempDir(), "pulse-app")
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
