package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"

	"scenery.sh/internal/app"
	"scenery.sh/internal/build"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/rustbuild"
)

var errWorkerInputsChanged = errors.New("application changed while workers were compiling")

type workerDevelopment struct {
	watch        *workerInputScope
	applied      workerInputs
	cfg          app.Config
	compiled     *compiler.Result
	declarations map[string]bool
	goWorkspace  *build.Result
	rust         workerRustDevelopment
	js           []workerJSBuild
	python       []workerPythonBuild
}

type workerRustDevelopment struct {
	selection     map[string]rustbuild.Service
	services      []rustbuild.Service
	inputs        *rustbuild.DevelopmentInputs
	artifacts     map[rustbuild.Service]rustbuild.Artifact
	digests       map[rustbuild.Service]string
	generated     []string
	jsModules     []string
	pythonModules []string
}

func startWorkerDevelopment(ctx context.Context, watch *workerInputScope, cfg app.Config, compiled *compiler.Result, snapshot *build.SourceSnapshot) (*workerDevelopment, workerBundle, error) {
	dev := &workerDevelopment{watch: watch, cfg: cfg}
	result := compiled
	if result == nil {
		var err error
		result, err = compiler.CompileContext(ctx, watch.root)
		if err != nil {
			return nil, workerBundle{}, err
		}
	}
	bundle, err := dev.prepare(ctx, result)
	if err != nil {
		return nil, workerBundle{}, err
	}
	var (
		inputs       workerInputs
		inputsLoaded bool
	)
	loadInputs := func() (workerInputs, error) {
		if inputsLoaded {
			return inputs, nil
		}
		var captureErr error
		inputs, captureErr = watch.capture()
		if captureErr != nil {
			return nil, captureErr
		}
		inputsLoaded = true
		dev.applied = inputs
		return inputs, nil
	}
	if len(dev.rust.services) > 0 {
		loaded, err := loadInputs()
		if err != nil {
			return nil, workerBundle{}, err
		}
		if _, err := dev.rust.rebuild(ctx, watch.root, loaded, true); err != nil {
			return nil, workerBundle{}, err
		}
		bundle.addRustWorkers(dev.compiled, dev.rust.selection, dev.rust.artifacts)
	}
	for i := range dev.js {
		program, _, err := (&dev.js[i]).run(ctx, watch.root, false)
		if err != nil {
			return nil, workerBundle{}, err
		}
		bundle.Programs = append(bundle.Programs, program)
	}
	for i := range dev.python {
		program, _, err := (&dev.python[i]).run(ctx, watch.root, false)
		if err != nil {
			return nil, workerBundle{}, err
		}
		bundle.Programs = append(bundle.Programs, program)
	}
	dev.goWorkspace, err = bundle.addGoWorkers(ctx, dev.compiled, "development", "", nil, []string{}, snapshot)
	if err != nil {
		return nil, workerBundle{}, err
	}
	if err := bundle.projectConfig(dev.compiled); err != nil {
		return nil, workerBundle{}, err
	}
	if !inputsLoaded && len(dev.rust.services) == 0 {
		watch.roots = dev.inputScopeRoots()
	}
	loaded, err := loadInputs()
	if err != nil {
		return nil, workerBundle{}, err
	}
	if err := watch.addExplicit(loaded, dev.jsDependencies()); err != nil {
		return nil, workerBundle{}, err
	}
	return dev, bundle, nil
}

// inputScopeRoots returns the minimal directory prefixes that contain worker
// inputs (declarations, Go sources/embeds, migrations, and JS/Python modules).
// Root-level files are always included regardless of roots; returning nil
// preserves full-repository scanning.
func (dev *workerDevelopment) inputScopeRoots() []string {
	var raw []string
	for path := range dev.declarations {
		if dir := filepath.Dir(filepath.FromSlash(path)); dir != "." {
			raw = append(raw, dir)
		}
	}
	if dev.goWorkspace != nil {
		for _, path := range dev.goWorkspace.SourceFiles {
			if dir := filepath.Dir(filepath.FromSlash(path)); dir != "." {
				raw = append(raw, dir)
			}
		}
	}
	for _, migration := range dev.cfg.Database.Migrations {
		if migration.Directory != "" && migration.Directory != "." {
			raw = append(raw, migration.Directory)
		}
	}
	for _, item := range dev.js {
		raw = append(raw, item.service.Directory)
	}
	for _, item := range dev.python {
		raw = append(raw, item.service.Directory)
	}
	return normalizeWorkerScopeRoots(raw)
}

func (dev *workerDevelopment) prepare(ctx context.Context, result *compiler.Result) (workerBundle, error) {
	if !result.Valid() {
		return workerBundle{}, invalidGraphError(result)
	}
	declarations, err := workerCompilerInputs(result)
	if err != nil {
		return workerBundle{}, err
	}
	bundle, selection, services, err := prepareWorkerBundle(ctx, result, "development", false)
	if err != nil {
		return workerBundle{}, err
	}
	dev.js, err = projectJSBuilds(result, bundle, "development")
	if err != nil {
		return workerBundle{}, err
	}
	dev.python, err = projectPythonBuilds(result, bundle, "development")
	if err != nil {
		return workerBundle{}, err
	}
	// Pure-Go development apps already verify/publish Go contracts inside
	// addGoWorkers (via LoadPrimedWorkspace/PrepareCachedWorkspaceHeldContext or
	// PrepareBuildGoWorkspace); only polyglot or non-Go apps need an upfront
	// contracts snapshot here before Rust/JS/Python builders run.
	hasGoWorkers := false
	resources := cp.ResourcesByAddress(result.Manifest)
	for _, service := range bundle.Host.Workers {
		if cp.StringValue(resources[service.Address].Spec["runtime"]) == "go" {
			hasGoWorkers = true
			break
		}
	}
	if !hasGoWorkers || len(selection) > 0 || len(dev.js) > 0 || len(dev.python) > 0 {
		if _, err := generate.GenerateSnapshotWithContext(ctx, result, "contracts", false); err != nil {
			return workerBundle{}, err
		}
	}
	dev.compiled, dev.declarations = result, declarations
	dev.rust.selection, dev.rust.services, dev.rust.generated = selection, services, dev.watch.generated
	dev.rust.jsModules = make([]string, 0, len(dev.js))
	for _, build := range dev.js {
		dev.rust.jsModules = append(dev.rust.jsModules, build.service.Directory)
	}
	dev.rust.pythonModules = make([]string, 0, len(dev.python))
	for _, build := range dev.python {
		dev.rust.pythonModules = append(dev.rust.pythonModules, build.service.Directory)
	}
	return bundle, nil
}

