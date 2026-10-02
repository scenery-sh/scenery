package storagefs

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestReferenceShardRejectsReplacementAndSymlinkAlias(t *testing.T) {
	s := testStore(t)
	putText(t, s, "key", "body", PutOptions{})
	lease, err := s.namespace.acquire(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Close() }()
	name := filepath.Dir(s.scope.refPath(lease.owner.Generation, "key"))
	expected, err := lease.root.Lstat(name)
	if err != nil {
		t.Fatal(err)
	}
	root, err := openReferenceShard(lease.root, name, expected)
	if err != nil {
		t.Fatal(err)
	}
	_ = root.Close()
	if err := lease.root.Rename(name, name+"-moved"); err != nil {
		t.Fatal(err)
	}
	if err := lease.root.Mkdir(name, 0o700); err != nil {
		t.Fatal(err)
	}
	if root, err := openReferenceShard(lease.root, name, expected); err == nil {
		_ = root.Close()
		t.Fatal("accepted a replaced shard identity")
	}
	if err := lease.root.Remove(name); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Base(name)+"-moved", filepath.Join(s.namespace.Path, name)); err != nil {
		t.Fatal(err)
	}
	if root, err := openReferenceShard(lease.root, name, expected); err == nil {
		_ = root.Close()
		t.Fatal("accepted a symlink selecting the original shard")
	}
}
