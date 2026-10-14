package main

import (
	"context"
	"fmt"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/lifecycle"
)

// compileSourcePlan is the read-only closure shared by source-derived commands.
// It has no renderer, external tool, publication or runtime dependency.
func compileSourcePlan(ctx context.Context, root string) (*compiler.Result, error) {
	var result *compiler.Result
	plan, err := lifecycle.New([]lifecycle.Node{
		{ID: "contract.compile", Kind: "contract", Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Outputs: []lifecycle.Reference{{Name: "contract", Kind: "contract", Ownership: lifecycle.Private}}, Action: func(ctx context.Context) error {
			var err error
			result, err = compiler.CompileContext(ctx, root)
			return err
		}},
		{ID: "source.validate", Kind: "source_validation", Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Dependencies: []lifecycle.ID{"contract.compile"}, Inputs: []lifecycle.Reference{{Name: "contract", Kind: "contract", Producer: "contract.compile"}}, Action: func(ctx context.Context) error {
			unchanged, err := compiler.SnapshotUnchanged(result)
			if err != nil {
				return err
			}
			if !unchanged {
				return fmt.Errorf("revision_conflict: application changed while capturing the source result; retry the command")
			}
			return nil
		}},
	})
	if err != nil {
		return nil, err
	}
	if err := plan.Run(ctx, []lifecycle.ID{"source.validate"}, []lifecycle.Capability{lifecycle.ReadSource}); err != nil {
		return result, err
	}
	return result, nil
}

// runLifecycleEffect admits the command's exact effect before delegating to its
// existing owner. External actions and current-state observations run every time.
func runLifecycleEffect(ctx context.Context, id lifecycle.ID, effects []lifecycle.Capability, action func(context.Context) error) error {
	plan, err := lifecycle.New([]lifecycle.Node{{ID: id, Kind: lifecycle.Kind(id), Cache: lifecycle.ExternalEffect, Effects: effects, Action: action}})
	if err != nil {
		return err
	}
	return plan.Run(ctx, []lifecycle.ID{id}, effects)
}
