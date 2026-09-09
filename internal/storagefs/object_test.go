package storagefs

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"scenery.sh/internal/atomicfile"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := testNamespace(t).Store(Scope{Store: "app", Tenant: "tenant-a"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func putText(t *testing.T, s *Store, key, body string, opts PutOptions) *Object {
	t.Helper()
	o, err := s.Put(context.Background(), key, strings.NewReader(body), opts)
	if err != nil {
		t.Fatal(err)
	}
	return o
}

func readText(t *testing.T, s *Store, key string) (string, *Object) {
	t.Helper()
	r, o, err := s.Get(context.Background(), key, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	return string(data), o
}

func TestImmutableStreamSurvivesReplacementAndDeletion(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{Metadata: map[string]string{"version": "old"}})
	r, opened, err := s.Get(context.Background(), "a", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	newObject := putText(t, s, "a", "new", PutOptions{IfMatch: old.ETag})
	if err := s.Delete(context.Background(), "a", DeleteOptions{IfMatch: newObject.ETag}); err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "old" || opened.ETag != old.ETag || opened.Metadata["version"] != "old" {
		t.Fatalf("mixed immutable version: %q, %+v, %v", data, opened, err)
	}
	if _, err := s.Head(context.Background(), "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted reference remains: %v", err)
	}
}

func TestReplacementETagIsNotContentDigest(t *testing.T) {
	s := testStore(t)
	a := putText(t, s, "a", "same", PutOptions{})
	b := putText(t, s, "a", "same", PutOptions{Metadata: map[string]string{"changed": "metadata"}, IfMatch: a.ETag})
	c := putText(t, s, "a", "same", PutOptions{IfMatch: b.ETag})
	if a.ETag == b.ETag || a.ETag == c.ETag || b.ETag == c.ETag || a.SHA256 != b.SHA256 || a.SHA256 != c.SHA256 {
		t.Fatal("opaque replacement versions did not advance independently of digest")
	}
	if err := s.Delete(context.Background(), "a", DeleteOptions{IfMatch: a.ETag}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale delete: %v", err)
	}
}

func TestCreateOnlyCompetitorsHaveOneWinner(t *testing.T) {
	s := testStore(t)
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			_, err := s.Put(context.Background(), "one", strings.NewReader("payload"), PutOptions{IfNoneMatch: true})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrPrecondition) {
				t.Errorf("create-only: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners=%d", wins.Load())
	}
}

func TestConditionalCompetitorsHaveOneWinner(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "one", "initial", PutOptions{})
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 3 {
		wg.Go(func() {
			_, err := s.Put(context.Background(), "one", strings.NewReader("replacement"), PutOptions{IfMatch: old.ETag})
			if err == nil {
				wins.Add(1)
			} else if !errors.Is(err, ErrPrecondition) {
				t.Errorf("conditional put: %v", err)
			}
		})
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("winners=%d", wins.Load())
	}
}

