// Package jsbuild checks and bundles independent JavaScript worker entrypoints.
package jsbuild

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/tscheck"
)

type Service struct {
	compiler.JSWorkerTarget
	Name    string
	Runtime string
}

// ErrSourcesChanged reports that a worker source or dependency changed during bundling.
var ErrSourcesChanged = errors.New("JavaScript worker sources changed during build")

type Artifact struct {
	Path         string
	Digest       string
	CacheHit     bool
	Dependencies []string
}

// Build compiles a JavaScript/TypeScript worker service and caches its artifact.
func Build(ctx context.Context, root string, service Service, check bool) (Artifact, error) {
	return BuildWithVerify(ctx, root, service, check, nil)
}

// BuildWithVerify compiles a worker service and invokes verify against the
// discovered cache dependencies before writing any cache entry.
func BuildWithVerify(ctx context.Context, root string, service Service, check bool, verify func(dependencies []string) error) (Artifact, error) {
	privateRoot := filepath.Join(root, ".scenery", "js")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return Artifact{}, err
	}
	if canonical, err := filepath.EvalSymlinks(privateRoot); err != nil || canonical != privateRoot {
		return Artifact{}, fmt.Errorf("JavaScript build directory must remain in the application workspace")
	}

	key := sha256.Sum256(fmt.Appendf(nil, "%q", service))
	cache := filepath.Join(privateRoot, fmt.Sprintf("build-%x.json", key[:8]))
	var priorDepDigests map[string]string
	if !check {
		var prior Artifact
		if err := withCacheLock(privateRoot, func() error {
			prior = readBuildCache(root, cache, service)
			if !prior.CacheHit {
				priorDepDigests = snapshotCacheDependencies(root, cache)
			}
			return nil
		}); err != nil {
			return Artifact{}, err
		}
		if prior.CacheHit {
			if verify != nil {
				if err := verify(prior.Dependencies); err != nil {
					return Artifact{}, err
				}
			}
			return prior, nil
		}
	}
	bun, err := exec.LookPath("bun")
	if err != nil {
		return Artifact{}, fmt.Errorf("service %s requires Bun %s: %w", service.Name, service.Version, err)
	}
	version, err := exec.CommandContext(ctx, bun, "--version").Output()
	if err != nil {
		return Artifact{}, fmt.Errorf("read Bun version: %w", err)
	}
	if strings.TrimSpace(string(version)) != service.Version {
		return Artifact{}, fmt.Errorf("service %s requires Bun %s, found %s", service.Name, service.Version, strings.TrimSpace(string(version)))
	}
	directory := filepath.Join(root, service.Directory)
	entry := filepath.Join(directory, "scenerycontract", "main.ts")
	for attempt := range 2 {
		buildStarted := time.Now()
		var before []moduleInputDigest
		if !check {
			before, err = captureModuleInputs(root, service)
			if err != nil {
				return Artifact{}, err
			}
		}
		artifact, depDigests, retryNewDep, err := buildJSWorkerAttempt(ctx, root, privateRoot, cache, directory, entry, bun, service, check, before, priorDepDigests, buildStarted, verify)
		if err != nil {
			return Artifact{}, err
		}
		if retryNewDep && attempt == 0 {
			priorDepDigests = depDigests
			continue
		}
		return artifact, nil
	}
	return Artifact{}, fmt.Errorf("JavaScript worker dependencies kept changing during build: %w", ErrSourcesChanged)
}

func buildJSWorkerAttempt(ctx context.Context, root, privateRoot, cache, directory, entry, bun string, service Service, check bool, before []moduleInputDigest, priorDepDigests map[string]string, buildStarted time.Time, verify func([]string) error) (Artifact, map[string]string, bool, error) {
	staging, err := os.MkdirTemp(privateRoot, "build-")
	if err != nil {
		return Artifact{}, nil, false, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	if check {
		if err := checkTypeScript(ctx, root, directory, staging, entry); err != nil {
			return Artifact{}, nil, false, err
		}
	}
	output := filepath.Join(staging, "worker")
	metadata := filepath.Join(staging, "modules.json")
	args := []string{"build", "--no-env-file", "--target=bun", "--packages=bundle", "--outfile=" + output, "--metafile=" + metadata}
	if !check {
		args = append(args, "--compile", "--no-compile-autoload-dotenv", "--no-compile-autoload-bunfig")
	}
	command := exec.CommandContext(ctx, bun, append(args, entry)...)
	command.Dir = staging
	if logs, err := command.CombinedOutput(); err != nil {
		return Artifact{}, nil, false, fmt.Errorf("bundle %s: %w\n%s", service.Name, err, logs)
	}
	inputs, err := verifyImports(root, directory, staging, metadata)
	if err != nil {
		return Artifact{}, nil, false, err
	}
	if check {
		return Artifact{}, nil, false, nil
	}

	after, err := captureModuleInputs(root, service)
	if err != nil {
		return Artifact{}, nil, false, err
	}
	if !slices.Equal(before, after) {
		return Artifact{}, nil, false, fmt.Errorf("%w", ErrSourcesChanged)
	}
	dependencies := cacheDependencies(root, directory, inputs)
	depDigests, retryNewDep, cacheable, err := verifyBuildDependencies(root, priorDepDigests, dependencies, buildStarted, verify)
	if err != nil {
		return Artifact{}, nil, false, err
	}
	if retryNewDep {
		return Artifact{}, depDigests, true, nil
	}
	fingerprint, err := buildFingerprintFromDigests(root, service, after, dependencies, depDigests)
	if err != nil {
		return Artifact{}, nil, false, err
	}
	digest, err := fileDigest(output)
	if err != nil {
		return Artifact{}, nil, false, err
	}
	destination := filepath.Join(privateRoot, "worker-"+digest)
	record, err := json.Marshal(buildCacheEntry{Inputs: fingerprint, Executable: digest, Dependencies: dependencies})
	if err != nil {
		return Artifact{}, nil, false, err
	}
	recordPath := filepath.Join(staging, "build.json")
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		return Artifact{}, nil, false, err
	}
	if err := withCacheLock(privateRoot, func() error {
		if cacheable {
			if err := os.Rename(recordPath, cache); err != nil {
				return err
			}
		}
		return os.Rename(output, destination)
	}); err != nil {
		return Artifact{}, nil, false, err
	}
	return Artifact{Path: destination, Digest: "sha256:" + digest, Dependencies: dependencies}, depDigests, false, nil
}

