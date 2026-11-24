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

	"scenery.sh/internal/devcache"
	"scenery.sh/internal/devprocess"
	sdksource "scenery.sh/sdk/rust"
)

const hostToolchain = "1.99.1"

// Host builds the framework-owned supervisor only on cache miss.
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
	hash := sha256.New()
	_, _ = fmt.Fprintln(hash, hostToolchain, runtime.GOOS, runtime.GOARCH)
	err = fs.WalkDir(sdksource.Host, ".", func(path string, entry fs.DirEntry, err error) error {
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
	root := filepath.Join(cache, fmt.Sprintf("%x", hash.Sum(nil)))
	executable := filepath.Join(root, "scenery-host")
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
	command := exec.CommandContext(ctx, "cargo", "+"+hostToolchain, "build", "--locked", "--release",
		"--manifest-path", filepath.Join(sources, "scenery-host", "Cargo.toml"), "--target-dir", target)
	command.Dir = filepath.Join(sources, "scenery-host")
	if output, err := command.CombinedOutput(); err != nil {
		return "", fmt.Errorf("build worker supervisor: %w\n%s", err, output)
	}
	binary, err := os.Open(filepath.Join(target, "release", "scenery-host"))
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
	return executable, os.Rename(staged.Name(), executable)
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
