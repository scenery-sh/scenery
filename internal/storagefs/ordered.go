package storagefs

import (
	"container/heap"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// A run is deliberately small enough that a large object descriptor cannot
	// turn an ordered scan into an unbounded memory allocation.
	orderedRunItems = 256
	orderedMergeFan = 64
)

// orderedSorter writes bounded sorted runs into the generation staging area
// and merges them without retaining the complete namespace in memory. The
// files are disposable: a crashed operation leaves valid staging material for
// the normal reclamation path to remove.
type orderedSorter[T any] struct {
	root    *os.Root
	staging string
	less    func(T, T) bool
	runs    []string
	items   []T
	create  orderedRunCreator
}

type orderedRunCreator func(*os.Root, string) (string, io.WriteCloser, error)

func newOrderedSorter[T any](root *os.Root, staging string, less func(T, T) bool) *orderedSorter[T] {
	return &orderedSorter[T]{root: root, staging: staging, less: less, items: make([]T, 0, orderedRunItems), create: createOrderedRun}
}

func (s *orderedSorter[T]) Add(ctx context.Context, value T) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.items = append(s.items, value)
	if len(s.items) < orderedRunItems {
		return nil
	}
	return s.flush(ctx)
}

func (s *orderedSorter[T]) Finish(ctx context.Context) (*orderedRun[T], error) {
	if err := ctx.Err(); err != nil {
		return nil, s.fail(err)
	}
	if len(s.items) > 0 {
		if err := s.flush(ctx); err != nil {
			return nil, s.fail(err)
		}
	}
	for len(s.runs) > orderedMergeFan {
		next := make([]string, 0, (len(s.runs)+orderedMergeFan-1)/orderedMergeFan)
		for start := 0; start < len(s.runs); start += orderedMergeFan {
			end := start + orderedMergeFan
			if end > len(s.runs) {
				end = len(s.runs)
			}
			if end-start == 1 {
				next = append(next, s.runs[start])
				continue
			}
			merged, err := mergeOrderedRuns(ctx, s.root, s.staging, s.runs[start:end], s.less, s.create)
			if err != nil {
				return nil, s.fail(err, next...)
			}
			if err := removeOrderedRuns(s.root, s.runs[start:end]...); err != nil {
				_ = s.root.Remove(merged)
				return nil, s.fail(err, next...)
			}
			next = append(next, merged)
		}
		s.runs = next
	}
	if len(s.runs) > 1 {
		merged, err := mergeOrderedRuns(ctx, s.root, s.staging, s.runs, s.less, s.create)
		if err != nil {
			return nil, s.fail(err)
		}
		if err := removeOrderedRuns(s.root, s.runs...); err != nil {
			_ = s.root.Remove(merged)
			return nil, s.fail(err)
		}
		s.runs = []string{merged}
	}
	if len(s.runs) == 0 {
		return nil, nil
	}
	run := &orderedRun[T]{root: s.root, path: s.runs[0]}
	s.runs = nil
	return run, nil
}