func (dev *workerDevelopment) filterExplicit(paths []string) []string {
	var out []string
	for _, path := range paths {
		clean := filepath.Clean(filepath.FromSlash(path))
		if clean == "." || (dev.watch != nil && dev.watch.alwaysIgnored(clean)) {
			continue
		}
		out = append(out, filepath.ToSlash(clean))
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (dev *workerDevelopment) jsDependencies() []string {
	var all []string
	for _, item := range dev.js {
		all = append(all, item.dependencies...)
	}
	return dev.filterExplicit(all)
}

func (rust *workerRustDevelopment) rebuild(ctx context.Context, root string, current workerInputs, rediscover bool) ([]string, error) {
	if len(rust.services) == 0 {
		return nil, nil
	}
	if rediscover {
		inputs, err := rustbuild.DiscoverDevelopmentInputs(ctx, root, rust.services)
		if err != nil {
			return nil, err
		}
		rust.inputs = inputs
	}
	if rust.artifacts == nil {
		rust.artifacts = make(map[rustbuild.Service]rustbuild.Artifact)
		rust.digests = make(map[rustbuild.Service]string)
	}
	digests := make(map[rustbuild.Service]string, len(rust.services))
	var selected []rustbuild.Service
	for _, service := range rust.services {
		digest, err := rust.digest(root, service, current)
		if err != nil {
			return nil, err
		}
		digests[service] = digest
		if digest != rust.digests[service] {
			selected = append(selected, service)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}
	artifacts, err := rustbuild.BuildSelected(ctx, root, rust.services, selected, rust.verifyInputs(root, current, selected))
	if err != nil {
		return nil, err
	}
	maps.Copy(rust.artifacts, artifacts)
	rust.digests = digests
	var rebuilt []string
	for _, service := range selected {
		rebuilt = append(rebuilt, service.ModuleDirectory)
	}
	return rebuilt, nil
}

func (rust *workerRustDevelopment) consumes(service rustbuild.Service, path string) bool {
	switch filepath.Base(path) {
	case "go.mod", "go.sum", "go.work", "go.work.sum", ".scenery.json":
		return false
	}
	if filepath.Ext(path) == ".go" || filepath.Ext(path) == ".scn" {
		return false
	}
	// A Cargo workspace root may also cover independent JS worker modules.
	// Preserve Rust inputs in a Rust package nested below a JS module.
	for _, directory := range slices.Concat(rust.jsModules, rust.pythonModules) {
		if pathUnder(directory, path) && (!pathUnder(directory, service.ModuleDirectory) || !pathUnder(service.ModuleDirectory, path)) {
			return false
		}
	}
	return rust.inputs.Consumes(service, path)
}

func (rust *workerRustDevelopment) verifyInputs(root string, current workerInputs, selected []rustbuild.Service) func(map[string][sha256.Size]byte) error {
	applies := func(path string) bool {
		return slices.ContainsFunc(selected, func(service rustbuild.Service) bool {
			return rust.consumes(service, path)
		})
	}
	scope := workerInputScope{root: root, generated: rust.generated}
	expected := make(map[string][sha256.Size]byte)
	for path, input := range current {
		if applies(path) {
			expected[path] = input.Digest
		}
	}
	return func(captured map[string][sha256.Size]byte) error {
		maps.DeleteFunc(captured, func(path string, _ [sha256.Size]byte) bool {
			return !applies(path) || scope.skip(filepath.Join(root, path))
		})
		if !maps.Equal(captured, expected) {
			return errWorkerInputsChanged
		}
		return nil
	}
}

func (rust *workerRustDevelopment) digest(root string, service rustbuild.Service, current workerInputs) (string, error) {
	hash := sha256.New()
	for _, path := range slices.Sorted(maps.Keys(current)) {
		if rust.consumes(service, path) {
			input := current[path]
			_, _ = fmt.Fprintf(hash, "%s\x00%x/%o\n", path, input.Digest, input.Mode)
		}
	}
	for _, directory := range []string{filepath.Join(service.ModuleDirectory, "scenerycontract"), ".scenery/gen/rust-sdk"} {
		err := filepath.WalkDir(filepath.Join(root, directory), func(path string, entry fs.DirEntry, err error) error {
			if err != nil || entry.IsDir() {
				return err
			}
			if !entry.Type().IsRegular() {
				return fmt.Errorf("generated Rust input %s must be a regular file", path)
			}
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			_, _ = fmt.Fprintf(hash, "%s\x00%x\n", path, sha256.Sum256(data))
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

// invalidGraphError reports the first error diagnostic of an invalid graph.
func invalidGraphError(result *compiler.Result) error {
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Severity == "error" {
			return &cliDiagnosticError{code: contractInvalidExitCode(result), diagnostic: diagnostic}
		}
	}
	return fmt.Errorf("invalid application graph")
}
