package main

import (
	"context"
	"encoding/json"

	"scenery.sh/internal/build"
	"scenery.sh/internal/devmeta"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/workers"
)

type devRuntimePlan struct {
	Result      *build.Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
	TypeScript  *workers.TypeScriptWorkerResult
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
		tsModel     workers.TypeScriptWorkerModel
		tsWorker    *workers.TypeScriptWorkerResult
		cached      *build.CachedGraph
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	if err := s.console.Phase("Building scenery application graph", func() error {
		cached, _, err = build.LoadCachedGraph(s.root, s.cfg, graphFingerprint)
		if err != nil {
			return err
		}
		if cached != nil {
			metadata = append(json.RawMessage(nil), cached.Metadata...)
			apiEncoding = append(json.RawMessage(nil), cached.APIEncoding...)
			result = cached.Result
			if !s.cfg.Temporal.Enabled && len(metadata) > 0 && len(apiEncoding) > 0 {
				return nil
			}
		}
		appModel, err = parse.App(s.root, s.cfg.Name)
		return err
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := s.console.Phase("Analyzing service topology", func() error {
		if cached != nil && len(metadata) > 0 && len(apiEncoding) > 0 {
			return nil
		}
		metadata, err = devmeta.BuildMetadataSnapshot(appModel)
		if err != nil {
			return err
		}
		apiEncoding, err = devmeta.BuildAPIEncoding(appModel)
		return err
	}); err != nil {
		return nil, devBuildError(nil, nil, err)
	}
	if err := validateLocalSecretsFiles(s.root); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if s.cfg.Temporal.Enabled {
		if err := s.console.Phase("Validating TypeScript Temporal workers", func() error {
			tsModel = workers.DiscoverTypeScriptActivities(s.root)
			if diagnostics := workers.ValidateTypeScriptContracts(tsModel, temporalExternalActivityDeclarations(s.root, appModel), nativeGoTemporalDeclarations(s.root, appModel)); len(diagnostics) > 0 {
				return workers.DiagnosticsError(diagnostics)
			}
			return nil
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
	}
	if appModel != nil {
		s.cfg = effectiveDevConfigForModel(s.cfg, appModel)
	}
	s.cfg = effectiveDevConfigForTypeScriptWorker(s.cfg, tsModel)
	if err := s.ensureManagedElectric(ctx); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.ensureTemporalDevServer(ctx); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			reused, refreshErr := build.RefreshCachedWorkspace(s.root, result)
			if refreshErr != nil {
				return refreshErr
			}
			if reused {
				return nil
			}
			appModel, err = parse.App(s.root, s.cfg.Name)
			if err != nil {
				return err
			}
			metadata, err = devmeta.BuildMetadataSnapshot(appModel)
			if err != nil {
				return err
			}
			apiEncoding, err = devmeta.BuildAPIEncoding(appModel)
			if err != nil {
				return err
			}
		}
		result, err = build.Prepare(s.root, appModel, s.cfg)
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
	if len(s.cfg.Dev.Setup) > 0 {
		if err := s.console.Phase("Running development setup", func() error {
			return s.runDevSetup(ctx)
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
	}
	if typeScriptWorkerAutoStartEnabled(s.cfg, tsModel) {
		if err := s.console.Phase("Generating TypeScript Temporal worker", func() error {
			generated, generateErr := s.generateTypeScriptTemporalWorker()
			if generateErr != nil {
				return generateErr
			}
			tsWorker = generated
			return nil
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
		if err := s.console.Phase("Installing app TypeScript dependencies", func() error {
			_, installErr := ensureTypeScriptWorkerAppDependencies(ctx, s.root, tsWorker.OutputDir)
			return installErr
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
		if err := s.console.Phase("Installing TypeScript worker dependencies", func() error {
			_, installErr := ensureTypeScriptWorkerDependencies(ctx, tsWorker.OutputDir)
			return installErr
		}); err != nil {
			return nil, devBuildError(metadata, apiEncoding, err)
		}
	}
	return &devRuntimePlan{
		Result:      result,
		Metadata:    metadata,
		APIEncoding: apiEncoding,
		TypeScript:  tsWorker,
		Initial:     initial,
	}, nil
}
