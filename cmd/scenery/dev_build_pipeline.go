package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync/atomic"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
)

var devBuildOperationSequence atomic.Uint64

func newDevBuildOperationID() string {
	return fmt.Sprintf("build-%x-%x", time.Now().UnixNano(), devBuildOperationSequence.Add(1))
}

func (s *devSupervisor) emitBuildStep(step build.Step) {
	fields := map[string]any{
		"operation_id": step.OperationID,
		"name":         step.Name, "started_at": step.StartedAt.UTC().Format(time.RFC3339Nano),
		"duration_ms": float64(step.Duration.Microseconds()) / 1000,
		"cache":       step.Cache, "reason": step.Reason, "ok": step.OK,
	}
	optionalInt := func(name string, value int) {
		if value != 0 {
			fields[name] = value
		}
	}
	optionalInt64 := func(name string, value int64) {
		if value != 0 {
			fields[name] = value
		}
	}
	optionalString := func(name, value string) {
		if value != "" {
			fields[name] = value
		}
	}
	if step.QueueDuration > 0 {
		fields["queue_ms"] = float64(step.QueueDuration.Microseconds()) / 1000
	}
	optionalInt("actions", step.Actions)
	optionalInt("cache_hits", step.CacheHits)
	optionalInt("cache_misses", step.CacheMisses)
	optionalInt("files_written", step.FilesWritten)
	optionalInt("files_removed", step.FilesRemoved)
	optionalInt64("bytes_written", step.BytesWritten)
	optionalInt("files_hashed", step.FilesHashed)
	optionalInt64("bytes_hashed", step.BytesHashed)
	optionalInt("files_reused", step.FilesReused)
	optionalInt64("bytes_reused", step.BytesReused)
	optionalInt64("executable_bytes", step.ExecutableBytes)
	if step.WrittenPaths != nil {
		fields["written_paths"] = step.WrittenPaths
	}
	if step.RemovedPaths != nil {
		fields["removed_paths"] = step.RemovedPaths
	}
	if step.PackagesRebuilt != nil {
		fields["packages_rebuilt"] = step.PackagesRebuilt
	}
	if step.Name == "go.command" && step.Reason == "build" {
		fields["packages_rebuilt_available"] = step.PackagesRebuiltAvailable
	}
	optionalString("snapshot_digest", step.SnapshotDigest)
	optionalString("contract_revision", step.ContractRevision)
	optionalString("implementation_revision", step.ImplementationRevision)
	optionalString("build_input_digest", step.BuildInputDigest)
	optionalString("framework_source_digest", step.FrameworkSourceDigest)
	optionalString("framework_executable_digest", step.FrameworkExecutableDigest)
	optionalString("go_target", step.GoTarget)
	if session := s.currentAgentSession(); session != nil {
		optionalString("session_id", session.SessionID)
		optionalString("app_root_hash", appRootHash(session.AppRoot))
		optionalString("owner_started_at", session.Owner.StartedAt)
		if session.OwnerPID != 0 {
			fields["owner_pid"] = session.OwnerPID
		}
	}
	s.console.Event("build.step", fields)
}

