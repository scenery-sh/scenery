package main

import (
	"context"
	"encoding/json"

	"scenery.sh/internal/build"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

type devRuntimePlan struct {
	Result      *build.Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
	Initial     bool
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
	if phaseErr, ok := err.(devBuildPhaseError); ok {
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
	var (
		appModel    *model.App
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		cached      *build.CachedGraph
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	sourceSnapshot := buildSourceSnapshot(snapshot)
	if err := s.console.Phase("Building scenery application graph", func() error {
		cached, _, err = build.LoadCachedGraph(s.root, s.cfg, graphFingerprint)
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
		appModel, err = parseDevApp(s.root, s.cfg.Name)
		return err
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		if len(metadata) > 0 && len(apiEncoding) > 0 {
			return nil
		}
		metadata, apiEncoding, err = buildDevMetadata(s.root)
		return err
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			reused, refreshErr := build.RefreshCachedWorkspaceWithSnapshot(s.root, result, sourceSnapshot)
			if refreshErr != nil {
				return refreshErr
			}
			if reused {
				return nil
			}
			if appModel == nil {
				appModel, err = parseDevApp(s.root, s.cfg.Name)
				if err != nil {
					return err
				}
			}
			metadata, apiEncoding, err = buildDevMetadata(s.root)
			if err != nil {
				return err
			}
		}
		result, err = build.PrepareWithSnapshot(s.root, appModel, s.cfg, sourceSnapshot)
		if err == nil && result != nil {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		return err
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
	s.setMetadata(metadata, apiEncoding)
	if err := s.persistStatus(ctx); err != nil {
		return nil, err
	}
	dbSetup, shouldRunDBSetup, err := s.nextDevDatabaseSetup(initial)
	if err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if shouldRunDBSetup {
		if err := s.console.Phase("Running database setup", func() error {
			return s.runDevDatabaseSetup(ctx, dbSetup)
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
	}
	return &devRuntimePlan{
		Result:      result,
		Metadata:    metadata,
		APIEncoding: apiEncoding,
		Initial:     initial,
	}, nil
}

func parseDevApp(root, name string) (*model.App, error) {
	return parse.Analyze(root, name)
}
