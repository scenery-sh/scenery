package main

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/jsbuild"
)

type workerJSBuild struct {
	service      jsbuild.Service
	program      workerProgram
	dependencies []string
}

func projectJSBuilds(result *compiler.Result, bundle workerBundle, role string) ([]workerJSBuild, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	var builds []workerJSBuild
	for _, service := range bundle.Host.Workers {
		resource := resources[service.Address]
		runtime := cp.StringValue(resource.Spec["runtime"])
		if !compiler.IsJSRuntime(runtime) {
			continue
		}
		target, err := compiler.SelectJSWorkerTarget(result, resource, role)
		if err != nil {
			return nil, err
		}
		requires, err := compiler.ServiceGoDependencies(resources, resource)
		if err != nil {
			return nil, err
		}
		var databases map[string]string
		for _, dependency := range requires {
			if dependency.Resolver == "sql" {
				if databases == nil {
					databases = make(map[string]string)
				}
				databases[dependency.Name] = dependency.RuntimeName
			}
		}
		builds = append(builds, workerJSBuild{
			service: jsbuild.Service{JSWorkerTarget: target, Name: service.Address, Runtime: runtime},
			program: workerProgram{
				Service: service.Address, Process: service.Process, Runtime: runtime, Databases: databases,
				Identity: workerProcessIdentity{ContractRevision: compiler.ServiceContractRevision(result.Manifest, resource, nil), Target: "bun"},
			},
		})
	}
	return builds, nil
}

type workerJSBuildError struct {
	service string
	err     error
}

func (failure *workerJSBuildError) Error() string {
	return fmt.Sprintf("JavaScript worker %s: %s", failure.service, failure.err)
}

func (failure *workerJSBuildError) Unwrap() error { return failure.err }

func (build *workerJSBuild) run(ctx context.Context, root string, check bool, verify func(dependencies []string) error) (workerProgram, bool, error) {
	artifact, err := jsbuild.BuildWithVerify(ctx, root, build.service, check, verify)
	if err != nil {
		if errors.Is(err, jsbuild.ErrSourcesChanged) {
			err = fmt.Errorf("%w: %w", errWorkerInputsChanged, err)
		}
		return workerProgram{}, false, &workerJSBuildError{service: build.program.Service, err: err}
	}
	if !check {
		build.dependencies = slices.Clone(artifact.Dependencies)
	}
	program := build.program
	program.Binary, program.Digest = artifact.Path, artifact.Digest
	program.Identity.ImplementationRevision, program.Identity.BuildInputDigest = artifact.Digest, artifact.Digest
	return program, !artifact.CacheHit, nil
}

func (build workerJSBuild) affected(paths []string) bool {
	for _, path := range paths {
		if slices.Contains(build.dependencies, path) {
			return true
		}
		switch filepath.Ext(path) {
		case ".go", ".rs", ".scn":
			continue
		}
		if pathUnder(build.service.Directory, path) {
			return true
		}
		switch filepath.Base(path) {
		case "package.json", "bun.lock", "bun.lockb", "package-lock.json", "tsconfig.json", "bunfig.toml":
			if pathUnder(build.service.Workspace, path) && pathUnder(filepath.Dir(path), build.service.Directory) {
				return true
			}
		}
	}
	return false
}

func (dev *workerDevelopment) jsDependencies() []string {
	var all []string
	for _, build := range dev.js {
		all = append(all, build.dependencies...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

func pruneJSWorkers(root string, programs []workerProgram) {
	keep := make([]string, 0, len(programs))
	for _, program := range programs {
		if compiler.IsJSRuntime(program.Runtime) && program.Binary != "" {
			keep = append(keep, program.Binary)
		}
	}
	_ = jsbuild.Prune(root, keep)
}

func pruneJSWorkersByPaths(root string, binaries []string) {
	_ = jsbuild.Prune(root, binaries)
}

func (dev *workerDevelopment) rebuildJS(ctx context.Context, active, next workerBundle, current workerInputs, paths []string, declarations bool, timing *workerRebuildTiming) (workerBundle, error) {
	start := time.Now()
	var verifyDeps func([]string) error
	if current != nil && dev.watch != nil {
		root := dev.watch.root
		verifyDeps = func(deps []string) error {
			for _, dep := range deps {
				clean := filepath.Clean(filepath.FromSlash(dep))
				expected, ok := current[clean]
				if !ok {
					continue
				}
				full := filepath.Join(root, clean)
				info, err := os.Lstat(full)
				if err != nil {
					if errors.Is(err, os.ErrNotExist) {
						return errWorkerInputsChanged
					}
					return err
				}
				if !info.Mode().IsRegular() {
					return errWorkerInputsChanged
				}
				data, err := os.ReadFile(full)
				if err != nil {
					return err
				}
				if (workerInput{Digest: sha256.Sum256(data), Mode: info.Mode().Perm()}) != expected {
					return errWorkerInputsChanged
				}
			}
			return nil
		}
	}
	for index := range dev.js {
		build := &dev.js[index]
		index := slices.IndexFunc(active.Programs, func(program workerProgram) bool {
			return program.Service == build.program.Service && program.Runtime == build.program.Runtime
		})
		if index >= 0 && !declarations && !build.affected(paths) {
			next.Programs = append(next.Programs, active.Programs[index])
			continue
		}
		program, compiled, err := build.run(ctx, dev.watch.root, false, verifyDeps)
		if err != nil {
			return workerBundle{}, err
		}
		next.Programs = append(next.Programs, program)
		if compiled {
			timing.JS = append(timing.JS, program.Service)
		}
	}
	if len(timing.JS) > 0 {
		timing.JSMS = workerMilliseconds(time.Since(start))
	}
	return next, nil
}
