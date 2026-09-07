package main

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	localagent "scenery.sh/internal/agent"
)

type fakeWorktreePostgresDocker struct {
	id        string
	volume    *worktreePostgresVolume
	container *worktreePostgresContainer
	writes    int
	failAfter string
}

type memoryWorktreePostgresState struct {
	record *localagent.WorktreeRecord
}

func (s *memoryWorktreePostgresState) read() (localagent.WorktreeRecord, error) {
	if s.record == nil {
		return localagent.WorktreeRecord{}, os.ErrNotExist
	}
	record := *s.record
	if record.Postgres != nil {
		postgres := *record.Postgres
		record.Postgres = &postgres
	}
	return record, nil
}

func (s *memoryWorktreePostgresState) SaveRecord(record localagent.WorktreeRecord) error {
	if record.Postgres != nil {
		postgres := *record.Postgres
		record.Postgres = &postgres
	}
	s.record = &record
	return nil
}

func (*memoryWorktreePostgresState) Close() error { return nil }

func (s *memoryWorktreePostgresState) RetirePostgres(instance string) error {
	if s.record.Postgres.InstanceID != instance || s.record.Postgres.Phase != "deleting" {
		return errors.New("wrong retirement intent")
	}
	s.record.Postgres = nil
	return nil
}

func (d *fakeWorktreePostgresDocker) Identity(context.Context) (string, string, error) {
	return d.id, "unix:///owned/docker.sock", nil
}

func (d *fakeWorktreePostgresDocker) HasRetainedResources(context.Context, string) (bool, error) {
	return d.volume != nil || d.container != nil, nil
}

func (d *fakeWorktreePostgresDocker) Inspect(context.Context, localagent.WorktreeRecord) (*worktreePostgresVolume, *worktreePostgresContainer, error) {
	return d.volume, d.container, nil
}

func (d *fakeWorktreePostgresDocker) CreateVolume(_ context.Context, record localagent.WorktreeRecord) error {
	d.writes++
	d.volume = &worktreePostgresVolume{Name: record.Postgres.Volume, CreatedAt: "2026-09-07T12:00:00Z", Driver: "local", Mountpoint: "/owned/data", Labels: worktreePostgresLabels(record)}
	if d.failAfter == "volume" {
		return errors.New("interrupted after volume creation")
	}
	return nil
}

func (d *fakeWorktreePostgresDocker) CreateContainer(_ context.Context, record localagent.WorktreeRecord) error {
	d.writes++
	d.container = &worktreePostgresContainer{
		ID: "owned-container", Name: record.Postgres.Container, Image: record.Postgres.Image,
		Volume: record.Postgres.Volume, Mountpoint: "/var/lib/postgresql", MountSource: d.volume.Mountpoint,
		Writable: true, HostIP: "127.0.0.1", Labels: worktreePostgresLabels(record),
	}
	if d.failAfter == "container" {
		return errors.New("interrupted after container creation")
	}
	return nil
}

func (d *fakeWorktreePostgresDocker) Start(context.Context, localagent.WorktreeRecord, string) error {
	d.writes++
	d.container.Running, d.container.Port = true, 55432
	return nil
}

func (d *fakeWorktreePostgresDocker) Stop(context.Context, localagent.WorktreeRecord, string) error {
	d.writes++
	d.container.Running = false
	return nil
}

func (d *fakeWorktreePostgresDocker) RemoveContainer(context.Context, localagent.WorktreeRecord, string) error {
	d.writes++
	d.container = nil
	return nil
}

func (d *fakeWorktreePostgresDocker) RemoveVolume(context.Context, localagent.WorktreeRecord) error {
	d.writes++
	d.volume = nil
	return nil
}

func testWorktreePostgresResolver(t *testing.T) (worktreePostgresResolver, *fakeWorktreePostgresDocker) {
	t.Helper()
	paths, err := localagent.PathsForWorktree(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	docker := &fakeWorktreePostgresDocker{id: "owned-daemon"}
	state := &memoryWorktreePostgresState{}
	return worktreePostgresResolver{
		paths: paths, appID: "books", docker: docker,
		probe:       func(context.Context, *localagent.WorktreePostgres) (string, error) { return "123456789", nil },
		legacyClaim: func() error { return nil },
		readState:   state.read,
		beginState:  func() (worktreePostgresOperation, error) { return state, nil },
	}, docker
}

func TestWorktreePostgresPendingVolumeRecovery(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	docker.failAfter = "volume"
	if _, err := r.ensure(context.Background()); err == nil {
		t.Fatal("interrupted creation succeeded")
	}
	pending, err := r.load()
	if err != nil || pending.Postgres.Phase != "pending" {
		t.Fatalf("pending authority was not retained: %v", err)
	}
	docker.failAfter = ""
	ready, err := r.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ready.InstanceID != pending.Postgres.InstanceID || ready.Password != pending.Postgres.Password || docker.writes != 3 {
		t.Fatal("retry replaced credentials, instance, or repeated volume creation")
	}
}

func TestWorktreePostgresMissingAuthorityRefusesRetainedResources(t *testing.T) {
	for _, object := range []string{"volume", "container"} {
		t.Run(object, func(t *testing.T) {
			r, docker := testWorktreePostgresResolver(t)
			if object == "volume" {
				docker.volume = &worktreePostgresVolume{Name: "retained"}
			} else {
				docker.container = &worktreePostgresContainer{ID: "retained"}
			}
			if _, err := r.ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "without matching local authority") {
				t.Fatalf("missing authority was not rejected: %v", err)
			}
			if docker.writes != 0 {
				t.Fatal("orphaned resource was mutated")
			}
			if _, err := r.readRecord(); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("replacement credentials were persisted: %v", err)
			}
		})
	}
}

