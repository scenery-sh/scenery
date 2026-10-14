package generate

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/lifecycle"
	"scenery.sh/internal/machine"
	"scenery.sh/internal/workspacetx"
)

// GenerateWithContext captures once and preserves that result through every
// renderer, validation, publication and response adapter.
func GenerateWithContext(ctx context.Context, root, selector string, check bool) (GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	compiled, err := compiler.CompileContext(ctx, root)
	if err != nil {
		return GenerateResult{Compilation: compiled}, err
	}
	return GenerateSnapshotWithContext(ctx, compiled, selector, check)
}

// GenerationNodes exposes the same selected producers used by generation.
// Inspection describes the plan without rendering or starting a checker.
func GenerationNodes(result *compiler.Result, selector string, check bool) ([]lifecycle.Node, error) {
	op, err := newGenerationOperation(result, selector, check)
	if err != nil {
		return nil, err
	}
	nodes, err := op.plan(false)
	if err != nil {
		return nil, err
	}
	plan, err := lifecycle.New(nodes)
	if err != nil {
		return nil, err
	}
	return plan.Nodes(), nil
}

type generationOperation struct {
	compilation      *compiler.Result
	selector         string
	check            bool
	cachedOnly       bool
	goRenderer       artifactRenderer
	goFiles, tsFiles []generatedFile
	result           GenerateResult
	updates          []workspacetx.File
}

func newGenerationOperation(compiled *compiler.Result, selector string, check bool) (*generationOperation, error) {
	op := &generationOperation{compilation: compiled, selector: selector, check: check, result: GenerateResult{Changed: []string{}, Checked: []string{}, Compilation: compiled}}
	if compiled == nil || !compiled.Valid() || compiled.Manifest == nil {
		return op, fmt.Errorf("cannot generate from invalid contract")
	}
	if selector != "" && selector != "contracts" && !strings.HasPrefix(selector, "typescript_client.") {
		return op, fmt.Errorf("invalid_request: unknown generation target %q", selector)
	}
	if strings.HasPrefix(selector, "typescript_client.") && len(typescriptTargets(compiled.Manifest.Resources, selector)) == 0 {
		return op, fmt.Errorf("invalid_request: unknown generation target %q", selector)
	}
	return op, nil
}

func GenerateSnapshotWithContext(ctx context.Context, compiled *compiler.Result, selector string, check bool) (GenerateResult, error) {
	op, err := newGenerationOperation(compiled, selector, check)
	if err != nil {
		return op.result, err
	}
	return op.run(ctx, op.plan)
}

func (op *generationOperation) run(ctx context.Context, makePlan func(bool) ([]lifecycle.Node, error)) (GenerateResult, error) {
	compiled, check := op.compilation, op.check
	var err error
	allowed := []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache}
	if !check {
		allowed = append(allowed, lifecycle.PublishArtifacts)
	}
	run := func(owner bool) error {
		nodes, err := makePlan(owner)
		if err != nil {
			return err
		}
		plan, err := lifecycle.New(nodes)
		if err != nil {
			return err
		}
		target := lifecycle.ID("artifacts.compare")
		if owner {
			target = "artifacts.commit"
		}
		return plan.Run(ctx, []lifecycle.ID{target}, allowed)
	}
	// A no-op invocation needs current validation but no writing transaction.
	if err = run(false); err != nil || check || len(op.result.Changed) == 0 {
		return op.result, err
	}
	err = workspacetx.PublishContext(ctx, compiled.Root, func() ([]workspacetx.File, error) {
		if err := run(true); err != nil {
			return nil, err
		}
		return op.updates, nil
	})
	return op.result, err
}

