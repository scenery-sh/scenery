package main

import (
	"context"
	"fmt"

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
