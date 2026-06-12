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
	"sort"
	"strings"
	"time"
)

const typeScriptWorkerDepsMarkerFile = ".scenery-deps.json"
const typeScriptWorkerAppDepsMarkerFile = ".scenery-app-deps.json"

var requiredTypeScriptWorkerPackages = []string{
	"@temporalio/activity",
	"@temporalio/worker",
	"tsx",
}

var typeScriptWorkerLookPath = exec.LookPath
var typeScriptWorkerCommandContext = exec.CommandContext

type typeScriptWorkerDepsMarker struct {
	PackageJSONSHA256 string `json:"package_json_sha256"`
	LockfileSHA256    string `json:"lockfile_sha256,omitempty"`
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
	if err := writeTypeScriptWorkerDepsMarker(filepath.Join(outputDir, typeScriptWorkerDepsMarkerFile), marker); err != nil {
		return false, err
	}
	return true, nil
}

func ensureTypeScriptWorkerAppDependencies(ctx context.Context, appRoot, markerDir string) (bool, error) {
	appRoot = strings.TrimSpace(appRoot)
	if appRoot == "" {
		return false, errors.New("TypeScript worker app root is empty")
	}
	packagePath := filepath.Join(appRoot, "package.json")
	if _, err := os.Stat(packagePath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	markerDir = strings.TrimSpace(markerDir)
	if markerDir == "" {
		markerDir = appRoot
	}
	ready, _, _, err := typeScriptWorkerAppDependenciesReady(appRoot, markerDir)
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
	cmd.Dir = appRoot
	output, err := cmd.CombinedOutput()
	if err != nil {
		details := strings.TrimSpace(string(output))
		if details != "" {
			return false, fmt.Errorf("install app TypeScript dependencies with %s: %w\n%s", filepath.Base(installer), err, details)
		}
		return false, fmt.Errorf("install app TypeScript dependencies with %s: %w", filepath.Base(installer), err)
	}
	if missing := missingTypeScriptPackageDependencies(appRoot); len(missing) > 0 {
		return false, fmt.Errorf("install app TypeScript dependencies with %s did not create %s", filepath.Base(installer), strings.Join(missing, ", "))
	}
	packageHash, err := typeScriptPackageJSONHash(packagePath)
	if err != nil {
		return false, err
	}
	lockHash, err := typeScriptPackageLockHash(appRoot)
	if err != nil {
		return false, err
	}
	marker := typeScriptWorkerDepsMarker{
		PackageJSONSHA256: packageHash,
		LockfileSHA256:    lockHash,
		Installer:         filepath.Base(installer),
		InstalledAt:       time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := os.MkdirAll(markerDir, 0o755); err != nil {
		return false, err
	}
	if err := writeTypeScriptWorkerDepsMarker(filepath.Join(markerDir, typeScriptWorkerAppDepsMarkerFile), marker); err != nil {
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
	marker, err := readTypeScriptWorkerDepsMarker(filepath.Join(outputDir, typeScriptWorkerDepsMarkerFile))
	if err != nil {
		return false, packageHash, nil
	}
	return marker.PackageJSONSHA256 == packageHash, packageHash, nil
}

func typeScriptWorkerAppDependenciesReady(appRoot, markerDir string) (bool, string, string, error) {
	packageHash, err := typeScriptPackageJSONHash(filepath.Join(appRoot, "package.json"))
	if err != nil {
		return false, "", "", err
	}
	lockHash, err := typeScriptPackageLockHash(appRoot)
	if err != nil {
		return false, "", "", err
	}
	if missing := missingTypeScriptPackageDependencies(appRoot); len(missing) > 0 {
		return false, packageHash, lockHash, nil
	}
	marker, err := readTypeScriptWorkerDepsMarker(filepath.Join(markerDir, typeScriptWorkerAppDepsMarkerFile))
	if err != nil {
		return false, packageHash, lockHash, nil
	}
	return marker.PackageJSONSHA256 == packageHash && marker.LockfileSHA256 == lockHash, packageHash, lockHash, nil
}

func typeScriptWorkerPackageJSONHash(outputDir string) (string, error) {
	hash, err := typeScriptPackageJSONHash(filepath.Join(outputDir, "package.json"))
	if err != nil {
		return "", fmt.Errorf("read generated TypeScript worker package.json: %w", err)
	}
	return hash, nil
}

func typeScriptPackageJSONHash(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func typeScriptPackageLockHash(root string) (string, error) {
	for _, name := range []string{"bun.lock", "bun.lockb", "package-lock.json", "pnpm-lock.yaml", "yarn.lock"} {
		path := filepath.Join(root, name)
		data, err := os.ReadFile(path)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return "", err
		}
		sum := sha256.Sum256(append([]byte(name+"\n"), data...))
		return hex.EncodeToString(sum[:]), nil
	}
	return "", nil
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

func missingTypeScriptPackageDependencies(root string) []string {
	packages, err := typeScriptPackageDependencies(filepath.Join(root, "package.json"))
	if err != nil {
		return []string{"package.json dependencies"}
	}
	var missing []string
	for _, pkg := range packages {
		if _, err := os.Stat(filepath.Join(root, "node_modules", filepath.FromSlash(pkg), "package.json")); err != nil {
			missing = append(missing, pkg)
		}
	}
	return missing
}

func typeScriptPackageDependencies(packagePath string) ([]string, error) {
	data, err := os.ReadFile(packagePath)
	if err != nil {
		return nil, err
	}
	var pkg struct {
		Dependencies    map[string]json.RawMessage `json:"dependencies"`
		DevDependencies map[string]json.RawMessage `json:"devDependencies"`
	}
	if err := json.Unmarshal(data, &pkg); err != nil {
		return nil, err
	}
	seen := map[string]struct{}{}
	for _, deps := range []map[string]json.RawMessage{pkg.Dependencies, pkg.DevDependencies} {
		for name := range deps {
			name = strings.TrimSpace(name)
			if name != "" {
				seen[name] = struct{}{}
			}
		}
	}
	packages := make([]string, 0, len(seen))
	for name := range seen {
		packages = append(packages, name)
	}
	sort.Strings(packages)
	return packages, nil
}

func typeScriptWorkerDependencyInstallCommand() (string, []string, error) {
	if bun, err := typeScriptWorkerLookPath("bun"); err == nil {
		return bun, []string{"install"}, nil
	}
	if npm, err := typeScriptWorkerLookPath("npm"); err == nil {
		return npm, []string{"install", "--no-audit", "--no-fund"}, nil
	}
	return "", nil, errors.New("TypeScript Temporal worker dependencies are missing; install Bun or npm and rerun scenery")
}

func readTypeScriptWorkerDepsMarker(path string) (typeScriptWorkerDepsMarker, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return typeScriptWorkerDepsMarker{}, err
	}
	var marker typeScriptWorkerDepsMarker
	if err := json.Unmarshal(data, &marker); err != nil {
		return typeScriptWorkerDepsMarker{}, err
	}
	return marker, nil
}

func writeTypeScriptWorkerDepsMarker(path string, marker typeScriptWorkerDepsMarker) error {
	data, err := json.MarshalIndent(marker, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}
