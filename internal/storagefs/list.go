package storagefs

import (
	"bytes"
	"container/heap"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

const (
	DefaultListLimit = 100
	MaxListLimit     = 1000
	MaxPageBytes     = 64 << 10
)

type ListOptions struct {
	Prefix, Delimiter, Cursor string
	Limit                     int
}
type ListPage struct {
	Objects    []Object `json:"objects"`
	Prefixes   []string `json:"prefixes,omitempty"`
	NextCursor string   `json:"next_cursor,omitempty"`
}
type Stats struct {
	Objects int64 `json:"objects"`
	Bytes   int64 `json:"bytes"`
}

type listCursor struct {
	Binding string `json:"binding"`
	Last    string `json:"last"`
	Type    string `json:"type"`
}
type listEntry struct {
	key, kind string
	object    Object
}

func NormalizeListOptions(opts ListOptions) (ListOptions, error) {
	if err := ValidatePrefix(opts.Prefix); err != nil {
		return ListOptions{}, err
	}
	if opts.Delimiter != "" && opts.Delimiter != "/" {
		return ListOptions{}, fmt.Errorf("%w: delimiter must be /", ErrInvalid)
	}
	if opts.Limit <= 0 {
		opts.Limit = DefaultListLimit
	}
	if opts.Limit > MaxListLimit {
		opts.Limit = MaxListLimit
	}
	return opts, nil
}

// List scans all selected-partition references, retaining at most limit+1
// candidates. It is a bounded-memory metadata scan, not an indexed seek.
func (s *Store) List(ctx context.Context, opts ListOptions) (*ListPage, error) {
	opts, err := NormalizeListOptions(opts)
	if err != nil {
		return nil, err
	}
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", false)
	if err != nil {
		return nil, err
	}
	defer func() { _ = mutation.Close() }()
	binding := token("list-cursor", lease.owner.Incarnation, lease.owner.Generation, s.scope.Store, s.scope.Tenant, opts.Prefix, opts.Delimiter)
	last, err := decodeCursor(opts.Cursor, binding)
	if err != nil {
		return nil, err
	}
	candidates := &entryHeap{present: make(map[string]bool), capacity: opts.Limit + 1}
	err = scanReferences(ctx, lease, s.scope, func(ref reference) error {
		if !strings.HasPrefix(ref.Object.Key, opts.Prefix) {
			return nil
		}
		entry := listEntry{key: ref.Object.Key, kind: "object", object: ref.Object}
		entry.object.Metadata = nil
		if opts.Delimiter != "" {
			if index := strings.Index(strings.TrimPrefix(entry.key, opts.Prefix), "/"); index >= 0 {
				entry.key = entry.key[:len(opts.Prefix)+index+1]
				entry.kind = "prefix"
				entry.object = Object{}
			}
		}
		if opts.Cursor != "" && compareEntry(entry, last) <= 0 {
			return nil
		}
		candidates.add(entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	entries := candidates.entries
	sort.Slice(entries, func(i, j int) bool { return compareEntry(entries[i], entries[j]) < 0 })
	page := &ListPage{Objects: []Object{}}
	for i, entry := range entries {
		if i == opts.Limit {
			break
		}
		if entry.kind == "prefix" {
			page.Prefixes = append(page.Prefixes, entry.key)
		} else {
			page.Objects = append(page.Objects, entry.object)
		}
		page.NextCursor = ""
		if i+1 < len(entries) {
			page.NextCursor = encodeCursor(binding, entry)
		}
		data, err := json.Marshal(page)
		if err != nil {
			return nil, err
		}
		if len(data) <= MaxPageBytes {
			continue
		}
		if entry.kind == "prefix" {
			page.Prefixes = page.Prefixes[:len(page.Prefixes)-1]
		} else {
			page.Objects = page.Objects[:len(page.Objects)-1]
		}
		if i == 0 {
			return nil, fmt.Errorf("%w: one object descriptor cannot fit the page byte budget", ErrInvalid)
		}
		page.NextCursor = encodeCursor(binding, entries[i-1])
		break
	}
	return page, nil
}

func (s *Store) Stats(ctx context.Context) (Stats, error) {
	lease, err := s.namespace.acquire(ctx, false)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = lease.Close() }()
	mutation, err := lockFile(ctx, lease.root, "mutation.lock", false)
	if err != nil {
		return Stats{}, err
	}
	defer func() { _ = mutation.Close() }()
	var stats Stats
	err = scanReferences(ctx, lease, s.scope, func(ref reference) error {
		if stats.Objects == math.MaxInt64 || ref.Object.SizeBytes > math.MaxInt64-stats.Bytes {
			return fmt.Errorf("%w: metadata totals overflow", ErrCorrupt)
		}
		stats.Objects++
		stats.Bytes += ref.Object.SizeBytes
		return nil
	})
	if err != nil {
		return Stats{}, err
	}
	return stats, nil
}

func encodeCursor(binding string, last listEntry) string {
	// Encoding the key separately avoids JSON HTML escaping expanding a valid
	// 4096-byte key sixfold inside the outer opaque cursor.
	data, _ := json.Marshal(listCursor{Binding: binding, Last: base64.RawURLEncoding.EncodeToString([]byte(last.key)), Type: last.kind})
	return base64.RawURLEncoding.EncodeToString(data)
}

func decodeCursor(value, binding string) (listEntry, error) {
	if value == "" {
		return listEntry{}, nil
	}
	invalid := fmt.Errorf("%w: malformed or differently scoped storage cursor", ErrInvalid)
	if len(value) > 12<<10 {
		return listEntry{}, invalid
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return listEntry{}, invalid
	}
	var cursor listCursor
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cursor); err != nil {
		return listEntry{}, invalid
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return listEntry{}, invalid
	}
	if cursor.Binding != binding || (cursor.Type != "object" && cursor.Type != "prefix") {
		return listEntry{}, invalid
	}
	key, err := base64.RawURLEncoding.Strict().DecodeString(cursor.Last)
	if err != nil {
		return listEntry{}, invalid
	}
	if cursor.Type == "object" {
		err = ValidateKey(string(key))
	} else {
		err = ValidatePrefix(string(key))
		if !strings.HasSuffix(string(key), "/") {
			return listEntry{}, invalid
		}
	}
	if err != nil {
		return listEntry{}, invalid
	}
	return listEntry{key: string(key), kind: cursor.Type}, nil
}

func compareEntry(a, b listEntry) int {
	if order := strings.Compare(a.key, b.key); order != 0 {
		return order
	}
	return strings.Compare(a.kind, b.kind)
}

type entryHeap struct {
	entries  []listEntry
	present  map[string]bool
	capacity int
}

func (h entryHeap) Len() int           { return len(h.entries) }
func (h entryHeap) Less(i, j int) bool { return compareEntry(h.entries[i], h.entries[j]) > 0 }
func (h entryHeap) Swap(i, j int)      { h.entries[i], h.entries[j] = h.entries[j], h.entries[i] }
func (h *entryHeap) Push(value any) {
	entry := value.(listEntry)
	h.entries = append(h.entries, entry)
	h.present[entry.kind+":"+entry.key] = true
}
func (h *entryHeap) Pop() any {
	last := h.entries[len(h.entries)-1]
	h.entries = h.entries[:len(h.entries)-1]
	delete(h.present, last.kind+":"+last.key)
	return last
}
func (h *entryHeap) add(entry listEntry) {
	if h.present[entry.kind+":"+entry.key] {
		return
	}
	if len(h.entries) == h.capacity {
		if compareEntry(entry, h.entries[0]) >= 0 {
			return
		}
		heap.Pop(h)
	}
	heap.Push(h, entry)
}
