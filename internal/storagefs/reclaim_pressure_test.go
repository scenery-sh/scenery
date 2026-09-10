package storagefs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestReclaimRecoversFromScratchCreationNoSpace(t *testing.T) {
	testReclaimPressure(t, "create", syscall.ENOSPC, false)
}

func TestReclaimRecoversFromScratchExtensionNoSpace(t *testing.T) {
	testReclaimPressure(t, "extend", syscall.ENOSPC, false)
}

func TestReclaimRecoversFromScratchMergeNoSpace(t *testing.T) {
	// Exercise the real merge writer with small prebuilt runs, then propagate
	// its error through the reclamation scratch boundary. This avoids hundreds
	// of durable filesystem operations in an ordinary test root.
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	if err := root.Mkdir("staging", 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a", "b"} {
		if err := root.WriteFile(name, []byte("1\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, err = mergeOrderedRuns(context.Background(), root, "staging", []string{"a", "b"}, func(a, b int) bool { return a < b }, func(root *os.Root, dir string) (string, io.WriteCloser, error) {
		name, file, err := createOrderedRun(root, dir)
		if err != nil {
			return name, file, err
		}
		return name, &pressureWriter{WriteCloser: file, failAt: 2, err: syscall.ENOSPC}, nil
	})
	if !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("merge error: %v", err)
	}
	entries, readErr := os.ReadDir(filepath.Join(root.Name(), "staging"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("merge scratch cleanup: %v, %v", entries, readErr)
	}
	testReclaimPressure(t, "create", err, true)
}

func TestReclaimRecoversFromScratchQuota(t *testing.T) {
	testReclaimPressure(t, "create", syscall.EDQUOT, false)
}

func TestReclaimPressureReportsPartialDeletion(t *testing.T) {
	testReclaimPressure(t, "create", syscall.ENOSPC, true)
}

func TestReclaimRejectsScratchIOFailure(t *testing.T) {
	s := testStore(t)
	putText(t, s, "live", "old", PutOptions{})
	putText(t, s, "live", "current", PutOptions{})
	s.namespace.io.createOrderedRun = func(*os.Root, string) (string, io.WriteCloser, error) {
		return "", nil, syscall.EIO
	}
	if _, err := s.namespace.PreviewReclaim(context.Background()); !errors.Is(err, syscall.EIO) {
		t.Fatalf("non-space failure was hidden: %v", err)
	}
	if body, _ := readText(t, s, "live"); body != "current" {
		t.Fatal("live payload changed")
	}
}

type pressureWriter struct {
	io.WriteCloser
	writes int
	failAt int
	err    error
}

func (w *pressureWriter) Write(data []byte) (int, error) {
	w.writes++
	if w.writes == w.failAt {
		return 0, w.err
	}
	return w.WriteCloser.Write(data)
}

func testReclaimPressure(t *testing.T, phase string, pressure error, partial bool) {
	t.Helper()
	ctx := context.Background()
	s := testStore(t)
	putText(t, s, "live", "current", PutOptions{})
	lease, err := s.namespace.acquire(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(generationPath(lease.owner.Generation), "staging")
	// Small abandoned uploads keep failure cuts independent of volume timing.
	total := 2
	for i := range total {
		if err := lease.root.WriteFile(filepath.Join(staging, fmt.Sprintf("%032x", i)), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	normal, err := s.namespace.PreviewReclaim(ctx)
	if err != nil {
		t.Fatal(err)
	}
	failures := 0
	s.namespace.io.createOrderedRun = func(root *os.Root, dir string) (string, io.WriteCloser, error) {
		if phase == "create" {
			failures++
			return "", nil, &os.PathError{Op: "create", Path: dir, Err: pressure}
		}
		name, file, err := createOrderedRun(root, dir)
		if err != nil {
			return name, file, err
		}
		failures++
		return name, &pressureWriter{WriteCloser: file, failAt: 2, err: pressure}, nil
	}
	// Apply repeats preview and accepts only the normal selection digest,
	// proving that the fallback computes the identical ordered selection.
	if partial {
		remove := s.namespace.io.remove
		calls := 0
		s.namespace.io.remove = func(root *os.Root, name string) error {
			calls++
			if calls == 2 {
				return syscall.EIO
			}
			return remove(root, name)
		}
	}
	result, err := s.namespace.ApplyReclaim(ctx, normal.SelectionRevision)
	if partial {
		var cut *PartialReclaimError
		if !errors.As(err, &cut) || result.Completion != "partial" || result.Files != 1 {
			t.Fatalf("partial deletion result: %+v, %v", result, err)
		}
	} else if err != nil || result.Completion != "complete" || result.Files != int64(total) || result.Bytes != int64(total) {
		t.Fatalf("reclamation failed under pressure: %+v, %v", result, err)
	}
	if failures < 2 {
		t.Fatalf("did not inject every selection pass: %d", failures)
	}
	if body, _ := readText(t, s, "live"); body != "current" {
		t.Fatal("live payload reclaimed")
	}
	entries, err := os.ReadDir(filepath.Join(s.namespace.Path, staging))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if isOrderedRun(entry.Name()) {
			t.Fatalf("scratch leaked: %s", entry.Name())
		}
	}
}
