package storagefs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixtureObject(key, value string) Object {
	sum := sha256.Sum256([]byte(value))
	return Object{Store: "files", Tenant: "tenant", Key: key, SizeBytes: int64(len(value)), SHA256: hex.EncodeToString(sum[:]), ModifiedAt: time.Unix(1, 0).UTC(), Metadata: map[string]string{"source": "fixture"}}
}

func TestRestoreSwitchesCompleteGenerationWithFreshVersions(t *testing.T) {
	n := testNamespace(t)
	ctx := context.Background()
	s, _ := n.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	before, err := s.Put(ctx, "a", strings.NewReader("old"), PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := Discover(ctx, n.Path, n.Binding)
	lockInfo, _ := os.Stat(filepath.Join(n.Path, "maintenance.lock"))
	restore, err := n.BeginRestore(ctx, strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := restore.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "new"), strings.NewReader("new"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if err := restore.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := restore.Close(); err != nil {
		t.Fatal(err)
	}
	body, after, err := s.Get(ctx, "a", GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	bytes, err := io.ReadAll(body)
	if err := errors.Join(err, body.Close()); err != nil {
		t.Fatal(err)
	}
	if string(bytes) != "new" || after.ETag == before.ETag || !after.ModifiedAt.Equal(time.Unix(1, 0)) || after.Metadata["source"] != "fixture" {
		t.Fatalf("bad restored object: %+v %q", after, bytes)
	}
	next, _ := Discover(ctx, n.Path, n.Binding)
	if owner.Incarnation != next.Incarnation || owner.Generation == next.Generation {
		t.Fatal("wrong allocation transition")
	}
	nextLock, _ := os.Stat(filepath.Join(n.Path, "maintenance.lock"))
	if !os.SameFile(lockInfo, nextLock) {
		t.Fatal("replaced maintenance inode")
	}
	if _, err := os.Stat(filepath.Join(n.Path, generationPath(owner.Generation))); err != nil {
		t.Fatal("old generation removed before explicit cleanup")
	}
}

func TestRestoreDatabaseFailureRequiresExactResume(t *testing.T) {
	n := testNamespace(t)
	ctx := context.Background()
	digest := strings.Repeat("b", 64)
	r, err := n.BeginRestore(ctx, digest, true, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "value"), strings.NewReader("value"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	cut := errors.New("database acknowledgement lost")
	if err := r.Complete(ctx, func(context.Context) error { return cut }); !errors.Is(err, cut) {
		t.Fatal(err)
	}
	_ = r.Close()
	if err := n.CheckReady(ctx); !errors.Is(err, ErrRecovery) {
		t.Fatalf("missing recovery barrier: %v", err)
	}
	if _, err := n.BeginRestore(ctx, strings.Repeat("c", 64), true, "overwrite", "fail"); !errors.Is(err, ErrRecovery) {
		t.Fatalf("wrong digest resumed: %v", err)
	}
	if _, err := n.BeginRestore(ctx, digest, false, "overwrite", "fail"); !errors.Is(err, ErrRecovery) {
		t.Fatalf("wrong classes resumed: %v", err)
	}
	r, err = n.BeginRestore(ctx, digest, true, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Resuming() {
		t.Fatal("lost resume state")
	}
	if err := r.Stage(ctx, func(*Generation) error { t.Fatal("restaged recorded generation"); return nil }); err != nil {
		t.Fatal(err)
	}
	calls := 0
	if err := r.Complete(ctx, func(context.Context) error { calls++; return nil }); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if calls != 1 {
		t.Fatal("uncertain database restore not repeated")
	}
	if err := n.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestRestoreFailedStagePreservesActiveGeneration(t *testing.T) {
	n := testNamespace(t)
	ctx := context.Background()
	before, _ := Discover(ctx, n.Path, n.Binding)
	r, err := n.BeginRestore(ctx, strings.Repeat("a", 64), false, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("a", "complete"), strings.NewReader("short"), "fail")
	}); err == nil {
		t.Fatal("accepted truncated payload")
	}
	_ = r.Close()
	after, err := Discover(ctx, n.Path, n.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if before.Generation != after.Generation {
		t.Fatal("failed stage selected partial data")
	}
	if err := n.CheckReady(ctx); !errors.Is(err, ErrRecovery) {
		t.Fatalf("failed staging lost its pinned recovery barrier: %v", err)
	}
}

func TestRestoreMergeStagesEffectiveState(t *testing.T) {
	n := testNamespace(t)
	ctx := context.Background()
	s, _ := n.Store(Scope{Store: "files", Tenant: "tenant"}, 0)
	if _, err := s.Put(ctx, "retained", strings.NewReader("old"), PutOptions{}); err != nil {
		t.Fatal(err)
	}
	r, err := n.BeginRestore(ctx, strings.Repeat("d", 64), false, "merge", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(g *Generation) error {
		return g.Put(ctx, fixtureObject("added", "new"), strings.NewReader("new"), "fail")
	}); err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, nil); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	for _, key := range []string{"retained", "added"} {
		if _, err := s.Head(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := n.BeginRestore(ctx, strings.Repeat("d", 64), true, "merge", "fail"); !errors.Is(err, ErrInvalid) {
		t.Fatal("combined merge accepted")
	}
}

func TestRestoreOwnerPublicationFailureRetainsRecovery(t *testing.T) {
	n := testNamespace(t)
	ctx := context.Background()
	r, err := n.BeginRestore(ctx, strings.Repeat("e", 64), true, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Stage(ctx, func(*Generation) error { return nil }); err != nil {
		t.Fatal(err)
	}
	replace := n.io.replace
	cut := errors.New("owner sync acknowledgement lost")
	n.io.replace = func(root *os.Root, name string, data []byte) error {
		if err := replace(root, name, data); err != nil {
			return err
		}
		if name == "owner.json" {
			return cut
		}
		return nil
	}
	if err := r.Complete(ctx, func(context.Context) error { return nil }); !errors.Is(err, cut) {
		t.Fatal(err)
	}
	_ = r.Close()
	n.io.replace = replace
	if err := n.CheckReady(ctx); !errors.Is(err, ErrRecovery) {
		t.Fatal(err)
	}
	pending, err := n.Pending(ctx)
	if err != nil || pending == nil || !pending.Database || pending.ArchiveSHA256 != strings.Repeat("e", 64) || pending.Phase != "database" {
		t.Fatalf("generation publication lost combined recovery authority: %+v, %v", pending, err)
	}
	owner, err := Discover(ctx, n.Path, n.Binding)
	if err != nil || owner.Generation != pending.StagedGeneration {
		t.Fatalf("fault did not occur after generation publication: %+v, %v", owner, err)
	}
	r, err = n.BeginRestore(ctx, strings.Repeat("e", 64), true, "overwrite", "fail")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Complete(ctx, func(context.Context) error { t.Fatal("database replayed after proven switch"); return nil }); err != nil {
		t.Fatal(err)
	}
	_ = r.Close()
	if err := n.CheckReady(ctx); err != nil {
		t.Fatal(err)
	}
}