func verifyBuildCachePublish(root string, priorDepDigests map[string]string, dependencies []string, buildStarted time.Time, verify func([]string) error) (bool, error) {
	_, _, cacheable, err := verifyBuildDependencies(root, priorDepDigests, dependencies, buildStarted, verify)
	return cacheable, err
}

func verifyBuildDependencies(root string, priorDepDigests map[string]string, dependencies []string, buildStarted time.Time, verify func([]string) error) (map[string]string, bool, bool, error) {
	if verify != nil {
		if err := verify(dependencies); err != nil {
			return nil, false, false, err
		}
	}
	depDigests := make(map[string]string, len(dependencies))
	for _, dep := range dependencies {
		currentDigest, err := fileDigest(filepath.Join(root, dep))
		if _, hadPrior := priorDepDigests[dep]; hadPrior {
			if err != nil || currentDigest != priorDepDigests[dep] {
				return nil, false, false, fmt.Errorf("JavaScript worker dependency %s changed during build: %w", dep, ErrSourcesChanged)
			}
		} else if err != nil {
			return nil, false, false, err
		}
		depDigests[dep] = currentDigest
	}
	cacheable := true
	retryNewDep := false
	for _, dep := range dependencies {
		info, err := os.Lstat(filepath.Join(root, dep))
		if err != nil || !info.ModTime().Before(buildStarted) {
			cacheable = false
		}
		if err == nil && !info.ModTime().Before(buildStarted) {
			if _, hadPrior := priorDepDigests[dep]; !hadPrior {
				retryNewDep = true
			}
		}
	}
	return depDigests, retryNewDep, cacheable, nil
}

func checkTypeScript(ctx context.Context, root, module, staging, entry string) error {
	binary, err := tscheck.ManagedBinary(ctx, root)
	if err != nil {
		return err
	}
	config := map[string]any{
		"files":   []string{entry},
		"include": []string{},
		"compilerOptions": map[string]any{
			"noEmit": true, "allowJs": true, "allowImportingTsExtensions": true, "composite": false, "incremental": false,
			"target": "ES2022", "module": "ESNext", "moduleResolution": "Bundler",
			"lib":    []string{"ES2022", "DOM", "DOM.Iterable"},
			"strict": true, "exactOptionalPropertyTypes": true,
		},
	}
	tsconfig := filepath.Join(module, "tsconfig.json")
	if info, err := os.Lstat(tsconfig); err == nil {
		if !info.Mode().IsRegular() {
			return fmt.Errorf("TypeScript configuration must be a regular file")
		}
		config["extends"] = tsconfig
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := json.Marshal(config)
	if err != nil {
		return err
	}
	path := filepath.Join(staging, "tsconfig.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, "--project", path, "--noEmit", "--pretty", "false")
	command.Dir = module
	if logs, err := command.CombinedOutput(); err != nil {
		return fmt.Errorf("typecheck %s: %w\n%s", filepath.Base(module), err, logs)
	}
	return nil
}

func verifyImports(root, module, cwd, path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var graph struct {
		Inputs map[string]struct{} `json:"inputs"`
	}
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, err
	}
	if len(graph.Inputs) == 0 {
		return nil, fmt.Errorf("bun did not report its worker input graph")
	}
	dependency := func(path string) bool {
		relative, err := filepath.Rel(root, path)
		return err == nil && filepath.IsLocal(relative) && slices.Contains(strings.Split(relative, string(filepath.Separator)), "node_modules")
	}
	inputs := make([]string, 0, len(graph.Inputs))
	for path := range graph.Inputs {
		if !filepath.IsAbs(path) {
			path = filepath.Join(cwd, path)
		}
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return nil, err
		}
		relative, err := filepath.Rel(module, path)
		if (err != nil || !filepath.IsLocal(relative) || path != resolved) && (!dependency(path) || !dependency(resolved)) {
			return nil, fmt.Errorf("JavaScript worker import %s must belong to its module or installed workspace dependencies", path)
		}
		inputs = append(inputs, path)
	}
	return inputs, nil
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", digest.Sum(nil)), nil
}
