package storagefs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestCloneFallbackClassification(t *testing.T) {
	for _, err := range []error{syscall.EXDEV, syscall.ENOTSUP, syscall.ENOSYS} {
		if !cloneUnavailable(&os.PathError{Op: "clone", Path: "payload", Err: err}) {
			t.Fatal(err)
		}
	}
	for _, err := range []error{syscall.EACCES, syscall.EIO, syscall.EINVAL, syscall.EEXIST} {
		if cloneUnavailable(err) {
			t.Fatal(err)
		}
	}
}

func TestRestoreCloneVerifiesIndependentPayload(t *testing.T) {
	n := testNamespace(t)
	// Emulate the syscall boundary, not native clone capability.
	n.io.clonePayload = func(source *os.File, root *os.Root, name string) (*os.File, bool, error) {
		f, err := root.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return nil, false, err
		}
		_, err = io.Copy(f, source)
		if err == nil {
			_, err = f.Seek(0, io.SeekStart)
		}
		if err != nil {
			return nil, false, errors.Join(err, f.Close())
		}
		return f, true, nil
	}
	sourcePath := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(sourcePath, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	source, err := os.Open(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = source.Close() }()
	ctx := context.Background()
	r, err := n.BeginRestore(ctx, strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.Stage(ctx, func(g *Generation) error {
		if err := g.Put(ctx, fixtureObject("a", "value"), source, "fail"); err != nil {
			return err
		}
		if g.Cloned != 1 || g.Copied != 0 {
			t.Fatalf("wrong methods: %+v", g)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(sourcePath); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	s, _ := n.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	body, _, err := s.Get(ctx, "a", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	value, err := io.ReadAll(body)
	if err := errors.Join(err, body.Close()); err != nil {
		t.Fatal(err)
	}
	if string(value) != "value" {
		t.Fatal("source eviction damaged target")
	}
}

func TestRestoreCloneRealErrorDoesNotCopy(t *testing.T) {
	n := testNamespace(t)
	n.io.clonePayload = func(*os.File, *os.Root, string) (*os.File, bool, error) { return nil, false, syscall.EACCES }
	path := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(path, []byte("value"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	r, err := n.BeginRestore(context.Background(), strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	if err := r.Stage(context.Background(), func(g *Generation) error {
		return g.Put(context.Background(), fixtureObject("a", "value"), f, "fail")
	}); !errors.Is(err, syscall.EACCES) {
		t.Fatal(err)
	}
}
