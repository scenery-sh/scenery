package storagefs

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
	if len(prepared.Changes) != 3 || prepared.owner.Incarnation != n.Incarnation {
		t.Fatal("upgrade selected wrong namespace metadata")
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

func TestStorageSpecUpgradeRejectsInvalidReferenceAndRecoveryAccess(t *testing.T) {
	n, _, _, originals := upgradeNamespaceFixture(t)
	var referencePath string
	for path := range originals {
		if strings.Contains(path, string(filepath.Separator)+"refs"+string(filepath.Separator)) {
			referencePath = path
		}
	}
	var ref reference
	if err := json.Unmarshal(originals[referencePath], &ref); err != nil {
		t.Fatal(err)
	}
	ref.Object.Key = "different.bin"
	data, err := json.Marshal(ref)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(referencePath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSpecUpgrade(t.Context(), n.Path, n.Binding); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("misaddressed reference accepted: %v", err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(n.Path), stateupgrade.PendingName), []byte("pending"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(t.Context(), n.Path, n.Binding); !errors.Is(err, ErrRecovery) {
		t.Fatalf("ordinary namespace access bypassed pending upgrade: %v", err)
	}
}
