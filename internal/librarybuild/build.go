package librarybuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/generate"
	scenerylibrary "scenery.sh/library"
)

type Options struct {
	Workspace string
	OutputDir string
	Spec      generate.LibraryBuildSpec
	Version   string
	Platforms []string
}

type Result struct {
	ManifestPath string
	Manifest     scenerylibrary.Manifest
}

var commandContext = exec.CommandContext

const (
	linuxBuildImage     = "golang:1.26.3-bookworm"
	linuxBuildGoVersion = "go1.26.3"
	linuxGlibcFloor     = "2.36"
)

func Build(ctx context.Context, options Options) (Result, error) {
	if options.Workspace == "" || options.OutputDir == "" || options.Spec.Name == "" {
		return Result{}, fmt.Errorf("workspace, output directory, and library spec are required")
	}
	version := strings.TrimSpace(options.Version)
	if version == "" {
		version = options.Spec.Version
	}
	if !semver.IsValid(version) || semver.Canonical(version) != version {
		return Result{}, fmt.Errorf("library version must be canonical semantic version such as v1.2.3")
	}
	platforms, err := resolvePlatforms(options.Platforms)
	if err != nil {
		return Result{}, err
	}
	if err := os.MkdirAll(options.OutputDir, 0o755); err != nil {
		return Result{}, err
	}
	manifest := scenerylibrary.Manifest{
		Kind: scenerylibrary.ManifestKind, SchemaRevision: scenerylibrary.ManifestSchemaRevision,
		Library: options.Spec.Name, Version: version, ABIHash: options.Spec.ABIHash,
		Artifacts: map[string]scenerylibrary.Artifact{},
	}
	for _, platform := range platforms {
		goos, goarch, _ := strings.Cut(platform, "/")
		extension := ".so"
		if goos == "darwin" {
			extension = ".dylib"
		}
		filename := "lib" + options.Spec.Artifact + "_" + goos + "_" + goarch + extension
		path := filepath.Join(options.OutputDir, filename)
		if err := buildPlatform(ctx, options, version, goos, goarch, path); err != nil {
			return Result{}, err
		}
		digest, err := fileDigest(path)
		if err != nil {
			return Result{}, err
		}
		artifact := scenerylibrary.Artifact{
			GOOS: goos, GOARCH: goarch, Path: filename, SHA256: digest,
			GoVersion: runtime.Version(),
		}
		if goos == "linux" {
			artifact.GoVersion = linuxBuildGoVersion
			artifact.GlibcFloor = linuxGlibcFloor
		}
		manifest.Artifacts[goos+"_"+goarch] = artifact
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Result{}, err
	}
	manifestPath := filepath.Join(options.OutputDir, options.Spec.Artifact+".scenery-library.json")
	if err := atomicfile.Write(manifestPath, append(encoded, '\n'), 0o644, atomicfile.Options{SyncFile: true, SyncDir: true}); err != nil {
		return Result{}, err
	}
	return Result{ManifestPath: manifestPath, Manifest: manifest}, nil
}

func resolvePlatforms(values []string) ([]string, error) {
	if len(values) == 0 {
		values = []string{"darwin/arm64", "linux/amd64"}
	}
	seen := map[string]bool{}
	var platforms []string
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "host" {
			value = runtime.GOOS + "/" + runtime.GOARCH
		}
		if value != "darwin/arm64" && value != "linux/amd64" {
			return nil, fmt.Errorf("unsupported library platform %q; supported platforms are darwin/arm64 and linux/amd64", value)
		}
		if !seen[value] {
			seen[value] = true
			platforms = append(platforms, value)
		}
	}
	sort.Strings(platforms)
	return platforms, nil
}

func buildPlatform(ctx context.Context, options Options, version, goos, goarch, output string) error {
	buildArgs := []string{
		"build", "-buildmode=c-shared", "-tags=" + options.Spec.ExportBuildTag,
		"-ldflags=-X=main.sceneryLibraryVersion=" + version,
		"-o", output, options.Spec.ExportPackage,
	}
	if goos == "darwin" {
		if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
			return fmt.Errorf("building darwin/arm64 libraries requires a darwin/arm64 host, current host is %s/%s", runtime.GOOS, runtime.GOARCH)
		}
		command := commandContext(ctx, "go", buildArgs...)
		command.Dir = options.Workspace
		command.Env = replaceEnv(envpolicy.Environ(), "CGO_ENABLED=1", "GOOS="+goos, "GOARCH="+goarch)
		if outputBytes, err := command.CombinedOutput(); err != nil {
			return fmt.Errorf("build %s/%s library: %w\n%s", goos, goarch, err, outputBytes)
		}
		return nil
	}
	if goos != "linux" || goarch != "amd64" {
		return fmt.Errorf("cross-building %s/%s is unsupported on %s/%s", goos, goarch, runtime.GOOS, runtime.GOARCH)
	}
	outputDir := filepath.Dir(output)
	containerOutput := "/out/" + filepath.Base(output)
	containerArgs := []string{"run", "--rm", "--platform", "linux/amd64",
		"-v", options.Workspace + ":" + options.Workspace,
		"-v", outputDir + ":/out",
		"-w", options.Workspace,
	}
	for _, replacement := range localModuleReplacements(options.Workspace) {
		if replacement == options.Workspace || strings.HasPrefix(replacement, options.Workspace+string(filepath.Separator)) {
			continue
		}
		containerArgs = append(containerArgs, "-v", replacement+":"+replacement+":ro")
	}
	containerArgs = append(containerArgs, linuxBuildImage, "go", "build",
		"-buildmode=c-shared", "-tags="+options.Spec.ExportBuildTag,
		"-ldflags=-X=main.sceneryLibraryVersion="+version,
		"-o", containerOutput, options.Spec.ExportPackage)
	command := commandContext(ctx, "docker", containerArgs...)
	if outputBytes, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("build linux/amd64 library in container: %w\n%s", err, outputBytes)
	}
	return nil
}

func localModuleReplacements(workspace string) []string {
	data, err := os.ReadFile(filepath.Join(workspace, "go.mod"))
	if err != nil {
		return nil
	}
	parsed, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	var paths []string
	for _, replacement := range parsed.Replace {
		path := replacement.New.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(workspace, filepath.FromSlash(path))
		}
		path = filepath.Clean(path)
		if info, err := os.Stat(path); err == nil && info.IsDir() && !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

func replaceEnv(base []string, overrides ...string) []string {
	keys := map[string]bool{}
	for _, override := range overrides {
		key, _, _ := strings.Cut(override, "=")
		keys[key] = true
	}
	result := make([]string, 0, len(base)+len(overrides))
	for _, value := range base {
		key, _, _ := strings.Cut(value, "=")
		if !keys[key] {
			result = append(result, value)
		}
	}
	return append(result, overrides...)
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
