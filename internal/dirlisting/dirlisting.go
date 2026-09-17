// Package dirlisting reuses directory membership between complete walks of one
// tree, such as the repeated scans of a development watcher.
//
// A listing records only the names and types of a directory's entries. It is
// reused only while the directory's device, inode, size, modification time and
// status-change time are those observed immediately before and after it was
// read, and only when both times were older than the timestamp granularity of
// common filesystems at that read. Restoring a directory's modification time
// changes its status-change time, so it cannot hide a membership change. An
// entry's Info always reads the entry's current metadata.
//
// A Tree belongs to one walker of one tree. A walk that completes evicts the
// listings of directories it did not visit, and a Tree holds at most a bounded
// number of listings. A fresh walk reads every directory and replaces the
// listings it finds, for reconciliation when an observation is uncertain.
package dirlisting

import (
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// stableAge exceeds the timestamp granularity of common filesystems.
	stableAge = 2 * time.Second
	// maxListings bounds the listings one Tree retains.
	maxListings = 1 << 16
	// maxTrees bounds the Trees the registry retains.
	maxTrees = 32
)

// Entry is a directory entry whose Info reads the entry's current metadata.
type Entry struct {
	dir, name string
	typ       fs.FileMode
}

func (e Entry) Name() string               { return e.name }
func (e Entry) IsDir() bool                { return e.typ.IsDir() }
func (e Entry) Type() fs.FileMode          { return e.typ }
func (e Entry) Info() (fs.FileInfo, error) { return os.Lstat(filepath.Join(e.dir, e.name)) }
func (e Entry) String() string             { return fs.FormatDirEntry(e) }

type stamp struct {
	device, inode    uint64
	size             int64
	modified, change int64
}

type member struct {
	name string
	typ  fs.FileMode
}

type listing struct {
	stamp   stamp
	members []member
	seen    uint64
}

// Tree holds the reusable listings of one walker of one tree.
type Tree struct {
	mu       sync.Mutex
	listings map[string]*listing
	epoch    uint64
	limit    int
}

// NewTree returns an empty Tree.
func NewTree() *Tree {
	return &Tree{listings: map[string]*listing{}, limit: maxListings}
}

// Walk is one walk of a Tree.
type Walk struct {
	tree  *Tree
	epoch uint64
	fresh bool
}

// Begin starts a walk. A fresh walk reads every directory instead of reusing a
// listing and records what it read.
func (t *Tree) Begin(fresh bool) *Walk {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.epoch++
	return &Walk{tree: t, epoch: t.epoch, fresh: fresh}
}

// Finish ends a walk that visited the whole tree: it evicts the listings of
// directories no walk begun since this one visited. A partial walk must not
// call it.
func (w *Walk) Finish() {
	t := w.tree
	t.mu.Lock()
	defer t.mu.Unlock()
	for path, listing := range t.listings {
		if listing.seen < w.epoch {
			delete(t.listings, path)
		}
	}
}

// Invalidate discards every listing, so the next walk reads every directory.
func (t *Tree) Invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.listings = map[string]*listing{}
}

// Len reports the number of retained listings.
func (t *Tree) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.listings)
}

// ReadDir returns the entries of the directory at path sorted by name, as
// os.ReadDir does, and whether a retained listing was reused. A walk must name
// one directory by one path spelling to benefit from reuse.
func (w *Walk) ReadDir(path string) ([]fs.DirEntry, bool, error) {
	t := w.tree
	before, err := os.Lstat(path)
	current, identified := stamp{}, false
	if err == nil && before.IsDir() {
		current, identified = stampOf(before)
	}
	if identified && !w.fresh {
		t.mu.Lock()
		if listing := t.listings[path]; listing != nil && listing.stamp == current {
			listing.seen = max(listing.seen, w.epoch)
			members := listing.members
			t.mu.Unlock()
			return entries(path, members), true, nil
		}
		t.mu.Unlock()
	}
	read, err := os.ReadDir(path)
	if err != nil {
		t.forget(path)
		return nil, false, err
	}
	members := make([]member, len(read))
	for index, entry := range read {
		members[index] = member{name: entry.Name(), typ: entry.Type()}
	}
	settled := false
	if identified {
		if after, statErr := os.Lstat(path); statErr == nil && after.IsDir() {
			confirmed, ok := stampOf(after)
			settled = ok && confirmed == current &&
				now().Sub(time.Unix(0, current.modified)) > stableAge && now().Sub(time.Unix(0, current.change)) > stableAge
		}
	}
	t.mu.Lock()
	if settled {
		if len(t.listings) >= t.limit {
			t.listings = map[string]*listing{}
		}
		t.listings[path] = &listing{stamp: current, members: members, seen: w.epoch}
	} else {
		delete(t.listings, path)
	}
	t.mu.Unlock()
	return entries(path, members), false, nil
}

func (t *Tree) forget(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.listings, path)
}

func entries(dir string, members []member) []fs.DirEntry {
	result := make([]fs.DirEntry, len(members))
	for index, member := range members {
		result[index] = Entry{dir: dir, name: member.name, typ: member.typ}
	}
	return result
}

// now is the clock that decides whether a directory had settled.
var now = time.Now

// SetClockForTesting replaces the clock that decides whether a directory had
// settled and returns the restore function.
func SetClockForTesting(clock func() time.Time) func() {
	previous := now
	now = clock
	return func() { now = previous }
}

var registry struct {
	sync.Mutex
	trees map[string]*Tree
	order []string
}

// TreeFor returns the Tree registered under name, creating it when absent. The
// registry retains the most recently requested Trees.
func TreeFor(name string) *Tree {
	registry.Lock()
	defer registry.Unlock()
	if registry.trees == nil {
		registry.trees = map[string]*Tree{}
	}
	tree := registry.trees[name]
	if tree == nil {
		tree = NewTree()
		registry.trees[name] = tree
	}
	for index, existing := range registry.order {
		if existing == name {
			registry.order = append(registry.order[:index], registry.order[index+1:]...)
			break
		}
	}
	registry.order = append(registry.order, name)
	for len(registry.order) > maxTrees {
		delete(registry.trees, registry.order[0])
		registry.order = registry.order[1:]
	}
	return tree
}

// InvalidateAll discards the listings of every registered Tree.
func InvalidateAll() {
	registry.Lock()
	trees := make([]*Tree, 0, len(registry.trees))
	for _, tree := range registry.trees {
		trees = append(trees, tree)
	}
	registry.Unlock()
	for _, tree := range trees {
		tree.Invalidate()
	}
}
