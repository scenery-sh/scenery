package main

import (
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/workers"
)

func typeScriptTemporalDiagnostics(appRoot string, appModel *model.App) []workers.Diagnostic {
	if appModel == nil {
		return nil
	}
	ts := workers.DiscoverTypeScriptActivities(appRoot)
	return workers.ValidateTypeScriptContracts(ts, temporalExternalActivityDeclarations(appRoot, appModel), nativeGoTemporalDeclarations(appRoot, appModel))
}

func temporalExternalActivityDeclarations(appRoot string, appModel *model.App) []workers.ExternalActivityDeclaration {
	if appModel == nil {
		return nil
	}
	var declarations []workers.ExternalActivityDeclaration
	for _, decl := range appModel.Runtime {
		if decl.Kind != model.RuntimeDeclarationTemporalExternalActivity {
			continue
		}
		declarations = append(declarations, workerDeclarationFromRuntime(appRoot, decl))
	}
	return declarations
}

func nativeGoTemporalDeclarations(appRoot string, appModel *model.App) []workers.ExternalActivityDeclaration {
	if appModel == nil {
		return nil
	}
	var declarations []workers.ExternalActivityDeclaration
	for _, decl := range appModel.Runtime {
		if decl.Kind != model.RuntimeDeclarationTemporalWorkflow && decl.Kind != model.RuntimeDeclarationTemporalActivity {
			continue
		}
		if decl.TaskQueue == "" || !decl.TaskQueueExplicit {
			continue
		}
		declarations = append(declarations, workerDeclarationFromRuntime(appRoot, decl))
	}
	return declarations
}

func workerDeclarationFromRuntime(appRoot string, decl *model.RuntimeDeclaration) workers.ExternalActivityDeclaration {
	out := workers.ExternalActivityDeclaration{
		Name:      decl.Name,
		TaskQueue: decl.TaskQueue,
		Input:     decl.InputType,
		Output:    decl.OutputType,
		Kind:      string(decl.Kind),
	}
	if decl.Package != nil && decl.Package.GoPkg != nil && decl.Package.GoPkg.Fset != nil {
		position := decl.Package.GoPkg.Fset.Position(decl.TokenPos)
		out.File = normalizeDiagnosticFile(appRoot, position.Filename)
		out.Line = position.Line
	}
	return out
}
