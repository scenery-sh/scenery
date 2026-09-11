package main

import (
	"context"
	"errors"
	"os"

	localagent "scenery.sh/internal/agent"
)

// startRetained only wakes a fully initialized owned server. Missing resources
// and interrupted provisioning stay with ordinary post-build preparation.
func (r worktreePostgresResolver) startRetained(ctx context.Context) error {
	op, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer func() { _ = op.Close() }()
	record, err := r.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	p := record.Postgres
	if p.Major != 18 {
		return worktreePostgresPrecondition("the retained PostgreSQL major requires an explicit validated engine migration")
	}
	if p.Phase != "ready" || p.SystemID == "" || p.Restore != nil {
		return nil
	}
	_, container, err := r.inspect(ctx, record)
	if err != nil || container == nil {
		return err
	}
	_, err = r.ensureRetainedEndpoint(ctx, op, record, container)
	return err
}

// The caller holds the operation lock and has verified Docker ownership.
// Authenticate retained data before persisting any endpoint reconciliation.
func (r worktreePostgresResolver) ensureRetainedEndpoint(ctx context.Context, op worktreePostgresOperation, record localagent.WorktreeRecord, container *worktreePostgresContainer) (*localagent.WorktreePostgres, error) {
	if !container.Running {
		if err := r.docker.Start(ctx, record, container.ID); err != nil {
			return nil, err
		}
		_, current, err := r.inspect(ctx, record)
		if err != nil {
			return nil, err
		}
		if current == nil || !current.Running {
			return nil, worktreePostgresPrecondition("the verified container did not start")
		}
		container = current
	}
	p := record.Postgres
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

// observe never acquires a creating lock or changes retained state or Docker.
func (r worktreePostgresResolver) observe(ctx context.Context) (*localagent.WorktreePostgres, bool, error) {
	record, err := r.load()
	if err != nil {
		return nil, false, err
	}
	_, container, err := r.inspect(ctx, record)
	if err != nil {
		return nil, false, err
	}
	if container == nil || !container.Running {
		return record.Postgres, false, nil
	}
	observed := *record.Postgres
	observed.Port = container.Port
	if observed.Restore != nil || observed.Phase == "restoring" || observed.Phase == "restore-failed" || observed.Phase == "deleting" {
		return nil, false, worktreePostgresPrecondition("a retained restore or deletion operation requires explicit recovery")
	}
	systemID, err := r.probe(ctx, &observed)
	if err != nil {
		return nil, false, err
	}
	if observed.SystemID == "" || systemID != observed.SystemID {
		return nil, false, worktreePostgresPrecondition("the authenticated database does not match the retained database identity")
	}
	return &observed, true, nil
}

// stop is used by the current supervisor after its application children exit,
// or by an operator holding the worktree live lock. It preserves all data.
func (r worktreePostgresResolver) stop(ctx context.Context) error {
	op, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer func() { _ = op.Close() }()
	record, err := r.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	_, container, err := r.inspect(ctx, record)
	if err != nil {
		return err
	}
	if container == nil || !container.Running {
		return nil
	}
	if err := r.docker.Stop(ctx, record, container.ID); err != nil {
		return err
	}
	return op.SaveRecord(record)
}

// remove requires the caller to hold the live lock and verify the explicit
// prune selection/age. It retires credentials only after confirmed deletion.
func (r worktreePostgresResolver) remove(ctx context.Context) error {
	op, err := r.beginOperation()
	if err != nil {
		return err
	}
	defer func() { _ = op.Close() }()
	record, err := r.load()
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	// Explicit whole-cluster deletion also permits abandoning an interrupted
	// restore. Both locks exclude a live restore; its marker and archive remain
	// available until exact container/volume absence has been confirmed.
	_, container, err := r.inspect(ctx, record)
	if err != nil {
		return err
	}
	record.Postgres.Phase = "deleting"
	if err := op.SaveRecord(record); err != nil {
		return err
	}
	if container != nil {
		if container.Running {
			if err := r.docker.Stop(ctx, record, container.ID); err != nil {
				return err
			}
		}
		if err := r.docker.RemoveContainer(ctx, record, container.ID); err != nil {
			return err
		}
	}
	volume, container, err := r.inspect(ctx, record)
	if err != nil {
		return err
	}
	if container != nil {
		return worktreePostgresPrecondition("container deletion was not confirmed; retry the retained deletion intent")
	}
	if volume != nil {
		if err := r.docker.RemoveVolume(ctx, record); err != nil {
			return err
		}
	}
	volume, container, err = r.inspect(ctx, record)
	if err != nil {
		return err
	}
	if volume != nil || container != nil {
		return worktreePostgresPrecondition("resource deletion was not confirmed; retained credentials remain available for recovery")
	}
	return op.RetirePostgres(record.Postgres.InstanceID)
}
