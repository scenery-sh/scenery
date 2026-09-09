package atomicfile

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type interruptedReader struct{}

func (interruptedReader) Read([]byte) (int, error) { return 0, context.Canceled }

func TestCopyRootFailuresPreserveDestination(t *testing.T) {
	for _, source := range []io.Reader{strings.NewReader("short"), strings.NewReader("too long"), io.MultiReader(strings.NewReader("par"), interruptedReader{})} {
		r, err := os.OpenRoot(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		if err := r.WriteFile("output", []byte("previous"), 0o640); err != nil {
			t.Fatal(err)
		}
		before, _ := r.Stat("output")
		if err := CopyRoot(r, "output", source, 6, 0o600, Options{}); err == nil {
			t.Fatal("accepted incomplete or oversized transfer")
		}
		data, err := r.ReadFile("output")
		after, statErr := r.Stat("output")
		if err != nil || statErr != nil || string(data) != "previous" || !os.SameFile(before, after) || before.Mode() != after.Mode() {
			t.Fatal("failed transfer changed existing destination")
		}
		if err := r.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRootReplacementReportsUncertainDirectorySync(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	failure := errors.New("injected directory sync failure")
	err = writeRoot(r, "ref.json", []byte("new"), 0o600, Options{SyncFile: true, SyncDir: true}, func(f *os.File) error {
		info, err := f.Stat()
		if err != nil {
			return err
		}
		if info.IsDir() {
			return failure
		}
		return nil
	})
	var uncertain *PublicationError
	if !errors.As(err, &uncertain) || !errors.Is(err, failure) {
		t.Fatalf("expected uncertain publication, got %v", err)
	}
	got, err := r.ReadFile("ref.json")
	if err != nil || string(got) != "new" {
		t.Fatalf("replacement missing: %q, %v", got, err)
	}
}

func TestRootReplacementFailurePreservesOldFile(t *testing.T) {
	r, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.WriteFile("ref.json", []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected payload sync failure")
	err = writeRoot(r, "ref.json", []byte("new"), 0o600, Options{SyncFile: true, SyncDir: true}, func(*os.File) error { return failure })
	var uncertain *PublicationError
	if !errors.Is(err, failure) || errors.As(err, &uncertain) {
		t.Fatalf("expected definite pre-publication failure: %v", err)
	}
	got, err := r.ReadFile("ref.json")
	if err != nil || string(got) != "old" {
		t.Fatalf("previous file changed: %q, %v", got, err)
	}
}
