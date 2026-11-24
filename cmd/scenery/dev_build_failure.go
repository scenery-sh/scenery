package main

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"scenery.sh/internal/rustbuild"
)

// Compiler locations appear as path:line:column (Go, Rust and Bun) or
// path(line,column) (TypeScript). Keep the raw diagnostic alongside them.
var buildLocation = regexp.MustCompile(`(?m)^\s*(?:(?:-->|at)\s+)?([^\n:()]+\.(?:go|rs|[cm]?[jt]sx?|scn))[:(]([0-9]+)[:,]([0-9]+)`)

type devBuildFailure struct {
	Module string `json:"module,omitempty"`
	File   string `json:"file,omitempty"`
	Line   int    `json:"line,omitempty"`
	Column int    `json:"column,omitempty"`
	Stderr string `json:"stderr"`
}

func describeDevBuildFailure(err error, root string, modules []agentModule, changed []string) *devBuildFailure {
	result := &devBuildFailure{Stderr: boundedOutput(err.Error())}
	var js *workerJSBuildError
	if errors.As(err, &js) {
		result.Module, _, _ = strings.Cut(js.service, "/service/")
	}
	rust, cargoFailure := errors.AsType[*rustbuild.CommandError](err)
	if cargoFailure {
		for _, module := range modules {
			if module.Directory == filepath.Clean(rust.Service.ModuleDirectory) {
				result.Module = module.Name
				break
			}
		}
	}
	if diagnostic, ok := errors.AsType[*cliDiagnosticError](err); ok {
		result.File = diagnostic.diagnostic.Path
		if location := diagnostic.diagnostic.Range; location != nil {
			result.Line, result.Column = location.Start.Line, location.Start.Column
		}
	} else if match := buildLocation.FindStringSubmatch(result.Stderr); match != nil {
		result.File = filepath.Clean(strings.TrimSpace(match[1]))
		result.Line, _ = strconv.Atoi(match[2])
		result.Column, _ = strconv.Atoi(match[3])
	}
	if cargoFailure && result.File != "" && filepath.IsLocal(result.File) && !pathUnder(rust.Service.Workspace, result.File) {
		result.File = filepath.Join(rust.Service.Workspace, result.File)
	}
	if relative, relErr := filepath.Rel(root, result.File); relErr == nil && filepath.IsLocal(relative) {
		result.File = relative
	}
	best := -1
	if result.Module == "" && result.File != "" {
		for _, module := range modules {
			// Go and Cargo can render paths relative to a private build root.
			path := filepath.ToSlash(result.File)
			dir := filepath.ToSlash(module.Directory)
			if (pathUnder(dir, path) || strings.Contains(path, "/"+dir+"/")) && len(dir) > best {
				result.Module, best = module.Name, len(dir)
			}
		}
	}
	if result.Module == "" {
		candidates := map[string]bool{}
		for _, module := range modules {
			for _, file := range changed {
				if pathUnder(module.Directory, file) {
					candidates[module.Name] = true
				}
			}
		}
		if len(candidates) == 1 {
			for name := range candidates {
				result.Module = name
			}
		}
	}
	return result
}
