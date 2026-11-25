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

	"scenery.sh/internal/build"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/rustbuild"
)

var errWorkerInputsChanged = errors.New("application changed while workers were compiling")

type workerDevelopment struct {
	watch        *workerInputScope
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

func startWorkerDevelopment(ctx context.Context, watch *workerInputScope, inputs workerInputs) (*workerDevelopment, workerBundle, error) {
	dev := &workerDevelopment{watch: watch, applied: inputs}
	result, err := compiler.CompileContext(ctx, watch.root)
	if err != nil {
		return nil, workerBundle{}, err
	}
	bundle, err := dev.prepare(ctx, result)
	if err != nil {
		return nil, workerBundle{}, err
	}
	if _, err := dev.rust.rebuild(ctx, watch.root, inputs, true); err != nil {
		return nil, workerBundle{}, err
	}
	bundle.addRustWorkers(dev.compiled, dev.rust.selection, dev.rust.artifacts)
	for _, build := range dev.js {
		program, _, err := build.run(ctx, watch.root, false)
		if err != nil {
			return nil, workerBundle{}, err
		}
		bundle.Programs = append(bundle.Programs, program)
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

func (dev *workerDevelopment) prepare(ctx context.Context, result *compiler.Result) (workerBundle, error) {
	if !result.Valid() {
		for _, diagnostic := range result.Diagnostics {
			if diagnostic.Severity == "error" {
				return workerBundle{}, &cliDiagnosticError{code: contractInvalidExitCode(result), diagnostic: diagnostic}
			}
		}
		return workerBundle{}, fmt.Errorf("invalid application graph")
	}
	declarations, err := workerCompilerInputs(result)
	if err != nil {
		return workerBundle{}, err
	}
	bundle, selection, services, err := prepareWorkerBundle(ctx, result, "development")
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