func TestWorktreePostgresRestoreIntentPrecedesProvisioning(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	op, err := r.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	intent := &localagent.WorktreePostgresRestore{ArchiveSHA256: "sha256:" + strings.Repeat("a", 64), Mode: "overwrite", StartedAt: time.Now().UTC()}
	restore := worktreeRestoreOperation{worktreePostgresOperation: op, intent: intent}
	docker.failAfter = "volume"
	if _, err := r.ensureResourceWithOperation(t.Context(), restore, true, true); err == nil {
		t.Fatal("interrupted provisioning succeeded")
	}
	pending, err := r.load()
	if err != nil || pending.Postgres.Restore == nil || pending.Postgres.Restore.SQLStarted {
		t.Fatalf("restore intent was not retained before SQL: %v", err)
	}
	writes := docker.writes
	if _, err := r.ensure(t.Context()); err == nil || docker.writes != writes {
		t.Fatal("ordinary startup bypassed pending restore")
	}
	docker.failAfter = ""
	ready, err := r.ensureResourceWithOperation(t.Context(), restore, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if ready.InstanceID != pending.Postgres.InstanceID || ready.Password != pending.Postgres.Password || ready.Restore == nil || ready.Phase != "restoring" {
		t.Fatal("recovery replaced authority or exposed incomplete restore as ready")
	}
}

func TestWorktreePostgresObservationUsesAuthenticatedLiveEndpoint(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	if _, err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	docker.container.Port, docker.writes = 55439, 0
	r.beginState = func() (worktreePostgresOperation, error) {
		t.Fatal("observation acquired a creating operation")
		return nil, nil
	}
	r.probe = func(_ context.Context, p *localagent.WorktreePostgres) (string, error) {
		if p.Port != 55439 {
			t.Fatal("authentication used the stale retained port")
		}
		return "123456789", nil
	}
	observed, running, err := r.observe(context.Background())
	if err != nil || !running || observed.Port != 55439 || docker.writes != 0 {
		t.Fatalf("read-only observation failed: %v", err)
	}
	r.probe = func(context.Context, *localagent.WorktreePostgres) (string, error) { return "different-cluster", nil }
	if _, _, err := r.observe(context.Background()); err == nil || docker.writes != 0 {
		t.Fatal("observation accepted another authenticated cluster or mutated it")
	}
}

func TestWorktreePostgresStopRetainsAndRemoveRetiresAuthority(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	original, err := r.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, err := r.load()
	if err != nil || record.Postgres.Password != original.Password || docker.volume == nil || docker.container.Running {
		t.Fatal("stop failed to retain stopped database authority")
	}
	// An explicitly selected inactive cluster may be discarded after a
	// failed restore, but its recovery authority survives until deletion.
	record.Postgres.Phase = "restore-failed"
	record.Postgres.Restore = &localagent.WorktreePostgresRestore{ArchiveSHA256: "sha256:" + strings.Repeat("a", 64), Mode: "overwrite", StartedAt: time.Now(), SQLStarted: true}
	op, err := r.beginOperation()
	if err != nil {
		t.Fatal(err)
	}
	if err := op.SaveRecord(record); err != nil {
		t.Fatal(err)
	}
	if err := op.Close(); err != nil {
		t.Fatal(err)
	}
	if err := r.remove(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.load(); !errors.Is(err, os.ErrNotExist) || docker.volume != nil || docker.container != nil {
		t.Fatal("verified removal did not retire resource authority")
	}
}

func TestWorktreePostgresPendingContainerRecovery(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	docker.failAfter = "container"
	if _, err := r.ensure(context.Background()); err == nil {
		t.Fatal("interrupted creation succeeded")
	}
	docker.failAfter = ""
	if _, err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if docker.writes != 3 {
		t.Fatal("retry repeated external creation")
	}
}

func TestWorktreePostgresLostVolumeFailsClosed(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	if _, err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	docker.volume, docker.container, docker.writes = nil, nil, 0
	if _, err := r.ensure(context.Background()); err == nil || docker.writes != 0 {
		t.Fatal("lost data was treated as a new allocation")
	}
}

func TestWorktreePostgresContainerRecreationRetainsData(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	original, err := r.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	docker.container, docker.writes = nil, 0
	recreated, err := r.ensure(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if docker.writes != 2 || recreated.Password != original.Password || recreated.SystemID != original.SystemID || recreated.VolumeCreatedAt != original.VolumeCreatedAt {
		t.Fatal("recreation did not retain the data authority")
	}
}

func TestWorktreePostgresWrongDaemonDoesNotWrite(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	if _, err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	docker.id, docker.writes = "other-daemon", 0
	if _, err := r.ensure(context.Background()); err == nil || docker.writes != 0 {
		t.Fatal("daemon conflict was accepted")
	}
}

func TestWorktreePostgresWrongMountDoesNotStart(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	if _, err := r.ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	docker.container.Running, docker.container.Volume, docker.writes = false, "unrelated", 0
	if _, err := r.ensure(context.Background()); err == nil || docker.writes != 0 {
		t.Fatal("wrong volume mount was accepted or started")
	}
}

func TestWorktreePostgresLegacyClaimBlocksFreshAllocation(t *testing.T) {
	r, docker := testWorktreePostgresResolver(t)
	r.legacyClaim = func() error { return errors.New("migration required") }
	if _, err := r.ensure(context.Background()); err == nil || docker.writes != 0 {
		t.Fatal("legacy claim did not block fresh allocation")
	}
	if _, err := r.readRecord(); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("legacy guard created replacement ownership")
	}
}
