// Package pythonbuild verifies and packages independent Python worker modules using uv.
package pythonbuild

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
)

type Service struct {
	compiler.PythonWorkerTarget
	Name string
}

// ErrSourcesChanged reports that a Python worker source file changed during packaging.
var ErrSourcesChanged = errors.New("python worker sources changed during build")

type Artifact struct {
	Path        string
	Digest      string
	Interpreter string
	CacheHit    bool
}

// ResolveInterpreter uses uv to locate or sync the exact Python interpreter
// required by service. When a pyproject.toml exists in the workspace, uv syncs
// the workspace virtual environment and returns its interpreter.
func ResolveInterpreter(ctx context.Context, root string, service Service) (string, error) {
	uv, err := exec.LookPath("uv")
	if err != nil {
		return "", fmt.Errorf("service %s requires uv and Python %s: %w", service.Name, service.Version, err)
	}
	workspaceDir := filepath.Join(root, service.Workspace)
	pyproject := filepath.Join(workspaceDir, "pyproject.toml")
	if info, statErr := os.Lstat(pyproject); statErr == nil && info.Mode().IsRegular() {
		return syncWorkspacePython(ctx, uv, workspaceDir, service)
	}
	return findManagedPython(ctx, uv, service)
}

// DetectInstalledVersion queries uv for an installed Python interpreter and
// returns its exact X.Y.Z version, or an empty string if unavailable.
func DetectInstalledVersion(ctx context.Context) string {
	uv, err := exec.LookPath("uv")
	if err != nil {
		return ""
	}
	out, err := exec.CommandContext(ctx, uv, "python", "find", "--no-python-downloads").Output()
	if err != nil {
		return ""
	}
	python := strings.TrimSpace(string(out))
	if python == "" {
		return ""
	}
	version, err := readPythonVersion(ctx, python)
	if err != nil {
		return ""
	}
	return version
}

func syncWorkspacePython(ctx context.Context, uv, workspaceDir string, service Service) (string, error) {
	args := []string{"sync", "--python", service.Version, "--no-install-project"}
	if info, err := os.Lstat(filepath.Join(workspaceDir, "uv.lock")); err == nil && info.Mode().IsRegular() {
		args = append(args, "--frozen")
	}
	cmd := exec.CommandContext(ctx, uv, args...)
	cmd.Dir = workspaceDir
	cmd.Env = envpolicy.Environ()
	if logs, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("uv sync %s (Python %s): %w\n%s", service.Name, service.Version, err, logs)
	}
	python := filepath.Join(workspaceDir, ".venv", "bin", "python")
	actual, err := readPythonVersion(ctx, python)
	if err != nil {
		return "", fmt.Errorf("read synced Python version for %s: %w", service.Name, err)
	}
	if actual != service.Version {
		return "", fmt.Errorf("service %s requires Python %s, workspace .venv has %s", service.Name, service.Version, actual)
	}
	return python, nil
}

func findManagedPython(ctx context.Context, uv string, service Service) (string, error) {
	cmd := exec.CommandContext(ctx, uv, "python", "find", "--no-python-downloads", service.Version)
	cmd.Env = envpolicy.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("service %s requires Python %s (install with `uv python install %s`): %w\n%s",
			service.Name, service.Version, service.Version, err, strings.TrimSpace(string(out)))
	}
	python := strings.TrimSpace(string(out))
	actual, err := readPythonVersion(ctx, python)
	if err != nil {
		return "", fmt.Errorf("read Python version at %s: %w", python, err)
	}
	if actual != service.Version {
		return "", fmt.Errorf("service %s requires Python %s, found %s", service.Name, service.Version, actual)
	}
	return python, nil
}

