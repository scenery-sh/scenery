package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/rustbuild"
)

type workerDevelopment struct {
	watch        *workerInputWatch
	applied      workerInputs
	compiled     *compiler.Result
	declarations map[string]bool
	goWorkspace  *build.Result
	rust         workerRustDevelopment
	js           []workerJSBuild
}

type workerRustDevelopment struct {
	selection map[string]rustbuild.Service
	services  []rustbuild.Service
	inputs    *rustbuild.DevelopmentInputs
	artifacts map[rustbuild.Service]rustbuild.Artifact
	digests   map[rustbuild.Service]string
	generated []string
	jsModules []string
}

func startWorkerDevelopment(ctx context.Context, watch *workerInputWatch, inputs workerInputs) (*workerDevelopment, workerBundle, error) {
	dev := &workerDevelopment{watch: watch, applied: inputs}
	bundle, err := dev.prepare(ctx)
	if err != nil {
		return nil, workerBundle{}, err
	}
	if _, err := dev.rust.rebuild(ctx, watch.root, inputs, true); err != nil {
		return nil, workerBundle{}, err
	}
	bundle.addRustWorkers(dev.compiled, dev.rust.selection, dev.rust.artifacts)
	bundle, err = dev.rebuildJS(ctx, workerBundle{}, bundle, nil, true, &workerRebuildTiming{})
	if err != nil {
		return nil, workerBundle{}, err
	}
	watch.setExplicit(dev.jsDependencies(), true)
	if err := watch.scanExplicit(dev.applied, false); err != nil {
		return nil, workerBundle{}, err
	}
	dev.goWorkspace, err = bundle.addGoWorkers(ctx, dev.compiled, "development", "", nil, []string{})
	if err != nil {
		return nil, workerBundle{}, err
	}
	if err := bundle.projectConfig(dev.compiled); err != nil {
		return nil, workerBundle{}, err
	}
	return dev, bundle, nil
}

func (dev *workerDevelopment) prepare(ctx context.Context) (workerBundle, error) {
	result, err := compiler.CompileContext(ctx, dev.watch.root)
	if err != nil {
		return workerBundle{}, err
	}
	if !result.Valid() {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" {
				return workerBundle{}, &cliDiagnosticError{code: contractInvalidExitCode(result), diagnostic: diagnostic}
			}
		}
		return workerBundle{}, fmt.Errorf("invalid application graph")
	}
	if dev.compiled != nil && result.Manifest.ContractRevision != dev.compiled.Manifest.ContractRevision {
		return workerBundle{}, fmt.Errorf("application contract changed; restart the application to load its new manifest")
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
	dev.compiled, dev.declarations = result, declarations
	dev.rust.selection, dev.rust.services, dev.rust.generated = selection, services, dev.watch.generated
	dev.rust.jsModules = make([]string, 0, len(dev.js))
	for _, build := range dev.js {
		dev.rust.jsModules = append(dev.rust.jsModules, build.service.Directory)
	}
	return bundle, nil
}

type workerEdit struct {
	declarations bool
	cargoGraph   bool
}

func (dev *workerDevelopment) classify(paths []string) workerEdit {
	var edit workerEdit
	for _, path := range paths {
		edit.declarations = edit.declarations || workerDeclarationInput(path) || dev.declarations[path]
		switch filepath.Base(path) {
		case "Cargo.toml", "Cargo.lock", "rust-toolchain", "rust-toolchain.toml":
			edit.cargoGraph = true
		default:
			edit.cargoGraph = edit.cargoGraph || cargoConfigInput(path)
		}
	}
	return edit
}

