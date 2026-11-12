package build

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"scenery.sh/internal/devcache"
	"scenery.sh/internal/filemeta"
)

// developmentProcessShareLimit bounds the executables the machine's store
// keeps; the least recently used are removed when others are shared.
const developmentProcessShareLimit = 160

// sharedDevelopmentProcessRoot is the machine's store of linked development
// process executables, or empty when the development cache is unavailable.
// An executable is named by its process and linked identity: the package, the
// contract and implementation revisions, the digest of every consumed build
// input, the target, the build flags and the linker flags. Nothing in that
// identity names a workspace, so a worktree that needs an identity another
// worktree already linked adopts its executable instead of linking again.
var sharedDevelopmentProcessRoot = func() string {
	// A test's executables are fakes and must never reach the machine's
	// store; tests that exercise sharing name their own store.
	if testing.Testing() {
		return ""
	}
	root, err := devcache.Root()
	if err != nil || root == "" {
		return ""
	}
	return filepath.Join(root, "process-executables")
}

// adoptSharedDevelopmentProcesses gives every pending process whose identity
// the machine's store holds its executable and digest record, and returns the
// processes that still have to be linked. An adopted executable is proven the
// way a retained one is: its bytes must have the digest its link recorded.
func adoptSharedDevelopmentProcesses(ctx context.Context, pending []*DevelopmentProcess) []*DevelopmentProcess {
	shared := sharedDevelopmentProcessRoot()
	if shared == "" || len(pending) == 0 {
		return pending
	}
	started := time.Now()
	adopted := make([]bool, len(pending))
	var wg sync.WaitGroup
	for index, process := range pending {
		wg.Go(func() {
			source := filepath.Join(shared, filepath.Base(process.Binary))
			if copyDevelopmentProcessEntry(source, process.Binary) != nil {
				return
			}
			digest, ok, err := retainedDevelopmentProcessDigest(process.Binary)
			if err != nil || !ok {
				_ = os.Remove(process.Binary)
				_ = os.Remove(process.Binary + developmentProcessDigestSuffix)
				return
			}
			process.ArtifactDigest, adopted[index] = digest, true
			now := time.Now()
			_ = os.Chtimes(source, now, now)
		})
	}
	wg.Wait()
	var remaining []*DevelopmentProcess
	for index, process := range pending {
		if !adopted[index] {
			remaining = append(remaining, process)
		}
	}
	RecordStep(ctx, Step{Name: "process.adopt", StartedAt: started, Duration: time.Since(started), Cache: "shared_identity", Reason: "linked_in_another_workspace", OK: true, Actions: len(pending) - len(remaining), CacheMisses: len(remaining)})
	return remaining
}

// shareDevelopmentProcesses publishes newly linked executables to the
// machine's store. Sharing is a convenience: its failure changes nothing here.
func shareDevelopmentProcesses(linked []*DevelopmentProcess) {
	shared := sharedDevelopmentProcessRoot()
	if shared == "" || len(linked) == 0 || os.MkdirAll(shared, 0o700) != nil {
		return
	}
	var wg sync.WaitGroup
	for _, process := range linked {
		wg.Go(func() {
			target := filepath.Join(shared, filepath.Base(process.Binary))
			if _, err := os.Lstat(target); err == nil {
				return
			}
			_ = copyDevelopmentProcessEntry(process.Binary, target)
		})
	}
	wg.Wait()
	pruneSharedDevelopmentProcesses(shared)
}

// copyDevelopmentProcessEntry gives destination the executable at source and
// its digest record, the record last, so an entry without a record is never
// taken for a complete one. The executable is a clone where the filesystem
// clones files.
func copyDevelopmentProcessEntry(source, destination string) error {
	record, err := os.ReadFile(source + developmentProcessDigestSuffix)
	if err != nil {
		return err
	}
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("shared development process executable is not a regular file")
	}
	stage, err := os.CreateTemp(filepath.Dir(destination), ".adopt-*")
	if err != nil {
		return err
	}
	stagePath := stage.Name()
	_ = stage.Close()
	defer func() { _ = os.Remove(stagePath) }()
	if err := os.Remove(stagePath); err != nil {
		return err
	}
	if filemeta.Clone(source, stagePath) != nil {
		_ = os.Remove(stagePath)
		if err := copyRegularFile(source, stagePath, info.Mode().Perm()); err != nil {
			return err
		}
	}
	if cloned, err := os.Lstat(stagePath); err != nil || !cloned.Mode().IsRegular() {
		return errors.New("shared development process executable did not copy as a regular file")
	}
	if err := os.Rename(stagePath, destination); err != nil {
		return err
	}
	return os.WriteFile(destination+developmentProcessDigestSuffix, record, 0o600)
}

func copyRegularFile(source, destination string, mode os.FileMode) error {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

func pruneSharedDevelopmentProcesses(shared string) {
	entries, err := os.ReadDir(shared)
	if err != nil {
		return
	}
	type entry struct {
		name string
		used time.Time
	}
	var executables []entry
	for _, item := range entries {
		name := item.Name()
		if strings.HasSuffix(name, developmentProcessDigestSuffix) || strings.HasPrefix(name, ".") {
			continue
		}
		if info, err := item.Info(); err == nil && info.Mode().IsRegular() {
			executables = append(executables, entry{name, info.ModTime()})
		}
	}
	if len(executables) <= developmentProcessShareLimit {
		return
	}
	sort.Slice(executables, func(i, j int) bool { return executables[i].used.After(executables[j].used) })
	for _, stale := range executables[developmentProcessShareLimit:] {
		// The record goes first: an executable without one is never adopted.
		_ = os.Remove(filepath.Join(shared, stale.name+developmentProcessDigestSuffix))
		_ = os.Remove(filepath.Join(shared, stale.name))
	}
}
