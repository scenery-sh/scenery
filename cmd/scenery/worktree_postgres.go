package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"

	localagent "scenery.sh/internal/agent"
)

type worktreePostgresVolume struct {
	Name, CreatedAt, Driver, Mountpoint string
	Labels                              map[string]string
}

type worktreePostgresContainer struct {
	ID, Name, Image, Volume, Mountpoint, MountSource string
	Running, Writable                                bool
	Port                                             int
	HostIP                                           string
	Labels                                           map[string]string
}

// This is a Docker lifecycle test seam, not a provider or public namespace API.
// Implementations pin the selected endpoint and verify its daemon before writes.
type worktreePostgresDocker interface {
	Identity(context.Context) (id, endpoint string, err error)
	HasRetainedResources(context.Context, string) (bool, error)
	Inspect(context.Context, localagent.WorktreeRecord) (*worktreePostgresVolume, *worktreePostgresContainer, error)
	CreateVolume(context.Context, localagent.WorktreeRecord) error
	CreateContainer(context.Context, localagent.WorktreeRecord) error
	Start(context.Context, localagent.WorktreeRecord, string) error
	Stop(context.Context, localagent.WorktreeRecord, string) error
	RemoveContainer(context.Context, localagent.WorktreeRecord, string) error
	RemoveVolume(context.Context, localagent.WorktreeRecord) error
}

type worktreePostgresResolver struct {
	paths  localagent.WorktreePaths
	appID  string
	docker worktreePostgresDocker
	// probe authenticates and returns pg_control_system().system_identifier.
	probe       func(context.Context, *localagent.WorktreePostgres) (string, error)
	legacyClaim func() error
	readState   func() (localagent.WorktreeRecord, error)
	beginState  func() (worktreePostgresOperation, error)
}

type worktreePostgresOperation interface {
	SaveRecord(localagent.WorktreeRecord) error
	RetirePostgres(string) error
	Close() error
}

func newWorktreePostgresResolver(ctx context.Context, root, appID string) (worktreePostgresResolver, error) {
	machinePaths, err := commandAgentPaths()
	if err != nil {
		return worktreePostgresResolver{}, err
	}
	paths, err := localagent.PathsForWorktree(machinePaths.Home, root)
	if err != nil {
		return worktreePostgresResolver{}, err
	}
	docker, err := newWorktreeDockerClient(ctx)
	if err != nil {
		return worktreePostgresResolver{}, err
	}
	return worktreePostgresResolver{
		paths: paths, appID: appID, docker: docker, probe: probeWorktreePostgres,
		legacyClaim: func() error { return localagent.CheckLegacyWorktreeClaim(machinePaths, paths) },
	}, nil
}

func (r worktreePostgresResolver) readRecord() (localagent.WorktreeRecord, error) {
	if r.readState != nil {
		return r.readState()
	}
	return r.paths.LoadRecord(r.appID)
}

func (r worktreePostgresResolver) beginOperation() (worktreePostgresOperation, error) {
	if r.beginState != nil {
		return r.beginState()
	}
	return r.paths.BeginOperation()
}

func worktreePostgresPrecondition(message string) error {
	return &codedCLIError{code: 3, err: fmt.Errorf("managed worktree PostgreSQL: %s; retained data and credentials were not replaced", message)}
}

func (r worktreePostgresResolver) load() (localagent.WorktreeRecord, error) {
	record, err := r.readRecord()
	if err != nil {
		return record, err
	}
	if record.Postgres == nil {
		return record, os.ErrNotExist
	}
	return record, nil
}

func (r worktreePostgresResolver) verifyDaemon(ctx context.Context, p *localagent.WorktreePostgres) error {
	id, endpoint, err := r.docker.Identity(ctx)
	if err != nil {
		return err
	}
	if id != p.DaemonID || endpoint != p.DaemonEndpoint {
		return worktreePostgresPrecondition("the selected Docker daemon does not match retained ownership")
	}
	return nil
}

func worktreePostgresLabels(record localagent.WorktreeRecord) map[string]string {
	return map[string]string{
		"scenery.worktree.root":     record.AppRoot,
		"scenery.worktree.app":      record.AppID,
		"scenery.worktree.user":     fmt.Sprint(record.UserID),
		"scenery.resource.instance": record.Postgres.InstanceID,
	}
}

func verifyWorktreePostgresLabels(record localagent.WorktreeRecord, labels map[string]string) error {
	for key, expected := range worktreePostgresLabels(record) {
		if labels[key] != expected {
			return worktreePostgresPrecondition("Docker object ownership labels do not match the complete worktree identity")
		}
	}
	return nil
}