func (op *generationOperation) plan(owner bool) ([]lifecycle.Node, error) {
	compiled := op.compilation
	identity := map[string]string{"workspace": compiled.WorkspaceRevision, "contract": compiled.Manifest.ContractRevision, "producer": machine.RuntimeProducer().Commit, "selection": op.selector}
	contract := lifecycle.Reference{Name: "contract", Kind: "contract", Ownership: lifecycle.Private}
	nodes := []lifecycle.Node{{ID: "contract.compile", Kind: "contract", Cache: lifecycle.CurrentValidation, Identity: identity, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Inputs: []lifecycle.Reference{{Name: "source", Kind: "source", Digest: compiled.WorkspaceRevision}}, Outputs: []lifecycle.Reference{contract}, Action: func(context.Context) error { return validateGenerationSnapshot(compiled, owner) }}}
	rendered := []lifecycle.Reference{}
	dependencies := []lifecycle.ID{}
	if !op.cachedOnly && (op.selector == "" || op.selector == "contracts") {
		output := lifecycle.Reference{Name: "go.files", Kind: "artifact_set", Ownership: lifecycle.Managed}
		nodes = append(nodes, lifecycle.Node{ID: "go.render", Kind: "go_render", Dependencies: []lifecycle.ID{"contract.compile"}, Inputs: []lifecycle.Reference{{Name: "contract", Kind: "contract", Producer: "contract.compile"}}, Outputs: []lifecycle.Reference{output}, Identity: identity, Cache: lifecycle.Reusable, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Action: func(context.Context) error {
			var err error
			render := op.goRenderer
			if render == nil {
				render = renderGoContractFiles
			}
			op.goFiles, err = render(compiled)
			return err
		}})
		output.Producer = "go.render"
		rendered = append(rendered, output)
		dependencies = append(dependencies, "go.render")
	}
	if op.selector != "contracts" {
		targets := typescriptTargets(compiled.Manifest.Resources, op.selector)
		if op.cachedOnly {
			targets = cacheTypeScriptTargets(targets)
		}
		outputs := []lifecycle.Reference{{Name: "typescript.files", Kind: "artifact_set", Ownership: lifecycle.Managed}}
		for _, target := range targets {
			root, err := typeScriptOutputRoot(compiled, target)
			if err != nil {
				return nil, err
			}
			relative, err := filepath.Rel(compiled.Root, root)
			if err != nil {
				return nil, err
			}
			ownership := lifecycle.Managed
			if strings.HasPrefix(filepath.ToSlash(relative), ".scenery/") {
				ownership = lifecycle.Private
			}
			outputs = append(outputs, lifecycle.Reference{Name: target.Address, Kind: "typescript_client", Path: filepath.ToSlash(relative), Ownership: ownership})
		}
		nodes = append(nodes, lifecycle.Node{ID: "typescript.render", Kind: "typescript_render", Dependencies: []lifecycle.ID{"contract.compile"}, Inputs: []lifecycle.Reference{{Name: "contract", Kind: "contract", Producer: "contract.compile"}}, Outputs: outputs, Identity: identity, Cache: lifecycle.Reusable, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Action: func(context.Context) error {
			var err error
			if op.cachedOnly {
				op.tsFiles, err = renderCachedTypeScriptClients(compiled)
			} else {
				op.tsFiles, err = renderTypeScriptClientFiles(compiled, op.selector)
			}
			return err
		}})
		rendered = append(rendered, lifecycle.Reference{Name: "typescript.files", Kind: "artifact_set", Producer: "typescript.render"})
		dependencies = append(dependencies, "typescript.render")
		nodes = append(nodes, lifecycle.Node{ID: "typescript.check", Kind: "typescript_check", Dependencies: []lifecycle.ID{"typescript.render"}, Inputs: []lifecycle.Reference{{Name: "typescript.files", Kind: "artifact_set", Producer: "typescript.render"}}, Identity: identity, Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache}, Action: func(ctx context.Context) error {
			return verifyRenderedTypeScriptReactContext(ctx, compiled, targets, op.tsFiles)
		}})
		dependencies = append(dependencies, "typescript.check")
	}
	nodes = append(nodes, lifecycle.Node{ID: "source.validate", Kind: "source_validation", Dependencies: slices.Clone(dependencies), Inputs: slices.Clone(rendered), Identity: identity, Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Action: func(context.Context) error { return validateGenerationSnapshot(compiled, owner) }})
	nodes = append(nodes, lifecycle.Node{ID: "artifacts.compare", Kind: "artifact_validation", Dependencies: append(slices.Clone(dependencies), "source.validate"), Inputs: rendered, Identity: identity, Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource}, Outputs: []lifecycle.Reference{{Name: "artifact.changes", Kind: "artifact_set", Ownership: lifecycle.Private}}, Action: func(context.Context) error {
		files := append(slices.Clone(op.goFiles), op.tsFiles...)
		generated, err := inspectGeneratedFiles(compiled.Root, files)
		generated.Compilation = compiled
		op.result = generated
		if err != nil {
			return err
		}
		if op.check && len(generated.Changed) > 0 {
			return fmt.Errorf("generated artifacts are stale: %s", strings.Join(generated.Changed, ", "))
		}
		return validateGenerationSnapshot(compiled, owner)
	}})
	if !op.check {
		nodes = append(nodes, lifecycle.Node{ID: "artifacts.commit", Kind: "artifact_publication", Dependencies: []lifecycle.ID{"artifacts.compare"}, Inputs: []lifecycle.Reference{{Name: "artifact.changes", Kind: "artifact_set", Producer: "artifacts.compare"}}, Identity: identity, Cache: lifecycle.ExternalEffect, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache, lifecycle.PublishArtifacts}, Action: func(context.Context) error {
			changed := map[string]bool{}
			for _, path := range op.result.Changed {
				changed[path] = true
			}
			var updates []generatedFile
			for _, file := range append(slices.Clone(op.goFiles), op.tsFiles...) {
				relative, err := filepath.Rel(compiled.Root, file.Path)
				if err != nil {
					return err
				}
				if changed[filepath.ToSlash(relative)] {
					updates = append(updates, file)
				}
			}
			var err error
			op.updates, err = transactionFiles(compiled.Root, updates)
			return err
		}})
	}
	return nodes, nil
}