func readPythonVersion(ctx context.Context, python string) (string, error) {
	out, err := exec.CommandContext(ctx, python, "-B", "-c", "import sys; print('.'.join(map(str, sys.version_info[:3])))").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Build verifies a Python worker module and packages its executable zipapp.
func Build(ctx context.Context, root string, service Service, check bool) (Artifact, error) {
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return Artifact{}, fmt.Errorf("python build directory must remain in the application workspace")
	}
	privateRoot := filepath.Join(canonicalRoot, ".scenery", "python")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return Artifact{}, err
	}
	if canonical, err := filepath.EvalSymlinks(privateRoot); err != nil || canonical != privateRoot {
		return Artifact{}, fmt.Errorf("python build directory must remain in the application workspace")
	}
	interpreter, err := ResolveInterpreter(ctx, root, service)
	if err != nil {
		return Artifact{}, err
	}

	key := sha256.Sum256(fmt.Appendf(nil, "%q\n%q", service, interpreter))
	cache := filepath.Join(privateRoot, fmt.Sprintf("build-%x.json", key[:8]))
	if !check {
		var prior Artifact
		if err := withCacheLock(privateRoot, func() error {
			prior = readBuildCache(root, cache, service, interpreter)
			return nil
		}); err != nil {
			return Artifact{}, err
		}
		if prior.CacheHit {
			return prior, nil
		}
	}

	directory := filepath.Join(root, service.Directory)
	builder := filepath.Join(directory, "scenerycontract", "_build.py")
	if check {
		cmd := exec.CommandContext(ctx, interpreter, "-B", builder, "--check")
		cmd.Dir = directory
		cmd.Env = envpolicy.Environ()
		if logs, err := cmd.CombinedOutput(); err != nil {
			return Artifact{}, fmt.Errorf("check %s: %w\n%s", service.Name, err, logs)
		}
		if err := runOptionalVenvTypecheck(ctx, root, service); err != nil {
			return Artifact{}, err
		}
		return Artifact{Interpreter: interpreter}, nil
	}

	before, err := captureModuleInputs(root, service)
	if err != nil {
		return Artifact{}, err
	}
	staging, err := os.MkdirTemp(privateRoot, "build-")
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	output := filepath.Join(staging, "worker")
	cmd := exec.CommandContext(ctx, interpreter, "-B", builder, "--output", output, "--interpreter", interpreter)
	cmd.Dir = directory
	cmd.Env = envpolicy.Environ()
	if logs, err := cmd.CombinedOutput(); err != nil {
		return Artifact{}, fmt.Errorf("package %s: %w\n%s", service.Name, err, logs)
	}
	after, err := captureModuleInputs(root, service)
	if err != nil {
		return Artifact{}, err
	}
	if !slices.Equal(before, after) {
		return Artifact{}, ErrSourcesChanged
	}
	fingerprint := buildFingerprintFromDigests(service, interpreter, after)
	digest, err := fileDigest(output)
	if err != nil {
		return Artifact{}, err
	}
	destination := filepath.Join(privateRoot, "worker-"+digest)
	record, err := json.Marshal(buildCacheEntry{Inputs: fingerprint, Executable: digest, Interpreter: interpreter})
	if err != nil {
		return Artifact{}, err
	}
	recordPath := filepath.Join(staging, "build.json")
	if err := os.WriteFile(recordPath, record, 0o600); err != nil {
		return Artifact{}, err
	}
	if err := withCacheLock(privateRoot, func() error {
		if err := os.Rename(recordPath, cache); err != nil {
			return err
		}
		return os.Rename(output, destination)
	}); err != nil {
		return Artifact{}, err
	}
	return Artifact{Path: destination, Digest: "sha256:" + digest, Interpreter: interpreter}, nil
}

// runOptionalVenvTypecheck runs ty, pyright, or mypy when the workspace itself
// installs one into .venv/bin, keeping check deterministic across machines.
func runOptionalVenvTypecheck(ctx context.Context, root string, service Service) error {
	binDir := filepath.Join(root, service.Workspace, ".venv", "bin")
	directory := filepath.Join(root, service.Directory)
	candidates := []struct {
		name string
		args []string
	}{
		{"ty", []string{"check", directory}},
		{"pyright", []string{directory}},
		{"mypy", []string{"--no-error-summary", directory}},
	}
	for _, tool := range candidates {
		path := filepath.Join(binDir, tool.name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o111 == 0 {
			continue
		}
		cmd := exec.CommandContext(ctx, path, tool.args...)
		cmd.Dir = directory
		cmd.Env = envpolicy.Environ()
		if logs, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("typecheck %s (%s): %w\n%s", service.Name, tool.name, err, logs)
		}
		return nil
	}
	return nil
}

// ExportWorkerSources copies the Python worker module sources, generated
// contracts, and optional workspace pyproject.toml/uv.lock files into destination
// for an OCI Linux builder stage. Paths in exclude are skipped so a context
// written inside the application root cannot recurse into itself.
func ExportWorkerSources(root string, services []Service, destination string, exclude ...string) error {
	excluded := make([]string, 0, len(exclude))
	for _, path := range exclude {
		if path == "" {
			continue
		}
		if abs, err := filepath.Abs(path); err == nil {
			excluded = append(excluded, filepath.Clean(abs))
		}
	}
	isExcluded := func(path string) bool {
		abs, err := filepath.Abs(path)
		if err != nil {
			return false
		}
		abs = filepath.Clean(abs)
		for _, target := range excluded {
			if abs == target || strings.HasPrefix(abs, target+string(filepath.Separator)) {
				return true
			}
		}
		return false
	}
	for _, service := range services {
		workspaceDir := filepath.Join(root, service.Workspace)
		for _, manifest := range []string{"pyproject.toml", "uv.lock", ".python-version"} {
			src := filepath.Join(workspaceDir, manifest)
			info, err := os.Lstat(src)
			if err != nil {
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("python workspace file %s must be a regular file", manifest)
			}
			dst := filepath.Join(destination, service.Workspace, manifest)
			if err := copyRegularFile(src, dst, 0o644); err != nil {
				return err
			}
		}
		moduleSrc := filepath.Join(root, service.Directory)
		err := filepath.WalkDir(moduleSrc, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if skipModuleDir(moduleSrc, path) || isExcluded(path) {
					return filepath.SkipDir
				}
				return nil
			}
			if !entry.Type().IsRegular() || skipModuleFile(entry.Name()) || isExcluded(path) {
				return nil
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			return copyRegularFile(path, filepath.Join(destination, rel), 0o644)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func copyRegularFile(src, dst string, mode fs.FileMode) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, mode)
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
