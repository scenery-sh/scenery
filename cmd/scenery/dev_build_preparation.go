package main

import (
	"context"
	"encoding/json"
	"fmt"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
)

type devBuildPreparation struct {
	root        string
	cfg         app.Config
	snapshot    fileSnapshot
	cached      *build.CachedGraph
	result      *build.Result
	metadata    json.RawMessage
	apiEncoding json.RawMessage
}

func loadDevBuildPreparation(ctx context.Context, root string, cfg app.Config, snapshot fileSnapshot) (*devBuildPreparation, error) {
	p := &devBuildPreparation{root: root, cfg: cfg, snapshot: snapshot}
	cached, _, err := build.LoadCachedGraphContext(ctx, root, cfg, snapshotFingerprint(snapshot))
	if err != nil {
		return nil, err
	}
	p.cached = cached
	if cached != nil {
		p.result = cached.Result
		p.metadata = append(json.RawMessage(nil), cached.Metadata...)
		p.apiEncoding = append(json.RawMessage(nil), cached.APIEncoding...)
	}
	return p, nil
}

func (p *devBuildPreparation) prepare(ctx context.Context) error {
	snapshot := buildSourceSnapshot(p.snapshot)
	if p.cached != nil {
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
	p.result, err = build.PrepareForCompileWithSnapshotContext(ctx, p.root, p.cfg, snapshot)
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

// CompileContext may publish valid cache artifacts for the captured revision.
// This later check admits that revision to runtime preparation; it does not roll
// cache publication back. Never replace the baseline: a later edit stays pending.
func (p *devBuildPreparation) compile(ctx context.Context, compile func(context.Context, *build.Result) error) error {
	if err := compile(ctx, p.result); err != nil {
		return err
	}
	current, err := scanSourceAdmissionFiles(p.root)
	if err != nil {
		return err
	}
	if snapshotFingerprint(current) != snapshotFingerprint(p.snapshot) {
		return fmt.Errorf("application source changed during compilation; edit remains pending")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nil
}