func (s *orderedSorter[T]) flush(ctx context.Context) error {
	if len(s.items) == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	sort.Slice(s.items, func(i, j int) bool { return s.less(s.items[i], s.items[j]) })
	name, file, err := s.create(s.root, s.staging)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		if !keep {
			_ = s.root.Remove(name)
		}
	}()
	encoder := json.NewEncoder(file)
	for _, item := range s.items {
		if err := ctx.Err(); err != nil {
			_ = file.Close()
			return err
		}
		if err := encoder.Encode(item); err != nil {
			_ = file.Close()
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	s.runs = append(s.runs, name)
	s.items = s.items[:0]
	keep = true
	return nil
}

func (s *orderedSorter[T]) fail(err error, extra ...string) error {
	names := append(append([]string{}, s.runs...), extra...)
	return errors.Join(err, removeOrderedRuns(s.root, names...))
}

type orderedRun[T any] struct {
	root *os.Root
	path string
	once sync.Once
	err  error
}

func (r *orderedRun[T]) Visit(ctx context.Context, visit func(T) error) error {
	if r == nil {
		return nil
	}
	file, err := r.root.Open(r.path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(file)
	for {
		if err := ctx.Err(); err != nil {
			return errors.Join(err, file.Close())
		}
		var value T
		err := decoder.Decode(&value)
		if errors.Is(err, io.EOF) {
			return file.Close()
		}
		if err != nil {
			return errors.Join(fmt.Errorf("decode ordered storage run: %w", err), file.Close())
		}
		if err := visit(value); err != nil {
			return errors.Join(err, file.Close())
		}
	}
}

func (r *orderedRun[T]) Remove() error {
	if r == nil {
		return nil
	}
	r.once.Do(func() { r.err = r.root.Remove(r.path) })
	return r.err
}

func createOrderedRun(root *os.Root, staging string) (string, io.WriteCloser, error) {
	for range 4 {
		id, err := randomID()
		if err != nil {
			return "", nil, err
		}
		name := filepath.Join(staging, ".ordered-"+id)
		file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return "", nil, err
		}
		return name, file, nil
	}
	return "", nil, fmt.Errorf("unable to allocate a unique ordered storage run")
}

func isOrderedRun(name string) bool {
	id, ok := strings.CutPrefix(name, ".ordered-")
	return ok && isHexID(id, 16)
}

func removeStaleOrderedRuns(ctx context.Context, root *os.Root, staging string) error {
	return scanDirectory(ctx, root, staging, func(info os.FileInfo) error {
		if !isOrderedRun(info.Name()) {
			return nil
		}
		if err := checkOwned(info, false); err != nil {
			return err
		}
		return root.Remove(filepath.Join(staging, info.Name()))
	})
}

func removeOrderedRuns(root *os.Root, names ...string) error {
	var joined error
	for _, name := range names {
		if err := root.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

type orderedRunSource[T any] struct {
	file    *os.File
	decoder *json.Decoder
}

type orderedRunItem[T any] struct {
	value  T
	source int
}

type orderedRunHeap[T any] struct {
	items   []orderedRunItem[T]
	sources []orderedRunSource[T]
	less    func(T, T) bool
}

func (h orderedRunHeap[T]) Len() int { return len(h.items) }
func (h orderedRunHeap[T]) Less(i, j int) bool {
	if h.less(h.items[i].value, h.items[j].value) {
		return true
	}
	if h.less(h.items[j].value, h.items[i].value) {
		return false
	}
	return h.items[i].source < h.items[j].source
}
func (h orderedRunHeap[T]) Swap(i, j int)   { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *orderedRunHeap[T]) Push(value any) { h.items = append(h.items, value.(orderedRunItem[T])) }
func (h *orderedRunHeap[T]) Pop() any {
	last := h.items[len(h.items)-1]
	h.items = h.items[:len(h.items)-1]
	return last
}

func mergeOrderedRuns[T any](ctx context.Context, root *os.Root, staging string, names []string, less func(T, T) bool, create orderedRunCreator) (string, error) {
	name, output, err := create(root, staging)
	if err != nil {
		return "", err
	}
	keep := false
	defer func() {
		if !keep {
			_ = root.Remove(name)
		}
	}()
	sources := make([]orderedRunSource[T], 0, len(names))
	closeSources := func() error {
		var joined error
		for _, source := range sources {
			joined = errors.Join(joined, source.file.Close())
		}
		return joined
	}
	heapValue := &orderedRunHeap[T]{less: less}
	for _, runName := range names {
		file, err := root.Open(runName)
		if err != nil {
			_ = output.Close()
			return "", errors.Join(err, closeSources())
		}
		source := orderedRunSource[T]{file: file, decoder: json.NewDecoder(file)}
		sources = append(sources, source)
		value, ok, err := decodeOrderedRunValue[T](source.decoder)
		if err != nil {
			_ = output.Close()
			return "", errors.Join(err, closeSources())
		}
		if ok {
			heapValue.sources = sources
			heap.Push(heapValue, orderedRunItem[T]{value: value, source: len(sources) - 1})
		}
	}
	heapValue.sources = sources
	encoder := json.NewEncoder(output)
	for heapValue.Len() > 0 {
		if err := ctx.Err(); err != nil {
			_ = output.Close()
			return "", errors.Join(err, closeSources())
		}
		item := heap.Pop(heapValue).(orderedRunItem[T])
		if err := encoder.Encode(item.value); err != nil {
			_ = output.Close()
			return "", errors.Join(err, closeSources())
		}
		next, ok, err := decodeOrderedRunValue[T](heapValue.sources[item.source].decoder)
		if err != nil {
			_ = output.Close()
			return "", errors.Join(err, closeSources())
		}
		if ok {
			heap.Push(heapValue, orderedRunItem[T]{value: next, source: item.source})
		}
	}
	outputErr := output.Close()
	closeErr := closeSources()
	if err := errors.Join(outputErr, closeErr); err != nil {
		return "", err
	}
	keep = true
	return name, nil
}

func decodeOrderedRunValue[T any](decoder *json.Decoder) (T, bool, error) {
	var value T
	err := decoder.Decode(&value)
	if errors.Is(err, io.EOF) {
		return value, false, nil
	}
	return value, true, err
}
