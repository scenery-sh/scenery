package main

import (
	"errors"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/graph"
	"scenery.sh/internal/rustbuild"
)

func workerCheckDiagnostic(result *compiler.Result, err error) graph.Diagnostic {
	diagnostic := graph.Diagnostic{Code: "SCN6163", Severity: "error", Message: boundedOutput(err.Error())}
	var address string
	if failure, ok := errors.AsType[*workerJSBuildError](err); ok {
		diagnostic.Code = "SCN6167"
		address = failure.service
	}
	if failure, ok := errors.AsType[*rustbuild.CommandError](err); ok {
		g := agentGraph{Result: result}
		modules, _ := g.modules()
		for _, module := range modules {
			if filepath.Clean(failure.Service.ModuleDirectory) == module.Directory {
				address = graph.ResourceAddress(module.Name, "service", failure.Service.ServiceName)
				break
			}
		}
	}
	for _, resource := range result.Manifest.Resources {
		if resource.Address == address {
			diagnostic.Address = address
			diagnostic.Suggestions = []string{"Expected signatures:\n" + strings.Join(generate.WorkerSignatures(result.Manifest.Resources, resource), "\n")}
			break
		}
	}
	return diagnostic
}
