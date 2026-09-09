package storagefs

import (
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestPutSynchronizationCutsPreserveCommittedVersion(t *testing.T) {
	// Select the actual boundary rather than counting parent-directory syncs.
	for failAt := 0; failAt <= 2; failAt++ {
		s := testStore(t)
		old := putText(t, s, "a", "old", PutOptions{Metadata: map[string]string{"version": "old"}})
		renamed, failed, publicationSyncs := false, false, 0
		rename := s.namespace.io.rename
		s.namespace.io.rename = func(root *os.Root, from, to string) error {
			err := rename(root, from, to)
			renamed = err == nil
			return err
		}
		cut := errors.New("injected publication synchronization failure")
		s.namespace.io.syncFile = func(file *os.File) error {
			info, err := file.Stat()
			if err != nil {
				return err
			}
			if renamed {
				publicationSyncs++
			}
			// 0 = staged payload flush; 1/2 = destination/staging directory
			// synchronization immediately after the immutable payload rename.
			if !failed && ((failAt == 0 && !info.IsDir()) || (renamed && publicationSyncs == failAt)) {
				failed = true
				return cut
			}
			return nil
		}
		_, err := s.Put(context.Background(), "a", strings.NewReader("new"), PutOptions{IfMatch: old.ETag})
		if !errors.Is(err, cut) || !failed {
			t.Fatalf("sync cut %d was not reached: %v", failAt, err)
		}
		if value, object := readText(t, s, "a"); value != "old" || object.ETag != old.ETag || object.Metadata["version"] != "old" {
			t.Fatalf("sync cut %d published a mixed version", failAt)
		}
	}
}

func TestReferencePublicationFailurePreservesCommittedVersion(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{Metadata: map[string]string{"version": "old"}})
	cut := errors.New("reference publication failed before replacement")
	s.namespace.io.replace = func(*os.Root, string, []byte) error { return cut }
	if _, err := s.Put(context.Background(), "a", strings.NewReader("new"), PutOptions{IfMatch: old.ETag}); !errors.Is(err, cut) {
		t.Fatal(err)
	}
	if body, object := readText(t, s, "a"); body != "old" || object.ETag != old.ETag || object.Metadata["version"] != "old" {
		t.Fatal("failed reference publication changed the committed version")
	}
	preview, err := s.namespace.PreviewReclaim(context.Background())
	if err != nil || preview.Files != 1 {
		t.Fatalf("failed publication should leave only its unreachable new payload reclaimable: %+v %v", preview, err)
	}
}

func TestStreamOpenFailureReleasesMaintenanceLease(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	cut := errors.New("payload open failed")
	s.namespace.io.openPayload = func(*os.Root, string) (*os.File, error) { return nil, cut }
	stream, object, err := s.Get(context.Background(), "a", GetOptions{})
	if !errors.Is(err, cut) || !errors.Is(err, ErrCorrupt) || stream != nil || object != nil {
		t.Fatalf("failed stream open: stream=%v object=%v error=%v", stream, object, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	capture, err := s.namespace.Capture(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestReclaimRequiresDurableReferenceAbsence(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "old", PutOptions{})
	ctx := context.Background()
	cut := errors.New("reference directory sync failed")
	s.namespace.io.syncFile = func(*os.File) error { return cut }
	if err := s.Delete(ctx, "a", DeleteOptions{}); !errors.Is(err, cut) {
		t.Fatal(err)
	}
	preview, err := s.namespace.PreviewReclaim(ctx)
	if err != nil || preview.Files != 1 {
		t.Fatalf("expected retained payload after uncertain delete: %+v %v", preview, err)
	}
	removed := false
	s.namespace.io.remove = func(*os.Root, string) error { removed = true; return nil }
	if _, err := s.namespace.ApplyReclaim(ctx, preview.SelectionRevision); !errors.Is(err, cut) || removed {
		t.Fatalf("reclaimed before reference absence became durable: removed=%t error=%v", removed, err)
	}
}

type cancelUploadReader struct{ cancel context.CancelFunc }

func (r cancelUploadReader) Read(p []byte) (int, error) {
	r.cancel()
	return copy(p, "partial"), io.EOF
}

func TestCanceledUploadReleasesNamespaceLease(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if _, err := s.Put(ctx, "a", cancelUploadReader{cancel}, PutOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if body, object := readText(t, s, "a"); body != "old" || object.ETag != old.ETag {
		t.Fatal("cancellation published a partial upload")
	}
	captureCtx, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer stop()
	capture, err := s.namespace.Capture(captureCtx)
	if err != nil {
		t.Fatal(err)
	}
	if err := capture.Close(); err != nil {
		t.Fatal(err)
	}
}
