package main

import (
	"context"

	"scenery.sh/internal/compiler"
)

type postgresStartAttempt struct {
	cancel context.CancelFunc
	done   chan struct{}
	err    error
}

func (s *devSupervisor) beginRetainedPostgresStart(ctx context.Context, contract *compiler.Result) (*postgresStartAttempt, error) {
	if s.worktreeRootPaths == nil || contract == nil || !contract.Valid() || len(contract.SQLRequirements) == 0 {
		return nil, nil
	}
	base, err := appEnvWithDotEnv(s.processEnvironment(), s.root, s.env.DotEnvFiles()...)
	if err != nil {
		return nil, err
	}
	supply, err := resolveSQLSupply(contract.SQLRequirements, base, true)
	if err != nil {
		return nil, err
	}
	if len(supply) == 0 || lookupEnvValue(base, appDatabaseURLEnv) != "" {
		return nil, nil
	}
	root, appID, console := s.root, s.cfg.AppID(), s.console
	return beginPostgresStart(ctx, func(ctx context.Context) error {
		return console.Phase("Preparing retained PostgreSQL", func() error {
			// The source snapshot is immutable; build diagnostics belong to a
			// separate result. If fresh authority cannot be established (including
			// an active source transaction), leave all work to the ordinary build.
			unchanged, err := compiler.SnapshotUnchanged(contract)
			if err != nil || !unchanged {
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			resolver, err := newWorktreePostgresResolver(ctx, root, appID)
			if err != nil {
				return err
			}
			return resolver.startRetained(ctx)
		})
	}), nil
}

func beginPostgresStart(ctx context.Context, start func(context.Context) error) *postgresStartAttempt {
	ctx, cancel := context.WithCancel(ctx)
	attempt := &postgresStartAttempt{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(attempt.done)
		attempt.err = start(ctx)
	}()
	return attempt
}

func (a *postgresStartAttempt) wait() error {
	if a == nil {
		return nil
	}
	<-a.done
	return a.err
}

func (a *postgresStartAttempt) release() {
	if a != nil {
		a.cancel()
		<-a.done
	}
}
