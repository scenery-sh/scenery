package storagefs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOrderedSorterVisitsEveryReferenceOnceInOrder(t *testing.T) {
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	sorter := newOrderedSorter[reference](root, "staging", func(a, b reference) bool {
		return a.Object.Key < b.Object.Key
	})
	const total = orderedRunItems + 17
	for i := total - 1; i >= 0; i-- {
		if err := sorter.Add(context.Background(), reference{Object: Object{Key: fmt.Sprintf("key-%03d", i)}}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := sorter.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatal("sorter returned no run")
	}
	defer func() { _ = run.Remove() }()

	visited := 0
	if err := run.Visit(context.Background(), func(ref reference) error {
		want := fmt.Sprintf("key-%03d", visited)
		if ref.Object.Key != want {
			t.Fatalf("visit %d = %q, want %q", visited, ref.Object.Key, want)
		}
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited != total {
		t.Fatalf("visited %d references, want %d", visited, total)
	}
	if err := run.Remove(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ordered runs leaked: %v", entries)
	}
}

func TestOrderedSorterMergesMoreThanItsRunFan(t *testing.T) {
	path := t.TempDir()
	if err := os.Mkdir(filepath.Join(path, "staging"), 0o700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	sorter := newOrderedSorter[reference](root, "staging", func(a, b reference) bool {
		return a.Object.Key < b.Object.Key
	})
	for i := orderedMergeFan; i >= 0; i-- {
		name, file, err := createOrderedRun(root, "staging")
		if err != nil {
			t.Fatal(err)
		}
		if err := json.NewEncoder(file).Encode(reference{Object: Object{Key: fmt.Sprintf("key-%03d", i)}}); err != nil {
			t.Fatal(err)
		}
		if err := file.Close(); err != nil {
			t.Fatal(err)
		}
		sorter.runs = append(sorter.runs, name)
	}
	run, err := sorter.Finish(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if run == nil {
		t.Fatal("sorter returned no run")
	}
	defer func() { _ = run.Remove() }()
	visited := 0
	if err := run.Visit(context.Background(), func(ref reference) error {
		want := fmt.Sprintf("key-%03d", visited)
		if ref.Object.Key != want {
			t.Fatalf("visit %d = %q, want %q", visited, ref.Object.Key, want)
		}
		visited++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if visited != orderedMergeFan+1 {
		t.Fatalf("visited %d references, want %d", visited, orderedMergeFan+1)
	}
	if err := run.Remove(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(path, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("ordered runs leaked: %v", entries)
	}
}