func (dev *workerDevelopment) rebuild(ctx context.Context, active workerBundle, current workerInputs, paths []string, timing *workerRebuildTiming) (workerBundle, error) {
	edit := dev.classify(paths)
	next := active
	next.Programs = nil
	previousRustServices := dev.rust.services
	if edit.declarations {
		started := time.Now()
		var err error
		next, err = dev.prepare(ctx)
		timing.ContractMS = workerMilliseconds(time.Since(started))
		if err != nil {
			return workerBundle{}, err
		}
		next.config = active.config
	}
	timing.Declarations = edit.declarations
	rustStarted := time.Now()
	var rebuilt []string
	if edit.declarations || dev.rust.affects(paths) {
		var err error
		rediscover := edit.cargoGraph || !slices.Equal(previousRustServices, dev.rust.services)
		rebuilt, err = dev.rust.rebuild(ctx, dev.watch.root, current, rediscover)
		if err != nil {
			return workerBundle{}, err
		}
	}
	if len(rebuilt) != 0 {
		timing.CargoMS = workerMilliseconds(time.Since(rustStarted))
		timing.Cargo = rebuilt
	}
	if edit.declarations || len(rebuilt) != 0 {
		next.addRustWorkers(dev.compiled, dev.rust.selection, dev.rust.artifacts)
	} else {
		next.keepPrograms(active, "rust")
	}
	next, err := dev.rebuildJS(ctx, active, next, paths, edit.declarations, timing)
	if err != nil {
		return workerBundle{}, err
	}
	goChanged, err := build.DevelopmentGoInputsChanged(dev.goWorkspace, paths)
	if err != nil {
		return workerBundle{}, err
	}
	if edit.declarations || goChanged {
		goStarted := time.Now()
		var workspace *build.Result
		if !edit.declarations {
			if err := dev.bindWorkspaceRevision(current); err != nil {
				return workerBundle{}, err
			}
			workspace = dev.goWorkspace
		}
		retained := make([]string, 0, len(active.Programs))
		for _, program := range active.Programs {
			if program.Runtime == "go" {
				retained = append(retained, program.Binary)
			}
		}
		timing.Go = true
		goContext := build.WithTrace(ctx, func(step build.Step) {
			if step.Name == "process.link" {
				timing.LinkMS += workerMilliseconds(step.Duration)
			}
			if step.Name == "process.linked" {
				timing.GoLinks = step.PackagesRebuilt
			}
		})
		dev.goWorkspace, err = next.addGoWorkers(goContext, dev.compiled, "development", "", workspace, retained)
		timing.GoMS = workerMilliseconds(time.Since(goStarted))
		if err != nil {
			return workerBundle{}, err
		}
		if dev.goWorkspace != nil {
			for path, source := range dev.goWorkspace.SourceStamps {
				if input, ok := current[filepath.FromSlash(path)]; ok && source.Hash != fmt.Sprintf("%x", input.Digest) {
					return workerBundle{}, errWorkerInputsChanged
				}
			}
		}
	} else {
		next.keepPrograms(active, "go")
	}
	if edit.declarations {
		if err := next.projectConfig(dev.compiled); err != nil {
			return workerBundle{}, err
		}
	} else {
		keys := make(map[string][]string, len(active.Programs))
		for _, program := range active.Programs {
			keys[program.Service] = program.ConfigKeys
		}
		for index, program := range next.Programs {
			next.Programs[index].ConfigKeys = keys[program.Service]
		}
	}
	next.setWorkerConfig()
	return next, nil
}

// Bind the retained graph to the current implementation revision without
// recompiling it. A private-workspace cache miss can then safely republish the
// same Go projection through ordinary generation ownership checks.
func (dev *workerDevelopment) bindWorkspaceRevision(current workerInputs) error {
	inputs, err := compiler.WorkspaceRevisionInputs(dev.compiled)
	if err != nil {
		return err
	}
	captured := make(map[string][]byte, len(inputs))
	for _, input := range inputs {
		if !input.Present {
			continue
		}
		path := filepath.FromSlash(input.Path)
		data, err := os.ReadFile(filepath.Join(dev.watch.root, path))
		if err != nil {
			return err
		}
		if expected, exists := current[path]; exists && sha256.Sum256(data) != expected.Digest {
			return errWorkerInputsChanged
		}
		captured[input.Path] = data
	}
	rebound := *dev.compiled
	if err := compiler.BindCapturedWorkspaceRevision(&rebound, captured); err != nil {
		return err
	}
	dev.compiled = &rebound
	return nil
}

func (bundle *workerBundle) keepPrograms(active workerBundle, runtime string) {
	for _, program := range active.Programs {
		if program.Runtime == runtime {
			bundle.Programs = append(bundle.Programs, program)
		}
	}
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

func (rust *workerRustDevelopment) affects(paths []string) bool {
	for _, service := range rust.services {
		for _, path := range paths {
			if rust.consumes(service, path) {
				return true
			}
		}
	}
	return false
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
	for _, directory := range rust.jsModules {
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
	scope := workerInputWatch{root: root, generated: rust.generated}
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
