package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"scenery.sh/internal/build"
)

type devRuntimePlan struct {
	Result      *build.Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
	Initial     bool
	Environment *devRuntimeEnvironment
}

type devBuildPhaseError struct {
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
	Err         error
}

func (e devBuildPhaseError) Error() string {
	if e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e devBuildPhaseError) Unwrap() error {
	return e.Err
}

func devBuildErrorPayload(err error) (json.RawMessage, json.RawMessage) {
	var phaseErr devBuildPhaseError
	if errors.As(err, &phaseErr) {
		return phaseErr.Metadata, phaseErr.APIEncoding
	}
	return nil, nil
}

func devBuildError(metadata, apiEncoding json.RawMessage, err error) error {
	if err == nil {
		return nil
	}
	return devBuildPhaseError{Metadata: metadata, APIEncoding: apiEncoding, Err: err}
}

func (s *devSupervisor) prepareDevRuntimePlan(ctx context.Context, initial bool, snapshot fileSnapshot) (*devRuntimePlan, error) {
	ctx = build.WithTrace(ctx, func(step build.Step) {
		s.console.Event("build.step", map[string]any{
			"name": step.Name, "started_at": step.StartedAt.UTC().Format(time.RFC3339Nano),
			"duration_ms": float64(step.Duration.Microseconds()) / 1000,
			"cache":       step.Cache, "reason": step.Reason, "ok": step.OK,
		})
	})
	if err := s.console.Phase("Verifying framework source and producer", func() error { return build.VerifyFrameworkSession(ctx, s.root) }); err != nil {
		return nil, err
	}
	var (
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		err         error
	)
	var preparation *devBuildPreparation
	if err := s.console.Phase("Building scenery application graph", func() error {
		preparation, err = loadDevBuildPreparation(ctx, s.root, s.cfg, snapshot, nil)
		if err == nil {
			metadata, apiEncoding = preparation.metadata, preparation.apiEncoding
		}
		return err
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root, s.cfg, s.env); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	var postgresStart *postgresStartAttempt
	if initial {
		postgresStart, err = s.beginRetainedPostgresStart(ctx, snapshot.contract)
		if err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
		defer postgresStart.release()
	}
	if err := s.console.Phase("Generating boilerplate code", func() error { return preparation.prepare(ctx) }); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Analyzing service topology", preparation.analyze); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	result, metadata, apiEncoding = preparation.result, preparation.metadata, preparation.apiEncoding
	if err := s.console.Phase("Compiling application source code", func() error { return preparation.compile(ctx, build.CompileContext) }); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if s.currentPID() == "" {
		s.setMetadata(metadata, apiEncoding)
	}
	if err := s.persistStatus(ctx); err != nil {
		return nil, err
	}
	if err := postgresStart.wait(); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	dbSetup, shouldRunDBSetup, err := s.nextDevDatabaseSetup(initial, result.Contract)
	if err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	var environment *devRuntimeEnvironment
	if shouldRunDBSetup {
		if err := s.console.Phase("Running database setup", func() error {
			if err := s.console.Phase("Resolving database and storage capabilities", func() error {
				environment, err = s.prepareRuntimeEnvironment(ctx, result.Contract)
				return err
			}); err != nil {
				return err
			}
			return s.runDevDatabaseSetup(ctx, dbSetup, result.Contract, environment)
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
	}
	return &devRuntimePlan{
		Result:      result,
		Metadata:    metadata,
		APIEncoding: apiEncoding,
		Initial:     initial,
		Environment: environment,
	}, nil
}
