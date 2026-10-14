package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/lifecycle"
	"scenery.sh/internal/scn"
	"scenery.sh/internal/workspacetx"
)

const sqlcReceiptPath = compiler.SQLCReceiptPath

type sqlcArtifactReceipt = compiler.SQLCArtifactReceipt

type sqlcOperation struct {
	root              string
	spec              *sqlcGeneratorPlan
	config            sqlcConfigFile
	contract          *compiler.Result
	hooks             lifecycleHooks
	inputs            *compiler.InputCapture
	selections        []compiler.InputSelection
	excluded          map[string]bool
	stage             string
	tools             map[string]string
	files             map[string][]byte
	previous          sqlcArtifactReceipt
	stdout            io.Writer
	quiet             bool
	publishedContract *compiler.Result
}

func defaultLifecycleToolIdentity(program string) (string, error) {
	path, err := exec.LookPath(program)
	if err != nil {
		return "", fmt.Errorf("capability_unavailable: lifecycle tool %s is unavailable", program)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sqlcDigest(data), nil
}

func sqlcDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func sqlcBeforeDigest(data []byte) string {
	if data == nil {
		return workspacetx.AbsentDigest
	}
	return sqlcDigest(data)
}

func sqlcOutputRoots(config sqlcConfigFile) []string {
	var roots []string
	for _, block := range config.SQL {
		if block.Gen.Go.Out != "" {
			roots = append(roots, filepath.ToSlash(filepath.Clean(block.Gen.Go.Out)))
		}
		if block.Gen.JSON.Out != "" {
			roots = append(roots, filepath.ToSlash(filepath.Clean(block.Gen.JSON.Out)))
		}
		for _, plugin := range block.Codegen {
			if plugin.Out != "" {
				roots = append(roots, filepath.ToSlash(filepath.Clean(plugin.Out)))
			}
		}
	}
	return uniqueSorted(roots)
}

func selectedSQLCPlugins(config sqlcConfigFile) []sqlcPlugin {
	selected := map[string]bool{}
	for _, block := range config.SQL {
		for _, codegen := range block.Codegen {
			selected[codegen.Plugin] = true
		}
	}
	var plugins []sqlcPlugin
	for _, plugin := range config.Plugins {
		if selected[plugin.Name] {
			plugins = append(plugins, plugin)
		}
	}
	return plugins
}

func newSQLCOperation(root string, spec *sqlcGeneratorPlan, hooks lifecycleHooks, stdout io.Writer, quiet bool) (*sqlcOperation, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	contract := spec.Contract
	if contract == nil {
		contract, err = compiler.Compile(root)
		if err != nil {
			return nil, err
		}
		spec.Contract = contract
	}
	if !contract.Valid() {
		return nil, fmt.Errorf("failed_precondition: current application contract is invalid")
	}
	configPath := filepath.Join(root, filepath.FromSlash(firstNonEmpty(spec.ConfigPath, "sqlc.yaml")))
	config, data, err := readSQLCConfigBytes(configPath)
	if err != nil {
		return nil, err
	}
	resolveSQLCConfigReferences(&config, firstNonEmpty(spec.ConfigPath, "sqlc.yaml"))
	if len(spec.ConfigBytes) > 0 && !bytes.Equal(data, spec.ConfigBytes) {
		return nil, fmt.Errorf("revision_conflict: SQLC configuration changed after planning; retry generation")
	}
	if len(spec.ConfigBytes) == 0 {
		spec.ConfigBytes = bytes.Clone(data)
	}
	spec.Config = config
	if err := validateSQLCOutputRoots(contract, config, spec); err != nil {
		return nil, err
	}
	op := &sqlcOperation{root: root, spec: spec, config: config, contract: contract, hooks: hooks.withDefaults(), tools: map[string]string{}, files: map[string][]byte{}, excluded: map[string]bool{}, stdout: stdout, quiet: quiet}
	previous, err := compiler.ReadSQLCArtifactReceipt(root)
	if err != nil {
		return nil, err
	}
	op.previous = previous
	for _, schema := range spec.Schemas {
		if schema.AtlasSource != "" {
			if strings.TrimSpace(schema.AtlasDevURL) == "" {
				return nil, fmt.Errorf("failed_precondition: generators.sqlc schema %s has no scratch dev_url", schema.SQLCSchema)
			}
		}
	}
	return op, nil
}

func safeSQLCRelative(path string) bool {
	return path != "" && path != "." && !filepath.IsAbs(path) && filepath.ToSlash(filepath.Clean(path)) == path && path != ".." && !strings.HasPrefix(path, "../") && !strings.HasPrefix(path, ".scenery/") && !strings.Contains(path, "\\")
}
func sqlcPathInRoots(path string, roots []string) bool {
	for _, root := range roots {
		if path == root || strings.HasPrefix(path, root+"/") {
			return true
		}
	}
	return false
}

func (op *sqlcOperation) capture(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot, err := readSQLCInputSnapshot(op.root, op.spec)
	if err != nil {
		return err
	}
	inputs := snapshot.capture
	if op.spec.InputRevision != "" && op.spec.InputRevision != inputs.Digest {
		return fmt.Errorf("revision_conflict: SQLC inputs changed after planning; retry generation")
	}
	op.selections, op.excluded = snapshot.selections, snapshot.excluded
	op.inputs = inputs
	op.spec.InputSnapshot = snapshot
	op.spec.InputRevision = inputs.Digest
	configPath := filepath.ToSlash(filepath.Clean(firstNonEmpty(op.spec.ConfigPath, "sqlc.yaml")))
	if len(op.spec.ConfigBytes) > 0 && !bytes.Equal(inputs.Files[configPath], op.spec.ConfigBytes) {
		return fmt.Errorf("revision_conflict: SQLC configuration changed after planning; retry generation")
	}
	data, err := os.ReadFile(filepath.Join(op.root, filepath.FromSlash(configPath)))
	if err != nil {
		return err
	}
	if !bytes.Equal(data, inputs.Files[configPath]) {
		return fmt.Errorf("revision_conflict: SQLC configuration changed while capturing inputs")
	}
	programs := []string{"sqlc"}
	for _, schema := range op.spec.Schemas {
		if schema.AtlasSource != "" {
			programs = append(programs, "atlas")
			break
		}
	}
	for _, plugin := range selectedSQLCPlugins(op.config) {
		if plugin.Process.Cmd != "" {
			programs = append(programs, plugin.Process.Cmd)
		}
	}
	for _, program := range uniqueSorted(programs) {
		digest, err := op.hooks.toolIdentity(program)
		if err != nil {
			return err
		}
		op.tools[program] = digest
	}
	stageRoot := filepath.Join(op.root, ".scenery", "staging")
	if err := scn.RejectPathSymlinks(op.root, stageRoot); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(stageRoot, 0o700); err != nil {
		return err
	}
	op.stage, err = os.MkdirTemp(stageRoot, "sqlc-")
	if err != nil {
		return err
	}
	for _, directory := range inputs.Directories {
		if err := os.MkdirAll(filepath.Join(op.stage, filepath.FromSlash(directory)), 0o755); err != nil {
			return err
		}
	}
	for relative, data := range inputs.Files {
		if err := op.writeStage(relative, data); err != nil {
			return err
		}
	}
	return op.verify(false)
}

func (op *sqlcOperation) writeStage(relative string, data []byte) error {
	if !safeSQLCRelative(filepath.ToSlash(filepath.Clean(relative))) {
		return fmt.Errorf("invalid_request: unsafe SQLC stage path %s", relative)
	}
	path := filepath.Join(op.stage, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func (op *sqlcOperation) renderSchema(ctx context.Context, schema sqlcSchemaPlan) error {
	if !safeSQLCRelative(schema.SQLCSchema) || !safeSQLCRelative(schema.AtlasSource) {
		return fmt.Errorf("invalid_request: SQLC schema paths must stay in the workspace")
	}
	if _, owned := op.previous.Files[schema.SQLCSchema]; !owned {
		if _, err := os.Lstat(filepath.Join(op.root, filepath.FromSlash(schema.SQLCSchema))); err == nil {
			return fmt.Errorf("failed_precondition: SQLC schema %s has no proven generated ownership", schema.SQLCSchema)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	out, err := op.hooks.outputExec(ctx, lifecycleExecRequest{Dir: op.stage, Env: envpolicy.Environ(), Program: "atlas", Args: []string{"schema", "inspect", "--url", "file://" + filepath.ToSlash(filepath.Join(op.stage, filepath.FromSlash(schema.AtlasSource))), "--dev-url", schema.AtlasDevURL, "--format", "{{ sql . }}"}})
	if err != nil {
		return err
	}
	data := append([]byte("-- GENERATED: do not edit. Run `scenery generate sqlc` to refresh.\n\n"), out...)
	if data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	op.files[schema.SQLCSchema] = data
	return op.writeStage(schema.SQLCSchema, data)
}

func (op *sqlcOperation) renderSQLC(ctx context.Context) error {
	args := []string{"generate"}
	if op.spec.ConfigPath != "" && op.spec.ConfigPath != "sqlc.yaml" {
		args = append(args, "-f", op.spec.ConfigPath)
	}
	if err := op.hooks.runExec(ctx, lifecycleExecRequest{Dir: op.stage, Env: envpolicy.Environ(), Program: "sqlc", Args: args, Stdout: os.Stderr, Stderr: os.Stderr}); err != nil {
		return err
	}
	// SQLC may not mutate captured authored inputs or its schema producer's bytes.
	for relative, expected := range op.inputs.Files {
		data, err := os.ReadFile(filepath.Join(op.stage, filepath.FromSlash(relative)))
		if err != nil || !bytes.Equal(data, expected) {
			return fmt.Errorf("failed_precondition: SQLC changed a captured input %s", relative)
		}
	}
	for relative, expected := range op.files {
		data, err := os.ReadFile(filepath.Join(op.stage, filepath.FromSlash(relative)))
		if err != nil || !bytes.Equal(data, expected) {
			return fmt.Errorf("failed_precondition: SQLC changed a rendered schema %s", relative)
		}
	}
	var selections []compiler.InputSelection
	for _, root := range sqlcOutputRoots(op.config) {
		selections = append(selections, compiler.InputSelection{Path: root})
	}
	excluded := map[string]bool{}
	for path := range op.inputs.Files {
		excluded[path] = true
	}
	for path := range op.files {
		excluded[path] = true
	}
	outputs, err := compiler.CaptureInputPaths(op.stage, selections, excluded)
	if err != nil {
		return err
	}
	for _, selection := range selections {
		produced := false
		for path := range outputs.Files {
			if sqlcPathInRoots(path, []string{selection.Path}) {
				produced = true
				break
			}
		}
		if !produced {
			return fmt.Errorf("failed_precondition: SQLC produced no code output in declared root %s", selection.Path)
		}
	}
	for path, data := range outputs.Files {
		op.files[path] = data
	}
	return nil
}

func (op *sqlcOperation) verify(owner bool) error {
	verify := compiler.SnapshotUnchanged
	if owner {
		verify = compiler.SnapshotUnchangedDuringChangeTransaction
	}
	unchanged, err := verify(op.contract)
	if err != nil {
		return err
	}
	if !unchanged {
		return fmt.Errorf("revision_conflict: application changed during SQLC generation; retry generation")
	}
	current, err := compiler.CaptureInputPaths(op.root, op.selections, op.excluded)
	if err != nil {
		return err
	}
	if current.Digest != op.inputs.Digest {
		return fmt.Errorf("revision_conflict: SQLC inputs changed during generation; retry generation")
	}
	for program, digest := range op.tools {
		current, err := op.hooks.toolIdentity(program)
		if err != nil {
			return err
		}
		if current != digest {
			return fmt.Errorf("revision_conflict: lifecycle tool %s changed during generation", program)
		}
	}
	return nil
}

func (op *sqlcOperation) publish(ctx context.Context) error {
	err := workspacetx.PublishContext(ctx, op.root, op.publicationFiles)
	if err == nil {
		op.spec.Contract = op.publishedContract
	}
	return err
}

// publicationFiles verifies ownership under the transaction owner and binds
// every published or retired output to the exact bytes just verified.
func (op *sqlcOperation) publicationFiles() ([]workspacetx.File, error) {
	managed, err := compiler.ManagedGeneratedRoots(op.contract)
	if err != nil {
		return nil, err
	}
	roots := sqlcOutputRoots(op.config)
	for _, schema := range op.spec.Schemas {
		if schema.AtlasSource != "" {
			roots = append(roots, filepath.ToSlash(filepath.Dir(schema.SQLCSchema)))
		}
	}
	roots = uniqueSorted(roots)
	if err := op.verify(true); err != nil {
		return nil, err
	}
	current, err := compiler.ReadSQLCArtifactReceipt(op.root)
	if err != nil {
		return nil, err
	}
	original, _ := json.Marshal(op.previous)
	observed, _ := json.Marshal(current)
	if !bytes.Equal(original, observed) {
		return nil, fmt.Errorf("revision_conflict: SQLC output ownership changed during generation; retry generation")
	}
	known, err := compiler.GeneratedPaths(op.root)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(op.files))
	for path := range op.files {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	receipt := sqlcArtifactReceipt{Kind: "scenery.sqlc-generated", SchemaRevision: compiler.SQLCReceiptSchemaRevision(), Owner: op.root, InputRevision: op.inputs.Digest, Tools: op.tools, Roots: roots, Files: map[string]string{}}
	var changes []workspacetx.File
	for _, relative := range paths {
		if !safeSQLCRelative(relative) || !sqlcPathInRoots(relative, roots) {
			return nil, fmt.Errorf("failed_precondition: unsafe SQLC output %s", relative)
		}
		data := op.files[relative]
		before, err := readSQLCOwnedPath(op.root, relative)
		if err != nil {
			return nil, err
		}
		old, owned := current.Files[relative]
		if known[relative] && !owned {
			return nil, fmt.Errorf("failed_precondition: SQLC output %s belongs to another producer", relative)
		}
		if before != nil && !bytes.Equal(before, data) && (!owned || sqlcDigest(before) != old) {
			return nil, fmt.Errorf("failed_precondition: SQLC output %s has no matching ownership digest", relative)
		}
		receipt.Files[relative] = sqlcDigest(data)
		changes = append(changes, workspacetx.File{Path: relative, Bytes: data, ExpectedBeforeDigest: sqlcBeforeDigest(before)})
	}
	retired := make([]string, 0)
	for relative := range current.Files {
		if _, keep := op.files[relative]; !keep && sqlcPathInRoots(relative, managed) {
			retired = append(retired, relative)
		}
	}
	slices.Sort(retired)
	for _, relative := range retired {
		before, err := readSQLCOwnedPath(op.root, relative)
		if err != nil {
			return nil, err
		}
		if before == nil {
			continue
		}
		if sqlcDigest(before) != current.Files[relative] {
			return nil, fmt.Errorf("failed_precondition: retired SQLC output %s was modified", relative)
		}
		changes = append(changes, workspacetx.File{Path: relative, Remove: true, ExpectedBeforeDigest: sqlcDigest(before)})
	}
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	beforeReceipt, err := readSQLCOwnedPath(op.root, sqlcReceiptPath)
	if err != nil {
		return nil, err
	}
	changes = append(changes, workspacetx.File{Path: sqlcReceiptPath, Bytes: data, ExpectedBeforeDigest: sqlcBeforeDigest(beforeReceipt)})
	// Capture the response revision under the publication owner, using the
	// membership this receipt establishes. Newly adopted identical outputs
	// must not remain authored inputs in the post-publication snapshot.
	for path := range receipt.Files {
		known[path] = true
	}
	inputs, err := compiler.WorkspaceRevisionInputsWithGenerated(op.contract, known)
	if err != nil {
		return nil, err
	}
	captured := make(map[string][]byte, len(inputs))
	for _, input := range inputs {
		if !input.Present {
			continue
		}
		contents, err := os.ReadFile(filepath.Join(op.root, filepath.FromSlash(input.Path)))
		if err != nil {
			return nil, err
		}
		captured[input.Path] = contents
	}
	updated := *op.contract
	if err := compiler.BindCapturedWorkspaceRevision(&updated, captured); err != nil {
		return nil, err
	}
	// Reject edits during reclassification rather than assigning their bytes
	// to the already-rendered source snapshot.
	if err := op.verify(true); err != nil {
		return nil, err
	}
	op.publishedContract = &updated
	return changes, nil
}

func readSQLCOwnedPath(root, relative string) ([]byte, error) {
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := scn.RejectPathSymlinks(root, path); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("failed_precondition: SQLC output is not regular: %s", relative)
	}
	return os.ReadFile(path)
}

func sqlcLifecycleNodes(spec *sqlcGeneratorPlan, op *sqlcOperation) []lifecycle.Node {
	inputs := lifecycle.Reference{Name: "sqlc.inputs", Kind: "input_set", Ownership: lifecycle.Private, Digest: spec.InputRevision}
	config := lifecycle.Reference{Name: "sqlc.config", Kind: "source", Path: filepath.ToSlash(filepath.Clean(firstNonEmpty(spec.ConfigPath, "sqlc.yaml"))), Digest: sqlcDigest(spec.ConfigBytes)}
	nodes := []lifecycle.Node{{ID: "sqlc.source.capture", Kind: "source_capture", Cache: lifecycle.CurrentValidation, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache}, Inputs: []lifecycle.Reference{config}, Outputs: []lifecycle.Reference{inputs}}}
	dependencies := []lifecycle.ID{"sqlc.source.capture"}
	refs := []lifecycle.Reference{{Name: "sqlc.inputs", Kind: "input_set", Producer: "sqlc.source.capture"}}
	// Output roots are authored destination scopes for the produced file set,
	// not overlapping claims against the schema producer's individual files.
	for index, output := range spec.Record.Outputs {
		refs = append(refs, lifecycle.Reference{Name: fmt.Sprintf("sqlc.files.root.%d", index), Kind: "output_root", Path: output, Ownership: lifecycle.Managed})
	}
	for index, schema := range spec.Schemas {
		if schema.AtlasSource == "" {
			continue
		}
		id := lifecycle.ID(fmt.Sprintf("sqlc.schema.render.%d", index))
		output := lifecycle.Reference{Name: string(id), Kind: "sql_schema", Path: filepath.ToSlash(filepath.Clean(schema.SQLCSchema)), Ownership: lifecycle.Managed}
		node := lifecycle.Node{ID: id, Kind: "schema_render", Cache: lifecycle.ExternalEffect, Dependencies: []lifecycle.ID{"sqlc.source.capture"}, Inputs: []lifecycle.Reference{{Name: "sqlc.inputs", Kind: "input_set", Producer: "sqlc.source.capture"}}, Outputs: []lifecycle.Reference{output}, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache, lifecycle.UseScratch}, Identity: map[string]string{"tool": "atlas"}}
		if op != nil {
			node.Action = func(ctx context.Context) error { return op.renderSchema(ctx, schema) }
		}
		nodes = append(nodes, node)
		dependencies = append(dependencies, id)
		output.Producer = id
		refs = append(refs, output)
	}
	nodes = append(nodes, lifecycle.Node{ID: "sqlc", Kind: "sqlc", Cache: lifecycle.ExternalEffect, Dependencies: dependencies, Inputs: refs, Outputs: []lifecycle.Reference{{Name: "sqlc.files", Kind: "artifact_set", Ownership: lifecycle.Managed}}, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache, lifecycle.UseScratch}, Identity: map[string]string{"tool": "sqlc"}})
	nodes = append(nodes, lifecycle.Node{ID: "sqlc.source.validate", Kind: "source_validation", Cache: lifecycle.CurrentValidation, Dependencies: []lifecycle.ID{"sqlc"}, Inputs: []lifecycle.Reference{{Name: "sqlc.files", Kind: "artifact_set", Producer: "sqlc"}}, Effects: []lifecycle.Capability{lifecycle.ReadSource}})
	nodes = append(nodes, lifecycle.Node{ID: "sqlc.artifacts.commit", Kind: "artifact_publication", Cache: lifecycle.ExternalEffect, Dependencies: []lifecycle.ID{"sqlc.source.validate"}, Effects: []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache, lifecycle.PublishArtifacts}})
	if op != nil {
		nodes[0].Action = op.capture
		nodes[len(nodes)-3].Action = op.renderSQLC
		nodes[len(nodes)-2].Action = func(context.Context) error { return op.verify(false) }
		nodes[len(nodes)-1].Action = op.publish
	}
	return nodes
}

