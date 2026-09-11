package main

import (
	"context"
	"encoding/json"
	"fmt"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// nativeDevPreparation is confined to the private lifecycle probe. Selection and
// accepted graph ownership survive edits, but each preparation gets a new verifier.
type nativeDevPreparation struct {
	workspace string
	project   func(*compiler.Result) (generateapi.GoWorkspaceProjection, error)
	accepted  *build.Result
}

type devBuildPreparation struct {
	root        string
	cfg         app.Config
	snapshot    fileSnapshot
	native      *nativeDevPreparation
	cached      *build.CachedGraph
	result      *build.Result
	metadata    json.RawMessage
	apiEncoding json.RawMessage
}

func loadDevBuildPreparation(ctx context.Context, root string, cfg app.Config, snapshot fileSnapshot, native *nativeDevPreparation) (*devBuildPreparation, error) {
	p := &devBuildPreparation{root: root, cfg: cfg, snapshot: snapshot, native: native}
	if native == nil {
		cached, _, err := build.LoadCachedGraphContext(ctx, root, cfg, snapshotFingerprint(snapshot))
		if err != nil {
			return nil, err
		}
		p.cached = cached
	} else if r := native.accepted; r != nil && r.GraphFingerprint == snapshotFingerprint(snapshot) {
		// This receipt is process-owned and never loaded from ordinary successful state.
		p.cached = &build.CachedGraph{Result: r, Metadata: r.Metadata, APIEncoding: r.APIEncoding}
	}
	if p.cached != nil {
		p.result = p.cached.Result
		p.metadata = append(json.RawMessage(nil), p.cached.Metadata...)
		p.apiEncoding = append(json.RawMessage(nil), p.cached.APIEncoding...)
	}
	return p, nil
}

func (p *devBuildPreparation) prepare(ctx context.Context) error {
	snapshot := buildSourceSnapshot(p.snapshot)
	if p.cached != nil && p.native == nil {
		reused, err := build.RefreshCachedWorkspaceWithSnapshotContext(ctx, p.root, p.result, snapshot)
		if err != nil {
			return err
		}
		if reused {
			return nil
		}
		p.metadata, p.apiEncoding = nil, nil
	}
	var err error
	if p.native != nil {
		if p.cached != nil {
			p.result, err = build.RefreshNativeExperiment(ctx, p.root, p.cfg, snapshot, p.native.workspace, p.native.project, p.cached.Result)
		} else {
			p.result, err = build.PrepareNativeExperiment(ctx, p.root, p.cfg, snapshot, p.native.workspace, p.native.project)
		}
	} else {
		p.result, err = build.PrepareForCompileWithSnapshotContext(ctx, p.root, p.cfg, snapshot)
	}
	if err != nil {
		return err
	}
	p.result.GraphFingerprint = snapshotFingerprint(p.snapshot)
	return nil
}

func (p *devBuildPreparation) analyze() error {
	if len(p.metadata) == 0 || len(p.apiEncoding) == 0 {
		var err error
		p.metadata, p.apiEncoding, err = buildDevMetadataFromResult(p.result.Contract)
		if err != nil {
			return err
		}
	}
	p.result.Metadata = append(json.RawMessage(nil), p.metadata...)
	p.result.APIEncoding = append(json.RawMessage(nil), p.apiEncoding...)
	return nil
}

// Compilation and the final watcher recapture share one admission boundary.
// The captured baseline is never replaced: a concurrent edit remains pending.
func (p *devBuildPreparation) compile(ctx context.Context, compile func(context.Context, *build.Result) error) error {
	if err := compile(ctx, p.result); err != nil {
		return err
	}
	current, err := scanWatchedFiles(p.root)
	if err != nil {
		return err
	}
	if snapshotFingerprint(current) != snapshotFingerprint(p.snapshot) {
		return fmt.Errorf("application source changed during compilation; edit remains pending")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.native != nil {
		p.native.accepted = p.result
	}
	return nil
}
