package stateupgrade

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/machine"
)

type testRecord struct {
	machine.ArtifactIdentity
	Value string `json:"value"`
}

func fixtureChange(t *testing.T, path string) Change {
	t.Helper()
	const kind = "scenery.test.retained"
	const shape = `{"identity":"artifact","value":"string"}`
	value := testRecord{ArtifactIdentity: machine.NewArtifactIdentity(kind, shape), Value: "fixture-credential"}
	value.SpecRevision = "sha256:" + strings.Repeat("b", 64)
	before, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var target testRecord
	after, err := machine.PrepareArtifactSpecUpgrade(before, &target, &target.ArtifactIdentity, kind, shape)
	if err != nil {
		t.Fatal(err)
	}
	return Change{Path: path, Kind: kind, Before: before, After: after}
}

func fixtureStore(t *testing.T) (*Store, []Change) {
	t.Helper()
	root := t.TempDir()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
	changes := []Change{fixtureChange(t, "worktree.json"), fixtureChange(t, "storage/owner.json")}
	for _, change := range changes {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, change.Path)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, change.Path), change.Before, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	// Unit tests keep atomic filesystem semantics and inject durability/failure
	// cuts. Real fsync and public CLI publication belong to native probes.
	store.options = atomicfile.Options{}
	store.syncDir = func(string) error { return nil }
	t.Cleanup(func() { _ = store.Close() })
	return store, changes
}

func TestPreviewIsReadOnlyAndRevisionBound(t *testing.T) {
	s, changes := fixtureStore(t)
	before, err := os.ReadDir(s.path)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadDir(s.path)
	if err != nil || len(before) != len(after) || plan.ChangedFiles() != 2 || plan.Pending {
		t.Fatalf("preview mutated state or has wrong summary: %v", err)
	}
	if _, err := s.Apply(plan, "sha256:"+strings.Repeat("c", 64)); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("stale revision: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.path, backupRoot)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("rejected apply created backups: %v", err)
	}
}

func TestApplyPreservesExactBackupsAndCurrentNoop(t *testing.T) {
	s, changes := fixtureStore(t)
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	path, err := s.Apply(plan, plan.Revision)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var saved backup
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	for i, change := range changes {
		actual, err := os.ReadFile(filepath.Join(s.path, change.Path))
		if err != nil || !bytes.Equal(actual, change.After) || !bytes.Equal(saved.Changes[i].Before, change.Before) {
			t.Fatalf("metadata or exact backup differs: %v", err)
		}
		changes[i].Before = bytes.Clone(change.After)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("backup privacy: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.path, PendingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("completed transaction still pending: %v", err)
	}
	current, err := s.Resolve(changes)
	if err != nil || current.ChangedFiles() != 0 {
		t.Fatalf("current preview: %v", err)
	}
	s.write = func(string, []byte) error { t.Fatal("no-op wrote state"); return nil }
	if _, err := s.Apply(current, current.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestInterruptedApplyResumesOriginalTransaction(t *testing.T) {
	s, changes := fixtureStore(t)
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	injected := errors.New("interrupted write")
	write := s.write
	s.write = func(name string, data []byte) error {
		if name == "storage/owner.json" {
			return injected
		}
		return write(name, data)
	}
	if _, err := s.Apply(plan, plan.Revision); !errors.Is(err, injected) {
		t.Fatalf("expected interruption: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.path, PendingName)); err != nil {
		t.Fatalf("interruption lost recovery guard: %v", err)
	}
	observed := append([]Change(nil), changes...)
	observed[0].Before = bytes.Clone(changes[0].After)
	resumed, err := s.Resolve(observed)
	if err != nil || !resumed.Pending || resumed.Revision != plan.Revision || resumed.Updated != 1 {
		t.Fatalf("resume lost original selection: %#v, %v", resumed, err)
	}
	s.write = write
	if _, err := s.Apply(resumed, resumed.Revision); err != nil {
		t.Fatal(err)
	}
}

func TestUpgradeRejectsChangedAndUnsafeMetadata(t *testing.T) {
	s, changes := fixtureStore(t)
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.path, changes[1].Path), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(plan, plan.Revision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("changed metadata: %v", err)
	}
	if err := os.Remove(filepath.Join(s.path, changes[1].Path)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.path, changes[0].Path), filepath.Join(s.path, changes[1].Path)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Apply(plan, plan.Revision); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("symlink metadata: %v", err)
	}
	invalid := append([]Change(nil), changes...)
	invalid[1].Path = "../outside.json"
	if _, err := s.Resolve(invalid); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("escaping metadata: %v", err)
	}
}

func TestBackupFailureNeverPublishesMetadata(t *testing.T) {
	s, changes := fixtureStore(t)
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	failure := errors.New("backup persistence failed")
	s.write = func(string, []byte) error { return failure }
	if _, err := s.Apply(plan, plan.Revision); !errors.Is(err, failure) {
		t.Fatalf("backup failure: %v", err)
	}
	if err := s.checkFiles(changes, false); err != nil {
		t.Fatalf("metadata changed before backup: %v", err)
	}
	if _, err := os.Stat(filepath.Join(s.path, PendingName)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("failed backup published a pending transaction: %v", err)
	}
}

func TestCompletionFailureResumesWithoutRewritingMetadata(t *testing.T) {
	s, changes := fixtureStore(t)
	plan, err := s.Resolve(changes)
	if err != nil {
		t.Fatal(err)
	}
	remove := s.remove
	failure := errors.New("completion removal failed")
	s.remove = func(string) error { return failure }
	if _, err := s.Apply(plan, plan.Revision); !errors.Is(err, failure) {
		t.Fatalf("completion failure: %v", err)
	}
	observed := append([]Change(nil), changes...)
	for i := range observed {
		observed[i].Before = bytes.Clone(observed[i].After)
	}
	resumed, err := s.Resolve(observed)
	if err != nil || !resumed.Pending || resumed.Updated != 2 {
		t.Fatalf("completed metadata lost recovery identity: %v", err)
	}
	changedSelection := append(append([]Change(nil), observed...), fixtureChange(t, "new.json"))
	if _, err := s.Resolve(changedSelection); !errors.Is(err, ErrPrecondition) {
		t.Fatalf("changed selection resumed: %v", err)
	}
	s.remove = remove
	write := s.write
	s.write = func(name string, data []byte) error {
		if name == "worktree.json" || name == "storage/owner.json" {
			t.Fatal("resume rewrote completed metadata")
		}
		return write(name, data)
	}
	if _, err := s.Apply(resumed, resumed.Revision); err != nil {
		t.Fatal(err)
	}
}
