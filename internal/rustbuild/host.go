package rustbuild

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"time"

	"scenery.sh/internal/devcache"
	"scenery.sh/internal/devprocess"
	sdksource "scenery.sh/sdk/rust"
)

// HostToolchain is the Rust toolchain the Host is built with.
const HostToolchain = "1.99.1"

var hostSourceDigest = sync.OnceValues(func() (string, error) {
	hash := sha256.New()
	_, _ = fmt.Fprintln(hash, HostToolchain, runtime.GOOS, runtime.GOARCH)
	err := fs.WalkDir(sdksource.Host, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := sdksource.Host.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%x\n", path, sha256.Sum256(data))
		return nil
	})
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
})

// Host builds the framework-owned worker Host only on cache miss.
func Host(ctx context.Context) (string, error) {
	cache, err := devcache.Root()
	if err != nil {
		return "", err
	}
	cache, err = filepath.Abs(cache)
	if err != nil {
		return "", err
	}
	cache = filepath.Join(cache, "worker-host")
	digest, err := hostSourceDigest()
	if err != nil {
		return "", err
	}
	root := filepath.Join(cache, digest)
	executable := filepath.Join(root, "scenery-kernel")
	if info, err := os.Stat(executable); err == nil && info.Mode().IsRegular() && info.Mode()&0o100 != 0 {
		return executable, nil
	}
	unlock, err := devprocess.LockSubstrate(cache, "worker-host", devprocess.LockOptions{})
	if err != nil {
		return "", err
	}
	defer unlock()
	if info, err := os.Stat(executable); err == nil && info.Mode().IsRegular() && info.Mode()&0o100 != 0 {
		return executable, nil
	}
	sources := filepath.Join(root, "sources")
	if err := materializeHostSources(sources); err != nil {
		return "", err
	}
	target := filepath.Join(cache, "target")
	command := exec.CommandContext(ctx, "cargo", "+"+HostToolchain, "build", "--locked", "--release",
		"--manifest-path", filepath.Join(sources, "scenery-kernel", "Cargo.toml"), "--target-dir", target)
	command.Dir = filepath.Join(sources, "scenery-kernel")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build worker Host: %w\n%s", err, output)
	}
	binary, err := os.Open(filepath.Join(target, "release", "scenery-kernel"))
	if err != nil {
		return "", err
	}
	defer func() { _ = binary.Close() }()
	staged, err := os.CreateTemp(root, ".worker-host-*")
	if err != nil {
		return "", err
	}
	defer func() { _ = os.Remove(staged.Name()) }()
	_, err = io.Copy(staged, binary)
	if err == nil {
		err = staged.Chmod(0o700)
	}
	if closeErr := staged.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", err
	}
	if err := os.Rename(staged.Name(), executable); err != nil {
		return "", err
	}
	pruneOldHostDigests(cache, digest, 3)
	return executable, nil
}

// pruneOldHostDigests keeps the newest keepCount worker-host digest directories
// (always retaining activeDigest and the shared cargo target directory) so
// repeated edits to scenery-kernel do not accumulate unbounded host snapshots.
func pruneOldHostDigests(cache, activeDigest string, keepCount int) {
	entries, err := os.ReadDir(cache)
	if err != nil {
		return
	}
	type digestDir struct {
		name    string
		modTime time.Time
	}
	var candidates []digestDir
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		if name == "target" || name == activeDigest || len(name) != 64 {
			continue
		}
		info, statErr := entry.Info()
		if statErr != nil {
			continue
		}
		candidates = append(candidates, digestDir{name: name, modTime: info.ModTime()})
	}
	keepOthers := max(keepCount-1, 0)
	if len(candidates) <= keepOthers {
		return
	}
	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].modTime.After(candidates[j].modTime)
	})
	for _, old := range candidates[keepOthers:] {
		_ = os.RemoveAll(filepath.Join(cache, old.name))
	}
}

// ExportHostSources writes the Host's Cargo sources beneath root:
// `scenery-kernel/` and the `scenery-sdk/scenery-wire/` crate it depends on.
func ExportHostSources(root string) error {
	return materializeHostSources(root)
}

func materializeHostSources(root string) error {
	return fs.WalkDir(sdksource.Host, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(path))
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := sdksource.Host.ReadFile(path)
		if err != nil {
			return err
		}
		if previous, err := os.ReadFile(target); err == nil && bytes.Equal(previous, data) {
			return nil
		}
		return os.WriteFile(target, data, 0o600)
	})
}
