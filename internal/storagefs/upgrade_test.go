package storagefs

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"scenery.sh/internal/stateupgrade"
)

func upgradeNamespaceFixture(t *testing.T) (*Namespace, *Store, Object, map[string][]byte) {
	t.Helper()
	root := t.TempDir()
	key := strings.Repeat("a", 64)
	parent := filepath.Join(root, key)
	if err := os.Mkdir(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	binding := Binding{AppID: "storage-upgrade", AppRoot: root, WorktreeKey: key, UserID: os.Getuid(), Managed: true}
	n, err := allocate(t.Context(), filepath.Join(parent, "storage"), binding, testDiskIO())
	if err != nil {
		t.Fatal(err)
	}
	store, err := n.Store(Scope{Store: "files", Tenant: "tenant"}, 1024)
	if err != nil {
		t.Fatal(err)
	}
	object, err := store.Put(t.Context(), "kept.bin", strings.NewReader("kept payload"), PutOptions{ContentType: "application/octet-stream", Metadata: map[string]string{"Mixed-Case": "keep me"}})
	if err != nil {
		t.Fatal(err)
	}
	original := make(map[string][]byte)
	err = filepath.WalkDir(n.Path, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || !strings.HasSuffix(path, ".json") {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(data, &fields); err != nil {
			return err
		}
		fields["spec_revision"], _ = json.Marshal("sha256:" + strings.Repeat("b", 64))
		data, err = json.Marshal(fields)
		if err != nil {
			return err
		}
		original[path] = data
		return os.WriteFile(path, data, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	return n, store, *object, original
}

func TestStorageSpecUpgradePreservesObjectsAndIdentity(t *testing.T) {
	n, objectStore, object, originals := upgradeNamespaceFixture(t)
	if _, err := Discover(t.Context(), n.Path, n.Binding); err == nil {
		t.Fatal("ordinary reader accepted old specification")
	}
	// A process can exit before atomic metadata replacement. Its unpublished
	// temp file must not prevent safe retry or later ordinary generation reads.
	for path := range originals {
		if filepath.Base(path) == "owner.json" || filepath.Base(path) == "generation.json" {
			temp := filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".tmp-"+strings.Repeat("d", 32))
			if err := os.WriteFile(temp, []byte("unfinished"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	prepared, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if prepared != nil {
			_ = prepared.Close()
		}
	}()
	// Only the owner and the generation are bound to the specification.
	if len(prepared.Changes) != 2 || prepared.owner.Incarnation != n.Incarnation {
		t.Fatal("upgrade selected wrong namespace metadata")
	}
	for _, change := range prepared.Changes {
		if name := filepath.Base(change.Path); name != "owner.json" && name != "generation.json" {
			t.Fatalf("upgrade selected %s", change.Path)
		}
	}
	for path, expected := range originals {
		actual, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("preview modified source: %v", err)
		}
	}
	// Validate prepared bytes through ordinary domain readers. The real
	// multi-owner transaction and fsync are covered by the public CLI probe.
	for _, change := range prepared.Changes {
		if err := os.WriteFile(filepath.Join(filepath.Dir(n.Path), change.Path), change.After, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
	prepared = nil
	body, actual, err := objectStore.Get(t.Context(), object.Key, GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(body)
	if err := errors.Join(readErr, body.Close()); err != nil {
		t.Fatal(err)
	}
	// The reference still carries the specification revision that wrote it.
	for path, expected := range originals {
		if !strings.Contains(path, string(filepath.Separator)+"refs"+string(filepath.Separator)) {
			continue
		}
		if actual, err := os.ReadFile(path); err != nil || !bytes.Equal(actual, expected) {
			t.Fatalf("upgrade rewrote a reference: %v", err)
		}
	}
	if string(data) != "kept payload" || actual.ETag != object.ETag || actual.SHA256 != object.SHA256 || actual.Metadata["Mixed-Case"] != "keep me" || !actual.ModifiedAt.Equal(object.ModifiedAt) {
		t.Fatal("object content or logical identity changed")
	}
}

func TestStorageSpecUpgradeRejectsSchemaAndPendingLifecycle(t *testing.T) {
	n, _, _, originals := upgradeNamespaceFixture(t)
	ownerPath := filepath.Join(n.Path, "owner.json")
	var owner Owner
	if err := json.Unmarshal(originals[ownerPath], &owner); err != nil {
		t.Fatal(err)
	}
	owner.SchemaRevision = "sha256:" + strings.Repeat("c", 64)
	data, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownerPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding); err == nil {
		t.Fatal("different schema accepted")
	}
	if err := os.WriteFile(ownerPath, originals[ownerPath], 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(n.Path, "operation.json"), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding); !errors.Is(err, ErrRecovery) {
		t.Fatalf("pending lifecycle accepted: %v", err)
	}
}

// References are not part of an upgrade, so ordinary reads own their checks:
// the current schema under any well-formed specification revision.
func TestStorageReadsRejectInvalidReferencesOfAnotherSpecification(t *testing.T) {
	for _, tt := range []struct {
		name   string
		mutate func(fields map[string]json.RawMessage)
	}{
		{"misaddressed", func(fields map[string]json.RawMessage) {
			var object map[string]json.RawMessage
			if err := json.Unmarshal(fields["object"], &object); err != nil {
				t.Fatal(err)
			}
			object["key"], _ = json.Marshal("different.bin")
			fields["object"], _ = json.Marshal(object)
		}},
		{"other schema", func(fields map[string]json.RawMessage) {
			fields["schema_revision"], _ = json.Marshal("sha256:" + strings.Repeat("c", 64))
		}},
		{"malformed specification", func(fields map[string]json.RawMessage) {
			fields["spec_revision"], _ = json.Marshal("unknown")
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			n, store, object, originals := upgradeNamespaceFixture(t)
			prepared, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding)
			if err != nil {
				t.Fatal(err)
			}
			for _, change := range prepared.Changes {
				if err := os.WriteFile(filepath.Join(filepath.Dir(n.Path), change.Path), change.After, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := prepared.Close(); err != nil {
				t.Fatal(err)
			}
			for path, data := range originals {
				if !strings.Contains(path, string(filepath.Separator)+"refs"+string(filepath.Separator)) {
					continue
				}
				var fields map[string]json.RawMessage
				if err := json.Unmarshal(data, &fields); err != nil {
					t.Fatal(err)
				}
				tt.mutate(fields)
				if data, err = json.Marshal(fields); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, _, err := store.Get(t.Context(), object.Key, GetOptions{}); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("invalid reference accepted: %v", err)
			}
		})
	}
}

func TestStorageSpecUpgradeBlocksOrdinaryAccessWhilePending(t *testing.T) {
	n, _, _, _ := upgradeNamespaceFixture(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(n.Path), stateupgrade.PendingName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(t.Context(), n.Path, n.Binding); !errors.Is(err, ErrRecovery) {
		t.Fatalf("ordinary namespace access bypassed pending upgrade: %v", err)
	}
}

func TestStorageSpecUpgradeRefusesAHeldMaintenanceLease(t *testing.T) {
	n, _, _, _ := upgradeNamespaceFixture(t)
	root, err := openNamespaceRoot(n.Path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	held, err := lockFile(t.Context(), root, "maintenance.lock", true)
	if err != nil {
		t.Fatal(err)
	}
	wait := specUpgradeLeaseWait
	specUpgradeLeaseWait = 10 * time.Millisecond
	defer func() { specUpgradeLeaseWait = wait }()
	// Only the wait for the lease is bounded; the caller's context stays live
	// for validating a namespace of any size.
	if _, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding); !errors.Is(err, context.DeadlineExceeded) || !strings.Contains(err.Error(), "maintenance lease is held") || t.Context().Err() != nil {
		t.Fatalf("held lease: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if err := prepared.Close(); err != nil {
		t.Fatal(err)
	}
}