func runSQLCLifecycle(ctx context.Context, stdout io.Writer, root string, spec *sqlcGeneratorPlan, quiet bool, hooks lifecycleHooks) (returnErr error) {
	op, err := newSQLCOperation(root, spec, hooks, stdout, quiet)
	if err != nil {
		return err
	}
	defer func() {
		if op.stage != "" {
			returnErr = errors.Join(returnErr, os.RemoveAll(op.stage))
		}
	}()
	plan, err := lifecycle.New(sqlcLifecycleNodes(spec, op))
	if err != nil {
		return err
	}
	if err := plan.Run(ctx, []lifecycle.ID{"sqlc.artifacts.commit"}, []lifecycle.Capability{lifecycle.ReadSource, lifecycle.WriteCache, lifecycle.UseScratch, lifecycle.PublishArtifacts}); err != nil {
		return err
	}
	if !quiet {
		_, err = fmt.Fprintln(stdout, "scenery: generated SQLC artifacts")
	}
	return err
}

func validateSQLCOutputRoots(contract *compiler.Result, config sqlcConfigFile, spec *sqlcGeneratorPlan) error {
	managed, err := compiler.ManagedGeneratedRoots(contract)
	if err != nil {
		return err
	}
	roots := sqlcOutputRoots(config)
	for _, schema := range spec.Schemas {
		if schema.AtlasSource != "" {
			roots = append(roots, filepath.ToSlash(filepath.Dir(schema.SQLCSchema)))
		}
	}
	for _, root := range uniqueSorted(roots) {
		if !safeSQLCRelative(root) || !sqlcPathInRoots(root, managed) {
			return fmt.Errorf("failed_precondition: SQLC output %s is outside declared managed_generated_roots", root)
		}
	}
	return nil
}

func resolveSQLCConfigReferences(config *sqlcConfigFile, configPath string) {
	base := filepath.Dir(configPath)
	resolve := func(path string) string {
		if path == "" || filepath.IsAbs(path) {
			return path
		}
		return filepath.ToSlash(filepath.Clean(filepath.Join(base, path)))
	}
	for i := range config.SQL {
		block := &config.SQL[i]
		for j := range block.Schema.Values {
			block.Schema.Values[j] = resolve(block.Schema.Values[j])
		}
		for j := range block.Queries.Values {
			block.Queries.Values[j] = resolve(block.Queries.Values[j])
		}
		block.Gen.Go.Out = resolve(block.Gen.Go.Out)
		block.Gen.JSON.Out = resolve(block.Gen.JSON.Out)
		for j := range block.Codegen {
			block.Codegen[j].Out = resolve(block.Codegen[j].Out)
		}
	}
	for i := range config.Plugins {
		plugin := &config.Plugins[i]
		if strings.HasPrefix(plugin.WASM.URL, "file://") {
			plugin.WASM.URL = "file://" + resolve(strings.TrimPrefix(plugin.WASM.URL, "file://"))
		}
	}
}
