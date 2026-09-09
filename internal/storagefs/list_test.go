package storagefs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestListOrdersObjectsAndPrefixesWithExactEnd(t *testing.T) {
	s := testStore(t)
	for _, key := range []string{"c/z", "a", "b/z", "b/x", "d"} {
		putText(t, s, key, key, PutOptions{Metadata: map[string]string{"private": "metadata"}})
	}
	first, err := s.List(context.Background(), ListOptions{Delimiter: "/", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Objects) != 1 || first.Objects[0].Key != "a" || first.Objects[0].Metadata != nil || !reflect.DeepEqual(first.Prefixes, []string{"b/"}) || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	last, err := s.List(context.Background(), ListOptions{Delimiter: "/", Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(last.Objects) != 1 || last.Objects[0].Key != "d" || !reflect.DeepEqual(last.Prefixes, []string{"c/"}) || last.NextCursor != "" {
		t.Fatalf("exact final page: %+v", last)
	}
}

func TestPrefixOnlyPageHasNoFalseCursor(t *testing.T) {
	s := testStore(t)
	for _, key := range []string{"dir/a", "dir/b", "dir/c"} {
		putText(t, s, key, key, PutOptions{})
	}
	page, err := s.List(context.Background(), ListOptions{Delimiter: "/", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Objects) != 0 || !reflect.DeepEqual(page.Prefixes, []string{"dir/"}) || page.NextCursor != "" {
		t.Fatalf("prefix-only end: %+v", page)
	}
}

func TestListCursorRejectsDifferentScopeAndMalformedInput(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "a", PutOptions{})
	putText(t, s, "b", "b", PutOptions{})
	first, err := s.List(context.Background(), ListOptions{Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, opts := range []ListOptions{{Cursor: "a"}, {Cursor: first.NextCursor, Prefix: "a"}, {Cursor: first.NextCursor, Delimiter: "/"}} {
		if _, err := s.List(context.Background(), opts); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid cursor accepted: %+v, %v", opts, err)
		}
	}
	other, err := s.namespace.Store(Scope{Store: "app", Tenant: "tenant-b"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.List(context.Background(), ListOptions{Cursor: first.NextCursor}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cross-tenant cursor: %v", err)
	}
}

func TestListAndStatsNeverOpenPayload(t *testing.T) {
	s := testStore(t)
	putText(t, s, "a", "12345", PutOptions{})
	putText(t, s, "b", "123", PutOptions{})
	s.namespace.io.openPayload = func(*os.Root, string) (*os.File, error) { t.Fatal("metadata scan opened payload"); return nil, nil }
	if _, err := s.List(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	stats, err := s.Stats(context.Background())
	if err != nil || stats.Objects != 2 || stats.Bytes != 8 {
		t.Fatalf("stats: %+v, %v", stats, err)
	}
}

func TestPageByteBudgetWithMaximumKeysMakesProgress(t *testing.T) {
	s := testStore(t)
	for i := range 4 {
		putText(t, s, fmt.Sprintf("%d", i)+strings.Repeat("<", 4095), "x", PutOptions{})
	}
	cursor := ""
	count := 0
	for range 5 {
		page, err := s.List(context.Background(), ListOptions{Limit: 100, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(page)
		if err != nil || len(encoded) > MaxPageBytes || len(page.Objects) == 0 {
			t.Fatalf("page budget/progress: bytes=%d, objects=%d, err=%v", len(encoded), len(page.Objects), err)
		}
		count += len(page.Objects)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if count != 4 || cursor != "" {
		t.Fatalf("pagination did not finish exactly once: %d, %q", count, cursor)
	}
}

func TestCandidateHeapRetainsOnlyBoundedSmallestEntries(t *testing.T) {
	h := &entryHeap{capacity: 4, present: make(map[string]bool)}
	for i := 10000; i >= 0; i-- {
		h.add(listEntry{key: fmt.Sprintf("%05d", i), kind: "prefix"})
		h.add(listEntry{key: "00000", kind: "prefix"})
	}
	if h.Len() != 4 || len(h.present) != 4 {
		t.Fatalf("unbounded candidates: %d/%d", h.Len(), len(h.present))
	}
	for _, entry := range h.entries {
		if entry.key > "00003" {
			t.Fatalf("retained wrong candidate %q", entry.key)
		}
	}
}

func TestCanceledListCannotReturnCompletePage(t *testing.T) {
	s := testStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.List(ctx, ListOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled list: %v", err)
	}
	if _, err := s.Stats(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled stats: %v", err)
	}
}
