package storagefs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"scenery.sh/internal/atomicfile"
	"testing"
	"time"
)

// Unit tests exercise the protocol with injected durability boundaries. Real
// synchronization and cross-process/crash proof belongs to the storage probe.
func testDiskIO() diskIO {
	disk := durableIO()
	disk.clonePayload = nil // Native clone capability belongs to the release probe.
	disk.syncFile = func(*os.File) error { return nil }
	disk.replace = func(r *os.Root, name string, data []byte) error {
		return atomicfile.WriteRoot(r, name, data, 0o600, atomicfile.Options{})
	}
	return disk
}

func testBinding(root string) Binding {
	return Binding{AppID: "storage-test", AppRoot: root, UserID: os.Getuid()}
}

func testNamespace(t *testing.T) *Namespace {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	n, err := allocate(context.Background(), filepath.Join(dir, "storage"), testBinding(dir), testDiskIO())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDiscoveryDoesNotAllocate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent")
	if _, err := Discover(context.Background(), path, testBinding(dir)); !errors.Is(err, ErrUninitialized) {
		t.Fatalf("Discover: %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 0 {
		t.Fatalf("read-only discovery created state: %v, %v", entries, err)
	}
}

func TestNamespaceReopenPreservesAllocation(t *testing.T) {
	n := testNamespace(t)
	other, err := allocate(context.Background(), n.Path, n.Binding, testDiskIO())
	if err != nil {
		t.Fatal(err)
	}
	if other.Incarnation != n.Incarnation {
		t.Fatal("reallocated existing namespace")
	}
	l, err := n.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceRejectsWrongAuthority(t *testing.T) {
	n := testNamespace(t)
	wrong := n.Binding
	wrong.AppID = "other-app"
	if _, err := Discover(context.Background(), n.Path, wrong); !errors.Is(err, ErrOwnership) {
		t.Fatalf("wrong app: %v", err)
	}
	n.Incarnation = "stale"
	if _, err := n.acquire(context.Background(), false); !errors.Is(err, ErrOwnership) {
		t.Fatalf("stale incarnation: %v", err)
	}
}

func TestAllocatedMissingGenerationIsCorruption(t *testing.T) {
	n := testNamespace(t)
	owner, err := Discover(context.Background(), n.Path, n.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(n.Path, generationPath(owner.Generation), "refs")); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), n.Path, n.Binding); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing generation: %v", err)
	}
	if _, err := Allocate(context.Background(), n.Path, n.Binding); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("must not repair ready allocation: %v", err)
	}
}

func TestAllocatedMissingLockIsCorruption(t *testing.T) {
	n := testNamespace(t)
	if err := os.Remove(filepath.Join(n.Path, "mutation.lock")); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), n.Path, n.Binding); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(n.Path, "mutation.lock")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection recreated lock")
	}
}

func TestMalformedRecoveryBlocksOrdinaryLeaseNotOwnerInspection(t *testing.T) {
	n := testNamespace(t)
	if err := os.WriteFile(filepath.Join(n.Path, "operation.json"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), n.Path, n.Binding); err != nil {
		t.Fatal(err)
	}
	if _, err := n.acquire(context.Background(), false); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("pending recovery: %v", err)
	}
}

func TestIndependentLockDescriptionsExcludeWriters(t *testing.T) {
	n := testNamespace(t)
	r, err := openNamespaceRoot(n.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	first, err := lockFile(context.Background(), r, "mutation.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if second, err := lockFile(ctx, r, "mutation.lock", true); !errors.Is(err, context.DeadlineExceeded) {
		if second != nil {
			_ = second.Close()
		}
		t.Fatalf("independent writer bypassed lease: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := lockFile(context.Background(), r, "mutation.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestNamespaceRejectsSymlinkRoot(t *testing.T) {
	n := testNamespace(t)
	alias := filepath.Join(filepath.Dir(n.Path), "alias")
	if err := os.Symlink(n.Path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(context.Background(), alias, n.Binding); !errors.Is(err, ErrOwnership) {
		t.Fatalf("symlink root: %v", err)
	}
}

func TestTokensSeparateLogicalIdentities(t *testing.T) {
	seen := make(map[string]bool)
	for _, value := range []string{token("key", "a"), token("key", "a/b"), token("key", "A"), token("tenant", "a"), token("key", "ab", "c"), token("key", "a", "bc")} {
		if seen[value] {
			t.Fatal("token collision")
		}
		seen[value] = true
		if !isHexID(value, 32) {
			t.Fatalf("invalid token: %q", value)
		}
	}
}