type devRuntimePlan struct {
	Result      *build.Result
	Metadata    json.RawMessage
	APIEncoding json.RawMessage
	Initial     bool
	Environment *devRuntimeEnvironment
	// Processes is the linked process set of a process-model session, which
	// replaces the application executable.
	Processes *build.DevelopmentProcessSet
	// Prepared holds the service process instances this build already retained
	// and preflighted while the supervisor verified the candidate.
	Prepared *devProcessPreparation
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

func (s *devSupervisor) prepareDevRuntimePlan(ctx context.Context, initial bool, snapshot fileSnapshot) (plan *devRuntimePlan, returnErr error) {
	frameworkStarted := time.Now()
	// The build binds the framework source this verification reads.
	frameworkErr := s.console.Phase("Verifying framework source and producer", func() error {
		verified, err := build.VerifyFrameworkSessionForBuild(ctx, s.root)
		ctx = verified
		return err
	})
	build.RecordStep(ctx, build.Step{Name: "framework.verify", StartedAt: frameworkStarted, Duration: time.Since(frameworkStarted), Cache: "not_applicable", Reason: "current_source_and_producer", OK: frameworkErr == nil})
	if frameworkErr != nil {
		return nil, frameworkErr
	}
	var (
		metadata    json.RawMessage
		apiEncoding json.RawMessage
		result      *build.Result
		cached      *build.CachedGraph
		contract    *compiler.Result
		err         error
	)
	graphFingerprint := snapshotFingerprint(snapshot)
	sourceSnapshot := buildSourceSnapshot(snapshot)
	if err := s.console.Phase("Building scenery application graph", func() error {
		contract, err = build.CompileContractWithSnapshotContext(ctx, s.root, sourceSnapshot)
		if err != nil {
			return err
		}
		if !contract.Valid() {
			return nil
		}
		preparationFingerprint, fingerprintErr := build.PreparationFingerprint(s.cfg, contract)
		if fingerprintErr != nil {
			return fingerprintErr
		}
		cached, _, err = build.LoadCachedPreparationContext(ctx, s.root, s.cfg, graphFingerprint, preparationFingerprint, contract)
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
	// Every preparation step consumes the captured bytes. The supervisor checks
	// the complete authored tree once, immediately before predecessor retirement,
	// so a concurrently superseded generation may finish private work but can
	// never become the running application.
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
	if err := s.console.Phase("Generating boilerplate code", func() error {
		if cached != nil {
			prepared, refreshErr := build.PrepareCachedWorkspaceWithSnapshotContext(ctx, s.root, s.cfg, result, sourceSnapshot)
			if refreshErr != nil {
				return refreshErr
			}
			if prepared {
				return nil
			}
			metadata, apiEncoding = nil, nil
		}
		result, err = build.PrepareForCompileWithContractSnapshotContext(ctx, s.root, s.cfg, sourceSnapshot, contract)
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
	// A rebuild resolves its runtime capabilities while Go compiles; they depend
	// on configuration and SQL requirements, not on the linked executable. A
	// candidate whose checked contract changed its SQL requirements resolves again.
	type resolvedEnvironment struct {
		environment *devRuntimeEnvironment
		sql         compiler.SQLRequirements
		err         error
	}
	var concurrentEnvironment chan resolvedEnvironment
	if !initial && result != nil && result.Contract != nil {
		concurrentEnvironment = make(chan resolvedEnvironment, 1)
		contract := result.Contract
		go func() {
			started := time.Now()
			environment, err := s.prepareRuntimeEnvironment(ctx, contract)
			build.RecordStep(ctx, build.Step{Name: "supervisor.environment", StartedAt: started, Duration: time.Since(started), Cache: "not_applicable", Reason: "runtime_capabilities_during_compile", OK: err == nil})
			concurrentEnvironment <- resolvedEnvironment{environment: environment, sql: contract.SQLRequirements, err: err}
		}()
	}
	var processes *build.DevelopmentProcessSet
	joinImplementationCheck := func() error { return nil }
	if err := s.console.Phase("Compiling application source code", func() error {
		if result != nil && result.GraphFingerprint == "" {
			result.GraphFingerprint = graphFingerprint
			result.Metadata = append(json.RawMessage(nil), metadata...)
			result.APIEncoding = append(json.RawMessage(nil), apiEncoding...)
		}
		var buildErr error
		processes, joinImplementationCheck, buildErr = build.BuildDevelopmentProcessesContext(ctx, result)
		if buildErr != nil {
			joinImplementationCheck = func() error { return nil }
		}
		return buildErr
	}); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	var environment *devRuntimeEnvironment
	if concurrentEnvironment != nil {
		if resolved := <-concurrentEnvironment; resolved.err == nil && reflect.DeepEqual(resolved.sql, result.Contract.SQLRequirements) {
			environment = resolved.environment
		}
	}
	// Replacement instances are retained and preflighted while the supervisor
	// verifies the candidate below, so activation only starts them.
	// The implementation check runs beside the build; replacement instances are
	// retained and preflighted while it finishes.
	var prepared *devProcessPreparation
	if processes != nil && !initial && environment != nil {
		prepared = s.beginDevProcessPreparation(ctx, result, environment, processes)
		defer func() {
			if returnErr != nil {
				prepared.release(s)
			}
		}()
	}
	if err := joinImplementationCheck(); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	identityStep := build.Step{
		Name: "build.identity", StartedAt: time.Now(), Cache: "not_applicable", Reason: "exact_candidate_inputs", OK: true,
		SnapshotDigest: graphFingerprint, FrameworkSourceDigest: result.FrameworkSourceDigest,
	}
	if result.Contract != nil && result.Contract.Manifest != nil {
		identityStep.ContractRevision = result.Contract.Manifest.ContractRevision
	}
	if result.Target != nil {
		identityStep.GoTarget = result.Target.Name
		identityStep.ImplementationRevision = result.ImplementationRevisions[result.Target.Name]
	}
	if result.BuildInput != nil {
		identityStep.BuildInputDigest = result.BuildInput.Digest
	}
	if selection, selectionErr := build.ReadFrameworkSelection(s.root); selectionErr == nil {
		identityStep.FrameworkExecutableDigest = selection.ExecutableDigest
		if identityStep.FrameworkSourceDigest == "" {
			identityStep.FrameworkSourceDigest = selection.Source.Digest
		}
	}
	build.RecordStep(ctx, identityStep)
	if s.currentPID() == "" {
		s.setMetadata(metadata, apiEncoding)
	}
	statusStarted := time.Now()
	statusErr := s.persistStatus(ctx)
	build.RecordStep(ctx, build.Step{Name: "supervisor.status", StartedAt: statusStarted, Duration: time.Since(statusStarted), Cache: "not_applicable", Reason: "compiled_candidate_status", OK: statusErr == nil})
	if statusErr != nil {
		return nil, statusErr
	}
	if err := postgresStart.wait(); err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	setupStarted := time.Now()
	dbSetup, shouldRunDBSetup, err := s.nextDevDatabaseSetup(initial, result.Contract)
	build.RecordStep(ctx, build.Step{Name: "supervisor.database_setup_check", StartedAt: setupStarted, Duration: time.Since(setupStarted), Cache: "not_applicable", Reason: "migration_and_seed_inputs", OK: err == nil})
	if err != nil {
		return nil, devBuildError(metadata, apiEncoding, err)
	}
	if shouldRunDBSetup {
		if err := s.console.Phase("Running database setup", func() error {
			if err := s.console.Phase("Resolving database and storage capabilities", func() error {
				if environment != nil {
					return nil
				}
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
	plan = &devRuntimePlan{
		Result:      result,
		Metadata:    metadata,
		APIEncoding: apiEncoding,
		Initial:     initial,
		Environment: environment,
		Processes:   processes,
		Prepared:    prepared,
	}
	return plan, nil
}
