package main

import (
	"fmt"
	"os"
	"path/filepath"

	"scenery.sh/internal/app"
	"scenery.sh/internal/clientgen"
	"scenery.sh/internal/parse"
)

func discoverConfiguredApp(appRootOpt string) (string, app.Config, error) {
	start, err := resolveAppRoot(appRootOpt)
	if err != nil {
		return "", app.Config{}, err
	}
	appRoot, cfg, err := app.DiscoverRoot(start)
	if err != nil {
		return "", app.Config{}, err
	}
	return appRoot, cfg, nil
}

func writeTypeScriptClient(appRoot string, cfg app.Config, target, outputPath string) (string, error) {
	if target != "" && target != cfg.ID && target != cfg.Name {
		return "", fmt.Errorf("client target %q does not match local app %q", target, cfg.Name)
	}
	model, err := parse.App(appRoot, cfg.Name)
	if err != nil {
		return "", err
	}
	output, err := clientgen.GenerateTypeScript(model, clientgen.TypeScriptOptions{
		AppSlug:      firstNonEmpty(cfg.ID, cfg.Name),
		StandardAuth: cfg.Auth.Enabled,
	})
	if err != nil {
		return "", err
	}

	if !filepath.IsAbs(outputPath) {
		outputPath = filepath.Join(appRoot, outputPath)
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(outputPath, output, 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}
