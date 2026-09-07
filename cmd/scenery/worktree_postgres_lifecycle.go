package main

import (
	"context"
	"errors"
	"os"

	localagent "scenery.sh/internal/agent"
)

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
