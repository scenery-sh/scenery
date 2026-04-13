package build

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCopyTreeSkipsHiddenDirsAndBrokenSymlinks(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	writeFile := func(rel, data string) {
		path := filepath.Join(src, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	writeFile("go.mod", "module example\n\ngo 1.25.0\n")
	writeFile("svc/api.go", "package svc\n")
	writeFile("svc/encore.gen.go", "package svc\n")
	writeFile("node_modules/pkg/index.js", "console.log('skip')\n")

	if err := os.MkdirAll(filepath.Join(src, ".cursor", "rules"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("../../CLAUDE.md", filepath.Join(src, ".cursor", "rules", "broken.mdc")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("svc", filepath.Join(src, "svc-link")); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dst, "svc", "api.go")); err != nil {
		t.Fatalf("expected copied Go file: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".cursor")); !os.IsNotExist(err) {
		t.Fatalf("expected hidden directory to be skipped, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "node_modules")); !os.IsNotExist(err) {
		t.Fatalf("expected node_modules to be skipped, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "svc", "encore.gen.go")); !os.IsNotExist(err) {
		t.Fatalf("expected encore.gen.go to be skipped, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "svc-link")); !os.IsNotExist(err) {
		t.Fatalf("expected symlinked directory to be skipped, stat err = %v", err)
	}
}

func TestCopyTreeStripsEncoreCronJobs(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	path := filepath.Join(src, "audit", "retention.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const input = `package audit

import (
	"context"
	"encore.dev/cron"
)

var _ = cron.NewJob("audit-prune", cron.JobConfig{
	Title: "Prune",
	Every: 24 * cron.Hour,
})

func PruneOldLogs(ctx context.Context) error { return nil }
`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "audit", "retention.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "cron.NewJob") || strings.Contains(got, `"encore.dev/cron"`) {
		t.Fatalf("expected Encore cron to be stripped, got:\n%s", got)
	}
	if !strings.Contains(got, "func PruneOldLogs") {
		t.Fatalf("expected endpoint to remain, got:\n%s", got)
	}
}

func TestCopyTreeRewritesEncoreRlogImport(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	path := filepath.Join(src, "svc", "api.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const input = `package svc

import "encore.dev/rlog"

func Hello() {
	rlog.Info("hello", "service", "svc")
}
`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "svc", "api.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, `"encore.dev/rlog"`) {
		t.Fatalf("expected encore.dev/rlog import to be rewritten, got:\n%s", got)
	}
	if !strings.Contains(got, `"pulse.dev/rlog"`) {
		t.Fatalf("expected pulse.dev/rlog import to be present, got:\n%s", got)
	}
}

func TestCopyTreeRewritesEncoreCompatImports(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()

	path := filepath.Join(src, "svc", "api.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	const input = `package svc

import (
	encore "encore.dev"
	"encore.dev/beta/auth"
	"encore.dev/beta/errs"
)

func Hello() {
	_ = encore.Meta()
	_, _ = auth.UserID()
	_ = errs.B()
}
`
	if err := os.WriteFile(path, []byte(input), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := copyTree(src, dst); err != nil {
		t.Fatalf("copyTree returned error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dst, "svc", "api.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	for _, oldPath := range []string{`"encore.dev"`, `"encore.dev/beta/auth"`, `"encore.dev/beta/errs"`} {
		if strings.Contains(got, oldPath) {
			t.Fatalf("expected %s to be rewritten, got:\n%s", oldPath, got)
		}
	}
	for _, newPath := range []string{`encore "pulse.dev"`, `"pulse.dev/auth"`, `"pulse.dev/errs"`} {
		if !strings.Contains(got, newPath) {
			t.Fatalf("expected %s to be present, got:\n%s", newPath, got)
		}
	}
}
