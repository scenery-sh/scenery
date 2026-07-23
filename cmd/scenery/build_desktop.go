package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/desktop"
	"scenery.sh/internal/envpolicy"
)

type desktopBuildResult struct {
	Environment string                       `json:"environment"`
	Frontends   []desktopFrontendBuildResult `json:"frontends"`
}

type desktopFrontendBuildResult struct {
	Name         string   `json:"name"`
	TauriRoot    string   `json:"tauri_root"`
	FrontendDist string   `json:"frontend_dist"`
	Artifacts    []string `json:"artifacts"`
}

func buildDesktop(ctx context.Context, appRoot string, cfg app.Config, env app.ResolvedEnv, commandOutput io.Writer) (desktopBuildResult, error) {
	shells, err := configuredDesktopShells(appRoot, cfg)
	if err != nil {
		return desktopBuildResult{}, err
	}
	baseEnv, err := appEnvWithDotEnv(envpolicy.Environ(), appRoot, env.DotEnvFiles()...)
	if err != nil {
		return desktopBuildResult{}, err
	}
	overrides := []string{
		"SCENERY_APP_ROOT=" + appRoot,
		"SCENERY_ENV=" + env.Name,
		"SCENERY_RUNTIME_ENV=" + env.Name,
	}
	if domain := strings.TrimSpace(env.Domain); domain != "" {
		apiBase := "https://" + domain
		overrides = append(overrides,
			"API_BASE_URL="+apiBase,
			"SCENERY_API_BASE_URL="+apiBase,
			"SCENERY_API_URL="+apiBase,
			"VITE_API_BASE_URL="+apiBase,
		)
	}
	buildEnv := envWithOverrides(baseEnv, overrides...)
	result := desktopBuildResult{Environment: env.Name, Frontends: make([]desktopFrontendBuildResult, 0, len(shells))}
	for _, shell := range shells {
		buildBin, buildArgs, err := managedFrontendBuildCommand(shell.FrontendRoot, "")
		if err != nil {
			return desktopBuildResult{}, fmt.Errorf("prepare desktop frontend %q build: %w", shell.Name, err)
		}
		if err := desktop.Run(ctx, desktop.Command{Path: buildBin, Args: buildArgs, Dir: shell.FrontendRoot}, buildEnv, commandOutput); err != nil {
			return desktopBuildResult{}, fmt.Errorf("build desktop frontend %q: %w", shell.Name, err)
		}
		distDir := filepath.Join(shell.FrontendRoot, "dist")
		if info, err := os.Stat(distDir); err != nil || !info.IsDir() {
			return desktopBuildResult{}, fmt.Errorf("desktop frontend %q build produced no output directory at %s", shell.Name, distDir)
		}
		command, err := desktop.BuildCommand(shell, distDir)
		if err != nil {
			return desktopBuildResult{}, err
		}
		if err := desktop.Run(ctx, command, buildEnv, commandOutput); err != nil {
			return desktopBuildResult{}, fmt.Errorf("bundle desktop frontend %q: %w", shell.Name, err)
		}
		artifacts, err := desktop.BundleArtifacts(shell)
		if err != nil {
			return desktopBuildResult{}, fmt.Errorf("desktop frontend %q: %w", shell.Name, err)
		}
		result.Frontends = append(result.Frontends, desktopFrontendBuildResult{
			Name:         shell.Name,
			TauriRoot:    shell.TauriRoot,
			FrontendDist: distDir,
			Artifacts:    artifacts,
		})
	}
	return result, nil
}

func desktopBuildPayload(result desktopBuildResult) map[string]any {
	frontends := make([]map[string]any, 0, len(result.Frontends))
	for _, frontend := range result.Frontends {
		frontends = append(frontends, map[string]any{
			"name":          frontend.Name,
			"tauri_root":    frontend.TauriRoot,
			"frontend_dist": frontend.FrontendDist,
			"artifacts":     frontend.Artifacts,
		})
	}
	return withCLIPayloadIdentity("scenery.build.desktop", map[string]any{
		"environment": result.Environment,
		"frontends":   frontends,
	})
}