// inspect is non-mutating. All extant objects are verified before callers may
// reconcile absence, start a stopped container, or update the observed endpoint.
func (r worktreePostgresResolver) inspect(ctx context.Context, record localagent.WorktreeRecord) (*worktreePostgresVolume, *worktreePostgresContainer, error) {
	p := record.Postgres
	if err := r.verifyDaemon(ctx, p); err != nil {
		return nil, nil, err
	}
	volume, container, err := r.docker.Inspect(ctx, record)
	if err != nil {
		return nil, nil, err
	}
	if volume == nil {
		if container != nil || (p.Phase != "deleting" && (p.VolumeCreatedAt != "" || p.SystemID != "")) {
			return nil, nil, worktreePostgresPrecondition("the retained data volume is missing; restore or explicitly recover it before retrying")
		}
	} else {
		if err := verifyWorktreePostgresLabels(record, volume.Labels); err != nil {
			return nil, nil, err
		}
		if volume.Name != p.Volume || volume.CreatedAt == "" || volume.Driver != "local" || volume.Mountpoint == "" ||
			(p.VolumeCreatedAt != "" && p.VolumeCreatedAt != volume.CreatedAt) ||
			(p.VolumeDriver != "" && p.VolumeDriver != volume.Driver) ||
			(p.VolumeMount != "" && p.VolumeMount != volume.Mountpoint) {
			return nil, nil, worktreePostgresPrecondition("the actual volume does not match the retained volume identity")
		}
	}
	if container != nil {
		if err := verifyWorktreePostgresLabels(record, container.Labels); err != nil {
			return nil, nil, err
		}
		if container.ID == "" || (p.ContainerID != "" && p.ContainerID != container.ID) || container.Name != p.Container || container.Image != p.Image ||
			container.Volume != p.Volume || container.Mountpoint != "/var/lib/postgresql" || !container.Writable ||
			volume == nil || container.MountSource != volume.Mountpoint || container.HostIP != "127.0.0.1" ||
			(container.Running && (container.Port < 1 || container.Port > 65535)) {
			return nil, nil, worktreePostgresPrecondition("the actual container, image, volume mount or loopback publication does not match retained ownership")
		}
	}
	return volume, container, nil
}

func (r worktreePostgresResolver) ensure(ctx context.Context) (*localagent.WorktreePostgres, error) {
	op, err := r.beginOperation()
	if err != nil {
		return nil, err
	}
	defer func() { _ = op.Close() }()
	return r.ensureWithOperation(ctx, op, true)
}

func (r worktreePostgresResolver) ensureWithOperation(ctx context.Context, op worktreePostgresOperation, allowAllocation bool) (*localagent.WorktreePostgres, error) {
	return r.ensureResourceWithOperation(ctx, op, allowAllocation, false)
}

