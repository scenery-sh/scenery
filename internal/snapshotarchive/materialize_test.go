package snapshotarchive

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"
)

func TestMaterializationPublishesVerifiedFilesAndEvicts(t *testing.T) {
	ctx := context.Background()
	r, err := Open(ctx, writeArchive(t, archiveBytes(t, testObject("value"))), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	m, err := r.Materialize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()
	if err := m.VisitStore(ctx, "files", func(object Object, body io.Reader) error {
		if _, ok := body.(*os.File); !ok {
			t.Fatal("materialization did not provide a cloneable descriptor")
		}
		return copyVerified(ctx, io.Discard, body, object.SizeBytes, object.SHA256)
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(m.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("source was not evicted: %v", err)
	}
	if err := m.VisitStore(ctx, "files", func(Object, io.Reader) error { t.Fatal("visited evicted source"); return nil }); err == nil {
		t.Fatal("accepted closed source")
	}
}
