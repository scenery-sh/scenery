package main

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/jsbuild"
)

type workerJSBuild struct {
	service jsbuild.Service
	program workerProgram
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

func (build workerJSBuild) run(ctx context.Context, root string, check bool) (workerProgram, bool, error) {
	artifact, err := jsbuild.Build(ctx, root, build.service, check)
	if err != nil {
		return workerProgram{}, false, &workerJSBuildError{service: build.program.Service, err: err}
	}
	program := build.program
	program.Binary, program.Digest = artifact.Path, artifact.Digest
	program.Identity.ImplementationRevision, program.Identity.BuildInputDigest = artifact.Digest, artifact.Digest
	return program, !artifact.CacheHit, nil
}

func (build workerJSBuild) affected(paths []string) bool {
	for _, path := range paths {
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

func (dev *workerDevelopment) rebuildJS(ctx context.Context, active, next workerBundle, paths []string, declarations bool, timing *workerRebuildTiming) (workerBundle, error) {
	start := time.Now()
	for _, build := range dev.js {
		index := slices.IndexFunc(active.Programs, func(program workerProgram) bool {
			return program.Service == build.program.Service && program.Runtime == build.program.Runtime
		})
		if index >= 0 && !declarations && !build.affected(paths) {
			next.Programs = append(next.Programs, active.Programs[index])
			continue
		}
		program, compiled, err := build.run(ctx, dev.watch.root, false)
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