func (r worktreePostgresResolver) ensureResourceWithOperation(ctx context.Context, op worktreePostgresOperation, allowAllocation, restoreRecovery bool) (*localagent.WorktreePostgres, error) {
	record, err := r.readRecord()
	if errors.Is(err, os.ErrNotExist) {
		if !allowAllocation {
			return nil, err
		}
		record = localagent.NewWorktreeRecord(r.paths, r.appID)
	} else if err != nil {
		return nil, err
	}
	if record.Postgres == nil {
		if !allowAllocation {
			return nil, os.ErrNotExist
		}
		if !record.SQLAllocationChecked {
			if r.legacyClaim == nil {
				return nil, worktreePostgresPrecondition("legacy data provenance has not been checked")
			}
			if err := r.legacyClaim(); err != nil {
				return nil, err
			}
			record.SQLAllocationChecked = true
		}
		id, endpoint, err := r.docker.Identity(ctx)
		if err != nil {
			return nil, err
		}
		retained, err := r.docker.HasRetainedResources(ctx, record.AppRoot)
		if err != nil {
			return nil, err
		}
		if retained {
			return nil, worktreePostgresPrecondition("Docker retains resources for this canonical worktree without matching local authority; explicitly recover the ownership record")
		}
		var instance [16]byte
		if _, err := rand.Read(instance[:]); err != nil {
			return nil, err
		}
		password, err := randomPostgresPassword()
		if err != nil {
			return nil, err
		}
		key := hex.EncodeToString(instance[:])
		record.Postgres = &localagent.WorktreePostgres{
			InstanceID: key, DaemonID: id, DaemonEndpoint: endpoint,
			Image: postgresServerImage, Major: 18, User: postgresServerUser, Password: password,
			Container: "scenery-pg-" + key, Volume: "scenery-pg-data-" + key, Phase: "pending",
		}
		// Credentials and random instance identity are durable before Docker is
		// allowed to create anything. Interrupted calls reconcile this same intent.
		if err := op.SaveRecord(record); err != nil {
			return nil, err
		}
	}
	p := record.Postgres
	if p.Major != 18 {
		return nil, worktreePostgresPrecondition("the retained PostgreSQL major requires an explicit validated engine migration")
	}
	if p.Phase == "deleting" || (!restoreRecovery && (p.Restore != nil || p.Phase == "restoring" || p.Phase == "restore-failed")) {
		return nil, worktreePostgresPrecondition("an unfinished restore or deletion requires explicit recovery")
	}
	volume, container, err := r.inspect(ctx, record)
	if err != nil {
		return nil, err
	}
	if p.SystemID != "" && container != nil && !restoreRecovery {
		// Authenticate retained data before persisting any reconciliation.
		// A bad credential or cluster identity must not rewrite authority.
		if !container.Running {
			if err := r.docker.Start(ctx, record, container.ID); err != nil {
				return nil, err
			}
			_, container, err = r.inspect(ctx, record)
			if err != nil {
				return nil, err
			}
			if container == nil || !container.Running {
				return nil, worktreePostgresPrecondition("the verified container did not start")
			}
		}
		observed := *p
		observed.Port = container.Port
		systemID, err := r.probe(ctx, &observed)
		if err != nil || systemID == "" {
			return nil, worktreePostgresPrecondition("authenticated cluster readiness failed; raw connection errors are omitted to protect credentials")
		}
		if systemID != p.SystemID {
			return nil, worktreePostgresPrecondition("authenticated PostgreSQL cluster identity differs from retained data")
		}
		if p.Port != observed.Port || p.Phase != "ready" {
			p.Port, p.Phase = observed.Port, "ready"
			if err := op.SaveRecord(record); err != nil {
				return nil, err
			}
		}
		return p, nil
	}
	if volume == nil {
		if err := r.docker.CreateVolume(ctx, record); err != nil {
			return nil, err
		}
		volume, container, err = r.inspect(ctx, record)
		if err != nil {
			return nil, err
		}
		if volume == nil {
			return nil, worktreePostgresPrecondition("volume creation was not confirmed; retry the retained pending operation")
		}
	}
	p.VolumeCreatedAt, p.VolumeDriver, p.VolumeMount = volume.CreatedAt, volume.Driver, volume.Mountpoint
	if p.Phase == "pending" {
		p.Phase = "volume"
	}
	if err := op.SaveRecord(record); err != nil {
		return nil, err
	}
	if container == nil {
		// A previously recorded container may be recreated only after the retained
		// volume was verified above. Persist the authorized recreation first.
		p.ContainerID, p.Port, p.Phase = "", 0, "volume"
		if err := op.SaveRecord(record); err != nil {
			return nil, err
		}
		if err := r.docker.CreateContainer(ctx, record); err != nil {
			return nil, err
		}
		_, container, err = r.inspect(ctx, record)
		if err != nil {
			return nil, err
		}
		if container == nil {
			return nil, worktreePostgresPrecondition("container creation was not confirmed; retry the retained pending operation")
		}
	}
	p.ContainerID, p.Phase = container.ID, "container"
	if err := op.SaveRecord(record); err != nil {
		return nil, err
	}
	if !container.Running {
		if err := r.docker.Start(ctx, record, container.ID); err != nil {
			return nil, err
		}
		_, container, err = r.inspect(ctx, record)
		if err != nil {
			return nil, err
		}
		if container == nil || !container.Running {
			return nil, worktreePostgresPrecondition("the verified container did not start")
		}
	}
	p.Port = container.Port
	if err := op.SaveRecord(record); err != nil {
		return nil, err
	}
	systemID, err := r.probe(ctx, p)
	if err != nil || systemID == "" {
		return nil, worktreePostgresPrecondition("authenticated cluster readiness failed; raw connection errors are omitted to protect credentials")
	}
	if p.SystemID != "" && p.SystemID != systemID {
		return nil, worktreePostgresPrecondition("authenticated PostgreSQL cluster identity differs from retained data")
	}
	p.SystemID, p.Phase = systemID, "ready"
	if err := op.SaveRecord(record); err != nil {
		return nil, err
	}
	return p, nil
}
