package main

import (
	"context"
	"fmt"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/pythonbuild"
)

type workerPythonBuild struct {
	service     pythonbuild.Service
	program     workerProgram
	interpreter string
}

func projectPythonBuilds(result *compiler.Result, bundle workerBundle, role string) ([]workerPythonBuild, error) {
	resources := cp.ResourcesByAddress(result.Manifest)
	var builds []workerPythonBuild
	for _, service := range bundle.Host.Workers {
		resource := resources[service.Address]
		runtime := cp.StringValue(resource.Spec["runtime"])
		if !compiler.IsPythonRuntime(runtime) {
			continue
		}
		target, err := compiler.SelectPythonWorkerTarget(result, resource, role)
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
		builds = append(builds, workerPythonBuild{
			service: pythonbuild.Service{PythonWorkerTarget: target, Name: service.Address},
			program: workerProgram{
				Service: service.Address, Process: service.Process, Runtime: runtime, Databases: databases,
				Identity: workerProcessIdentity{ContractRevision: compiler.ServiceContractRevision(result.Manifest, resource, nil), Target: "python"},
			},
		})
	}
	return builds, nil
}

type workerPythonBuildError struct {
	service string
	err     error
}

func (failure *workerPythonBuildError) Error() string {
	return fmt.Sprintf("Python worker %s: %s", failure.service, failure.err)
}

func (build *workerPythonBuild) run(ctx context.Context, root string, check bool) (workerProgram, bool, error) {
	artifact, err := pythonbuild.Build(ctx, root, build.service, check)
	if err != nil {
		return workerProgram{}, false, &workerPythonBuildError{service: build.program.Service, err: err}
	}
	build.interpreter = artifact.Interpreter
	program := build.program
	program.Binary, program.Digest = artifact.Path, artifact.Digest
	program.Identity.ImplementationRevision, program.Identity.BuildInputDigest = artifact.Digest, artifact.Digest
	return program, !artifact.CacheHit, nil
}
