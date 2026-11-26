package main

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"

	"scenery.sh/internal/app"
	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/generate"
	"scenery.sh/internal/staticfrontend"
)

type workerFrontend struct {
	Name     string   `json:"name"`
	Root     string   `json:"root"`
	Reserved []string `json:"reserved"`
	Upstream string   `json:"upstream,omitempty"`
}

func selectWorkerFrontend(root string, cfg app.Config, result *compiler.Result) (*workerFrontend, error) {
	name := cfg.RootFrontend()
	frontend := cfg.Frontends[name]
	if frontend.Adapter != "vite" {
		return nil, nil
	}
	if !filepath.IsLocal(name) || filepath.Base(name) != name || name == "." {
		return nil, fmt.Errorf("frontend name for Vite must be a single directory name")
	}
	directory := filepath.Join(root, filepath.FromSlash(frontend.Root))
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil || canonical != directory {
		return nil, fmt.Errorf("frontend root for Vite must be an app-relative non-symlink directory")
	}
	reserved := []string{"/api", "/auth", "/__app", "/__scenery"}
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.http-gateway" {
			if path := cp.StringValue(resource.Spec["base_path"]); path != "" && path != "/" {
				reserved = append(reserved, path)
			}
		}
	}
	return &workerFrontend{Name: name, Root: directory, Reserved: reserved}, nil
}

func buildWorkerFrontend(ctx context.Context, result *compiler.Result, frontend workerFrontend) (*workerFrontend, error) {
	if _, err := generate.GenerateSnapshotWithContext(ctx, result, "", false); err != nil {
		return nil, err
	}
	program, args, err := managedFrontendBuildCommand(frontend.Root, "/")
	if err != nil {
		return nil, err
	}
	build := exec.CommandContext(ctx, program, args...)
	build.Dir = frontend.Root
	if output, err := build.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("build Vite frontend %s: %w\n%s", frontend.Name, err, tailLines(string(output), 30))
	}
	frontend.Root = filepath.Join(frontend.Root, "dist")
	verified, err := staticfrontend.Open(frontend.Root, frontend.Reserved)
	if err != nil {
		return nil, err
	}
	if err := verified.Close(); err != nil {
		return nil, err
	}
	return &frontend, nil
}
