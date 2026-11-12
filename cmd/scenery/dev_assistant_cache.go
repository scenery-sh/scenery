package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"scenery.sh/internal/assistantadapter/eve"
	"scenery.sh/internal/runtimeassets"
	"scenery.sh/internal/toolchain"
)

// Prepared caches are never runtime working directories. Every hit hashes the
// copied bytes against the retained descriptor before starting a private copy.
// The input includes the complete materialized authored/generated tree, the
// verified managed toolchain manifest/platform and exact Node executable bytes.
type assistantOverlayCache struct {
	path   string
	key    string
	onCopy func(assistantCopyStats, error)
}

// A cache record identifies its inputs and the verified tree. The tree is
// canonical: it records no directory of its own (see eve.CanonicalRoot), so a
// restore is a verified copy that rewrites nothing.
type assistantOverlayCacheRecord struct {
	Key  string                   `json:"key"`
	Tree runtimeassets.Descriptor `json:"tree"`
}

func openAssistantOverlayCache(root, overlay, node string) (*assistantOverlayCache, error) {
	inputs, err := assistantOverlayInputDigest(overlay)
	if err != nil {
		return nil, err
	}
	nodeDigest, err := eve.PackageLockDigest(node)
	if err != nil {
		return nil, err
	}
	key := strings.TrimPrefix(digestBytes([]byte(strings.Join([]string{
		"assistant-prepared-v2", inputs, nodeDigest,
		toolchain.BundledManifestSHA256(), toolchain.CurrentPlatform().String(),
	}, "\n"))), "sha256:")
	base := filepath.Join(root, ".scenery", "assistant-cache", "prepared")
	if err := rejectExistingAssistantPathSymlinks(root, base); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(base, 0o700); err != nil {
		return nil, err
	}
	return &assistantOverlayCache{path: filepath.Join(base, key), key: key}, nil
}

func assistantOverlayInputDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == "." {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.IsDir() && !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported assistant input %q", relative)
		}
		digest := ""
		if info.Mode().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			digest = digestBytes(data)
		}
		_, _ = fmt.Fprintf(hash, "%q %o %s\n", filepath.ToSlash(relative), info.Mode(), digest)
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func (c *assistantOverlayCache) restore(ctx context.Context, overlay string) (bool, error) {
	info, err := os.Lstat(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("prepared cache is not a directory")
	}
	recordPath := filepath.Join(c.path, "record.json")
	info, err = os.Lstat(recordPath)
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return false, errors.New("prepared cache record is not an owner-only regular file")
	}
	data, err := os.ReadFile(recordPath)
	if err != nil {
		return false, err
	}
	var record assistantOverlayCacheRecord
	if err := rejectDuplicateJSONObjects(data); err != nil {
		return false, err
	}
	if err := decodeJSONExact(data, &record); err != nil {
		return false, err
	}
	if record.Key != c.key {
		return false, errors.New("prepared cache input identity mismatch")
	}
	if err := record.Tree.Validate(); err != nil {
		return false, err
	}
	stats := assistantCopyStats{Started: time.Now()}
	err = restoreAssistantPreparedTree(ctx, filepath.Join(c.path, "tree"), overlay, &record.Tree, &stats)
	stats.Duration = time.Since(stats.Started)
	if c.onCopy != nil {
		c.onCopy(stats, err)
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (c *assistantOverlayCache) publish(ctx context.Context, overlay string) error {
	stage, err := os.MkdirTemp(filepath.Dir(c.path), ".stage-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(stage) }()
	tree := filepath.Join(stage, "tree")
	if err := os.Mkdir(tree, 0o755); err != nil {
		return err
	}
	if err := copyAssistantPreparedTree(ctx, overlay, tree, nil); err != nil {
		return err
	}
	// The build recorded the overlay it was built in. The reusable copy records
	// the canonical root instead, as a production capsule does, and only a build
	// whose Scenery connection is resolved at runtime is kept.
	if err := eve.CanonicalizeBuild(tree, overlay); err != nil {
		return err
	}
	descriptor, err := runtimeassets.DescribeTree(tree)
	if err != nil {
		return err
	}
	record := assistantOverlayCacheRecord{Key: c.key, Tree: descriptor}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(stage, "record.json"), data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(stage, c.path); err != nil {
		// Another owner may have published the same exact inputs. Never overwrite
		// its output; the next hit still verifies its retained bytes independently.
		if info, statErr := os.Lstat(c.path); statErr == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return nil
		}
		return err
	}
	return nil
}

// Only dependency/build output is cached. Provider state, HOME, and ephemeral
// compilation metadata remain private, as in the existing production capsule.
func assistantPreparedPath(path string) bool {
	if path != "node_modules" && !strings.HasPrefix(path, "node_modules/") && path != ".output" && !strings.HasPrefix(path, ".output/") {
		return false
	}
	for _, excluded := range []string{".output/.eve", "node_modules/.cache", "node_modules/.nitro", "node_modules/.package-lock.json"} {
		if path == excluded || strings.HasPrefix(path, excluded+"/") {
			return false
		}
	}
	return true
}

// assistantVerifyWorkers bounds the files of one cloned tree that are read
// and hashed at once.
const assistantVerifyWorkers = 6

// assistantPreparedEntry describes one path of a prepared tree as its
// descriptor would, without the content of a file: a relative symlink that
// stays inside destination, a directory with mode 0755, or a regular file with
// mode 0644 or 0755.
func assistantPreparedEntry(path, relative, destination string, info fs.FileInfo) (runtimeassets.Entry, error) {
	actual := runtimeassets.Entry{Path: relative}
	switch {
	case info.Mode()&os.ModeSymlink != 0:
		link, err := os.Readlink(path)
		if err != nil {
			return actual, err
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		if filepath.IsAbs(link) || !assistantPathWithin(destination, filepath.Join(filepath.Dir(target), link)) {
			return actual, errors.New("prepared cache symlink escapes tree")
		}
		actual.Kind, actual.Target = runtimeassets.EntrySymlink, link
	case info.IsDir():
		if info.Mode().Perm() != 0o755 {
			return actual, fmt.Errorf("unsupported prepared directory mode: %s", relative)
		}
		actual.Kind, actual.Mode = runtimeassets.EntryDirectory, 0o755
	case info.Mode().IsRegular():
		actual.Kind, actual.Mode = runtimeassets.EntryFile, uint32(info.Mode().Perm())
		if actual.Mode != 0o644 && actual.Mode != 0o755 {
			return actual, fmt.Errorf("unsupported prepared file mode: %s", relative)
		}
	default:
		return actual, fmt.Errorf("unsupported prepared cache path: %s", relative)
	}
	return actual, nil
}

// restoreAssistantPreparedTree gives destination an independent, verified
// copy of the prepared tree at source. Where the filesystem can clone a
// directory, each top-level entry is cloned and the clone is then read and
// verified against expected, which costs no file creation; anything that
// prevents that, including a clone that does not verify, removes what was
// cloned and copies file by file instead.
func restoreAssistantPreparedTree(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor, stats *assistantCopyStats) error {
	cloned, err := cloneAssistantPreparedTree(ctx, source, destination, expected, stats)
	if err == nil {
		return nil
	}
	for _, name := range cloned {
		_ = os.RemoveAll(filepath.Join(destination, name))
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	*stats = assistantCopyStats{Started: stats.Started}
	return copyAssistantPreparedTreeMeasured(ctx, source, destination, expected, stats)
}

// cloneAssistantPreparedTree clones the top-level entries of source into
// destination and verifies every cloned path and byte against expected. It
// returns the names it created, whether or not it succeeded.
func cloneAssistantPreparedTree(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor, stats *assistantCopyStats) ([]string, error) {
	entries := make(map[string]runtimeassets.Entry, len(expected.Entries))
	for _, entry := range expected.Entries {
		entries[entry.Path] = entry
	}
	top, err := os.ReadDir(source)
	if err != nil {
		return nil, err
	}
	var cloned []string
	started := time.Now()
	for _, entry := range top {
		name := entry.Name()
		if !assistantPreparedPath(name) {
			return cloned, fmt.Errorf("unexpected prepared cache path %q", name)
		}
		target := filepath.Join(destination, name)
		if _, err := os.Lstat(target); err == nil {
			return cloned, fmt.Errorf("prepared path already exists: %s", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return cloned, err
		}
		if err := cloneAssistantTreeEntry(filepath.Join(source, name), target); err != nil {
			// A failed clone may leave part of the entry behind.
			return append(cloned, name), err
		}
		cloned = append(cloned, name)
	}
	stats.Write += time.Since(started)
	// Every cloned path is classified in walk order; the files are read and
	// hashed by workers, which only read, and compared with what the
	// descriptor expects of them.
	type verification struct {
		path string
		want runtimeassets.Entry
	}
	jobs := make(chan verification)
	failed := make(chan struct{})
	var measured sync.Mutex
	var firstFailure error
	fail := func(err error) {
		measured.Lock()
		defer measured.Unlock()
		if firstFailure == nil {
			firstFailure = err
			close(failed)
		}
	}
	failure := func() error {
		measured.Lock()
		defer measured.Unlock()
		return firstFailure
	}
	var workers sync.WaitGroup
	for range min(assistantVerifyWorkers, runtime.GOMAXPROCS(0)) {
		workers.Go(func() {
			for job := range jobs {
				if failure() != nil {
					continue
				}
				readStarted := time.Now()
				data, err := os.ReadFile(job.path)
				read := time.Since(readStarted)
				if err != nil {
					fail(err)
					continue
				}
				hashStarted := time.Now()
				size, digest := int64(len(data)), digestBytes(data)
				hash := time.Since(hashStarted)
				if size != job.want.Size || digest != job.want.Digest {
					fail(fmt.Errorf("prepared cache content mismatch: %s", job.want.Path))
					continue
				}
				measured.Lock()
				stats.Read, stats.Hash = stats.Read+read, stats.Hash+hash
				stats.Files++
				stats.Bytes += size
				measured.Unlock()
			}
		})
	}
	var walkErr error
	for _, name := range cloned {
		walkErr = filepath.WalkDir(filepath.Join(destination, name), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := failure(); err != nil {
				return err
			}
			relative, err := filepath.Rel(destination, path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if !assistantPreparedPath(relative) {
				return fmt.Errorf("unexpected prepared cache path %q", relative)
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			actual, err := assistantPreparedEntry(path, relative, destination, info)
			if err != nil {
				return err
			}
			want, ok := entries[relative]
			// Size and digest of a file are compared by the worker that reads it.
			comparable := want
			comparable.Size, comparable.Digest = 0, ""
			if !ok || comparable != actual {
				return fmt.Errorf("prepared cache content mismatch: %s", relative)
			}
			delete(entries, relative)
			stats.Entries++
			if actual.Kind != runtimeassets.EntryFile {
				return nil
			}
			select {
			case jobs <- verification{path: path, want: want}:
				return nil
			case <-failed:
				return failure()
			}
		})
		if walkErr != nil {
			break
		}
	}
	close(jobs)
	workers.Wait()
	if walkErr != nil {
		return cloned, walkErr
	}
	if err := failure(); err != nil {
		return cloned, err
	}
	if len(entries) != 0 {
		return cloned, errors.New("prepared cache files are missing")
	}
	return cloned, nil
}

func copyAssistantPreparedTree(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor) error {
	return copyAssistantPreparedTreeMeasured(ctx, source, destination, expected, nil)
}

func copyAssistantPreparedTreeMeasured(ctx context.Context, source, destination string, expected *runtimeassets.Descriptor, stats *assistantCopyStats) error {
	entries := map[string]runtimeassets.Entry{}
	if expected != nil {
		for _, entry := range expected.Entries {
			entries[entry.Path] = entry
		}
	}
	err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil || relative == "." {
			return err
		}
		relative = filepath.ToSlash(relative)
		if !assistantPreparedPath(relative) {
			if expected != nil {
				return fmt.Errorf("unexpected prepared cache path %q", relative)
			}
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		target := filepath.Join(destination, filepath.FromSlash(relative))
		actual, err := assistantPreparedEntry(path, relative, destination, info)
		if err != nil {
			return err
		}
		var data []byte
		if actual.Kind == runtimeassets.EntryFile {
			started := time.Now()
			data, err = os.ReadFile(path)
			if stats != nil {
				stats.Read += time.Since(started)
			}
			if err != nil {
				return err
			}
			started = time.Now()
			actual.Size, actual.Digest = int64(len(data)), digestBytes(data)
			if stats != nil {
				stats.Hash += time.Since(started)
				stats.Files++
				stats.Bytes += actual.Size
			}
		}
		if expected != nil {
			if want, ok := entries[relative]; !ok || want != actual {
				return fmt.Errorf("prepared cache content mismatch: %s", relative)
			}
			delete(entries, relative)
		}
		if stats != nil {
			stats.Entries++
		}
		started := time.Now()
		switch actual.Kind {
		case runtimeassets.EntryDirectory:
			err = os.Mkdir(target, 0o755)
		case runtimeassets.EntrySymlink:
			err = os.Symlink(actual.Target, target)
		default:
			err = os.WriteFile(target, data, fs.FileMode(actual.Mode))
		}
		if stats != nil {
			stats.Write += time.Since(started)
		}
		return err
	})
	if err != nil {
		return err
	}
	if len(entries) != 0 {
		return errors.New("prepared cache files are missing")
	}
	return nil
}
