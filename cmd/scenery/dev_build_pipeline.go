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
		cached      *build.CachedGraph
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	sourceSnapshot := buildSourceSnapshot(snapshot)
	if err := s.console.Phase("Building scenery application graph", func() error {
		cached, _, err = build.LoadCachedGraphContext(ctx, s.root, s.cfg, graphFingerprint)
		if err != nil {
			return err
		}
		if cached != nil {
			metadata = append(json.RawMessage(nil), cached.Metadata...)
			apiEncoding = append(json.RawMessage(nil), cached.APIEncoding...)
			result = cached.Result
			if len(metadata) > 0 && len(apiEncoding) > 0 {
				return nil
			}
		}
		return nil
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root, s.cfg, s.env); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			reused, refreshErr := build.RefreshCachedWorkspaceWithSnapshotContext(ctx, s.root, result, sourceSnapshot)
			if refreshErr != nil {
				return refreshErr
			}
			if reused {
				return nil
			}
			metadata, apiEncoding = nil, nil
		}
		result, err = build.PrepareWithSnapshotContext(ctx, s.root, nil, s.cfg, sourceSnapshot)
		if err == nil && result != nil {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return err
	}); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		if len(metadata) == 0 || len(apiEncoding) == 0 {
			metadata, apiEncoding, err = buildDevMetadataFromResult(result.Contract)
			if err != nil {
				return err
			}
		}
		result.Metadata = append(json.RawMessage(nil), metadata...)
		result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		return nil
	}); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Compiling application source code", func() error {
		if result != nil && result.GraphFingerprint == "" {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return build.CompileContext(ctx, result)
	}); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if s.currentPID() == "" {
		s.setMetadata(metadata, apiEncoding)
	}
	if err := s.persistStatus(ctx); err != nil {
		return nil, err
	}
	dbSetup, shouldRunDBSetup, err := s.nextDevDatabaseSetup(initial, result.Contract)
	if err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	var environment *devRuntimeEnvironment
	if shouldRunDBSetup {
		if err := s.console.Phase("Running database setup", func() error {
			environment, err = s.prepareRuntimeEnvironment(ctx, result.Contract)
			if err != nil {
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