func TestLogicalKeysAndTenantPartitionsDoNotAlias(t *testing.T) {
	s := testStore(t)
	for _, key := range []string{"a", "a/b", "A", "žluťoučký"} {
		putText(t, s, key, key, PutOptions{})
	}
	other, err := s.namespace.Store(Scope{Store: "app", Tenant: "tenant-b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	putText(t, other, "a", "other tenant", PutOptions{})
	for _, key := range []string{"a", "a/b", "A", "žluťoučký"} {
		if body, _ := readText(t, s, key); body != key {
			t.Fatalf("aliased key %q", key)
		}
	}
	if body, _ := readText(t, other, "a"); body != "other tenant" {
		t.Fatal("tenant alias")
	}
}

func TestHeadReadsNoPayload(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "payload", PutOptions{})
	s.namespace.io.openPayload = func(*os.Root, string) (*os.File, error) { t.Fatal("Head opened payload"); return nil, nil }
	o, err := s.Head(context.Background(), "a")
	if err != nil || o.ETag != old.ETag || o.SizeBytes != 7 {
		t.Fatalf("Head: %+v, %v", o, err)
	}
}

func TestRangeRetainsFullObjectSize(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "0123456789", PutOptions{})
	offset, length := int64(3), int64(2)
	r, o, err := s.Get(context.Background(), "a", GetOptions{Offset: &offset, Length: &length})
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "34" || o.SizeBytes != 10 {
		t.Fatalf("range %q, %+v, %v", data, o, err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	offset = 10
	r, _, err = s.Get(context.Background(), "a", GetOptions{Offset: &offset})
	if err != nil {
		t.Fatal(err)
	}
	data, err = io.ReadAll(r)
	_ = r.Close()
	if err != nil || len(data) != 0 {
		t.Fatalf("EOF range: %q, %v", data, err)
	}
}

func TestMissingPayloadIsCorruptionNotNotFound(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "payload", PutOptions{})
	lease, err := s.namespace.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	ref, err := readReference(lease.root, lease.owner.Generation, s.scope, "a")
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.root.Remove(s.scope.versionPath(lease.owner.Generation, "a", ref.VersionID)); err != nil {
		t.Fatal(err)
	}
	_ = lease.Close()
	if _, _, err := s.Get(context.Background(), "a", GetOptions{}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing payload: %v", err)
	}
}

func TestFailedUploadNeverPublishesPartialObject(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{})
	failure := errors.New("transport interrupted")
	_, err := s.Put(context.Background(), "a", io.MultiReader(strings.NewReader("partial"), errorReader{failure}), PutOptions{})
	if !errors.Is(err, failure) {
		t.Fatalf("upload: %v", err)
	}
	if data, o := readText(t, s, "a"); data != "old" || o.ETag != old.ETag {
		t.Fatal("failed body was published")
	}
}

type errorReader struct{ err error }

func (r errorReader) Read([]byte) (int, error) { return 0, r.err }

func TestReferenceSyncUncertaintyRetainsCompleteNewObject(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{})
	replace := s.namespace.io.replace
	failure := errors.New("injected reference directory sync failure")
	s.namespace.io.replace = func(r *os.Root, name string, data []byte) error {
		if err := replace(r, name, data); err != nil {
			return err
		}
		return &atomicfile.PublicationError{Err: failure}
	}
	_, err := s.Put(context.Background(), "a", strings.NewReader("new"), PutOptions{IfMatch: old.ETag})
	var uncertain *atomicfile.PublicationError
	if !errors.As(err, &uncertain) {
		t.Fatalf("publication: %v", err)
	}
	if data, o := readText(t, s, "a"); data != "new" || o.ETag == old.ETag {
		t.Fatal("uncertain publication is not a complete new object")
	}
}

func TestPayloadRenameFailurePreservesReference(t *testing.T) {
	s := testStore(t)
	old := putText(t, s, "a", "old", PutOptions{})
	failure := errors.New("injected rename failure")
	s.namespace.io.rename = func(*os.Root, string, string) error { return failure }
	if _, err := s.Put(context.Background(), "a", strings.NewReader("new"), PutOptions{}); !errors.Is(err, failure) {
		t.Fatalf("rename: %v", err)
	}
	if data, o := readText(t, s, "a"); data != "old" || o.ETag != old.ETag {
		t.Fatal("payload rename failure replaced reference")
	}
	owner, err := Discover(context.Background(), s.namespace.Path, s.namespace.Binding)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(s.namespace.Path, generationPath(owner.Generation), "staging"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("staging leaked: %v, %v", entries, err)
	}
}

func TestConditionalAbsenceAndSizeLimit(t *testing.T) {
	s := testStore(t)
	if err := s.Delete(context.Background(), "absent", DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete(context.Background(), "absent", DeleteOptions{IfMatch: `"missing"`}); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("conditional absence: %v", err)
	}
	s.maxBytes = 3
	if _, err := s.Put(context.Background(), "a", strings.NewReader("four"), PutOptions{}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("size limit: %v", err)
	}
	if _, err := s.Head(context.Background(), "a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("oversized upload published: %v", err)
	}
}
