package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestTelemetryBundleRefusesExistingDestinations(t *testing.T) {
	t.Parallel()
	for _, symlink := range []bool{false, true} {
		name := "regular file"
		if symlink {
			name = "dangling symlink"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			output := filepath.Join(root, "result.zip")
			sentinel := []byte("owned existing destination\n")
			if symlink {
				if err := os.Symlink("missing-target", output); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(output, sentinel, 0600); err != nil {
				t.Fatal(err)
			}
			before, err := os.Lstat(output)
			if err != nil {
				t.Fatal(err)
			}
			var stdout bytes.Buffer
			err = runTelemetryBundleCommand(&stdout, []string{"--app-root", root, "--output", output, "-o", "json"})
			if err == nil {
				t.Fatal("existing destination became success")
			}
			diagnostic := cliErrorDiagnostic(err)
			if cliExitCode(err) != 3 || diagnostic.Code != "SCN8003" || diagnostic.ReportToken != "" || stdout.Len() != 0 {
				t.Fatalf("collision became internal failure or success: exit=%d diagnostic=%+v output=%s", cliExitCode(err), diagnostic, stdout.String())
			}
			after, err := os.Lstat(output)
			if err != nil || !os.SameFile(before, after) {
				t.Fatalf("existing destination identity changed: %v", err)
			}
			if symlink {
				if target, err := os.Readlink(output); err != nil || target != "missing-target" {
					t.Fatalf("existing symlink changed: %q %v", target, err)
				}
			} else if data, err := os.ReadFile(output); err != nil || !bytes.Equal(data, sentinel) {
				t.Fatalf("existing bytes changed: %q %v", data, err)
			}
			staging, err := filepath.Glob(filepath.Join(root, ".telemetry-export-*.zip"))
			if err != nil || len(staging) != 0 {
				t.Fatalf("refusal left staging files: %v %v", staging, err)
			}
		})
	}
}
