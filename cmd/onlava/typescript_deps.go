package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const typeScriptWorkerDepsMarkerFile = ".onlava-deps.json"

var requiredTypeScriptWorkerPackages = []string{
	"@temporalio/activity",
	"@temporalio/worker",
	"tsx",
}

var typeScriptWorkerLookPath = exec.LookPath
var typeScriptWorkerCommandContext = exec.CommandContext

type typeScriptWorkerDepsMarker struct {
	PackageJSONSHA256 string `json:"package_json_sha256"`
	Installer         string `json:"installer"`
	InstalledAt       string `json:"installed_at"`
}

func ensureTypeScriptWorkerDependencies(ctx context.Context, outputDir string) (bool, error) {
	outputDir = strings.TrimSpace(outputDir)
	if outputDir == "" {
		return false, errors.New("TypeScript worker output directory is empty")
	}
	ready, packageHash, err := typeScriptWorkerDependenciesReady(outputDir)
	if err != nil {
		return false, err
	}
	if ready {
		return false, nil
	}
	installer, args, err := typeScriptWorkerDependencyInstallCommand()
	if err != nil {
		return false, err
	}
	cmd := typeScriptWorkerCommandContext(ctx, installer, args...)
	cmd.Dir = outputDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details != "" {
			return false, fmt.Errorf("install TypeScript worker dependencies with %s: %w\n%s", filepath.Base(installer), err, details)
		}
		return false, fmt.Errorf("install TypeScript worker dependencies with %s: %w", filepath.Base(installer), err)
	}
	if missing := missingTypeScriptWorkerPackages(outputDir); len(missing) > 0 {
		return false, fmt.Errorf("install TypeScript worker dependencies with %s did not create %s", filepath.Base(installer), strings.Join(missing, ", "))
	}
	marker := typeScriptWorkerDepsMarker{
		PackageJSONSHA256: packageHash,
		Installer:         filepath.Base(installer),
		InstalledAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := writeTypeScriptWorkerDepsMarker(outputDir, marker); err != nil {
		return false, err
	}
	return true, nil
}

func typeScriptWorkerDependenciesReady(outputDir string) (bool, string, error) {
	packageHash, err := typeScriptWorkerPackageJSONHash(outputDir)
	if err != nil {
		return false, "", err
	}
	if missing := missingTypeScriptWorkerPackages(outputDir); len(missing) > 0 {
		return false, packageHash, nil
	}
	marker, err := readTypeScriptWorkerDepsMarker(outputDir)
	if err != nil {
		return false, packageHash, nil
	}
	return marker.PackageJSONSHA256 == packageHash, packageHash, nil
}

func typeScriptWorkerPackageJSONHash(outputDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, "package.json"))
	if err != nil {
		return "", fmt.Errorf("read generated TypeScript worker package.json: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func missingTypeScriptWorkerPackages(outputDir string) []string {
	var missing []string
	for _, pkg := range requiredTypeScriptWorkerPackages {
		if _, err := os.Stat(filepath.Join(outputDir, "node_modules", filepath.FromSlash(pkg), "package.json")); err != nil {
			missing = append(missing, pkg)
		}
	}
	return missing
}

func typeScriptWorkerDependencyInstallCommand() (string, []string, error) {
	if bun, err := typeScriptWorkerLookPath("bun"); err == nil {
		return bun, []string{"install"}, nil
	}
	if npm, err := typeScriptWorkerLookPath("npm"); err == nil {
		return npm, []string{"install", "--no-audit", "--no-fund"}, nil
	}
	return "", nil, errors.New("TypeScript Temporal worker dependencies are missing; install Bun or npm and rerun onlava")
}

func readTypeScriptWorkerDepsMarker(outputDir string) (typeScriptWorkerDepsMarker, error) {
	data, err := os.ReadFile(filepath.Join(outputDir, typeScriptWorkerDepsMarkerFile))
	if err != nil {
		return typeScriptWorkerDepsMarker{}, err
	}
	var marker typeScriptWorkerDepsMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return typeScriptWorkerDepsMarker{}, err
	}
	return marker, nil
}

func writeTypeScriptWorkerDepsMarker(outputDir string, marker typeScriptWorkerDepsMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join(outputDir, typeScriptWorkerDepsMarkerFile), data, 0o644)
}
