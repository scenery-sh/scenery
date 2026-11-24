// Package rustbuild builds generated worker entrypoints with the selected
// Cargo toolchain in a private copy of the application's source.
package rustbuild

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"scenery.sh/internal/appwalk"
	"scenery.sh/internal/devcache"
	"scenery.sh/internal/devprocess"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/watchignore"
)

type Service struct {
	ModuleDirectory string
	Workspace       string
	Crate           string
	ServiceName     string
	Toolchain       string
}

type Artifact struct {
	Path   string
	Digest string
}

func Build(ctx context.Context, root string, services []Service, release, check bool) (map[Service]Artifact, error) {
	if len(services) == 0 {
		return nil, nil
	}
	return buildSelected(ctx, root, services, services, release, check, nil)
}

// BuildSelected preserves all generated bin declarations in the private Cargo
// workspace but invokes Cargo only for the selected service entrypoints.
func BuildSelected(ctx context.Context, root string, services, selected []Service, verify func(map[string][sha256.Size]byte) error) (map[Service]Artifact, error) {
	return buildSelected(ctx, root, services, selected, false, false, verify)
}

func buildSelected(ctx context.Context, root string, services, selected []Service, release, check bool, verify func(map[string][sha256.Size]byte) error) (map[Service]Artifact, error) {
	if len(selected) == 0 {
		return nil, nil
	}
	var artifacts map[Service]Artifact
	err := withSnapshot(ctx, root, services, verify, func(snapshot, target, privateRoot string) error {
		artifacts = make(map[Service]Artifact, len(selected))
		for _, service := range selected {
			artifact, err := buildService(ctx, snapshot, target, privateRoot, service, release, check)
			if err != nil {
				return err
			}
			artifacts[service] = artifact
		}
		return nil
	})
	return artifacts, err
}

func withSnapshot(ctx context.Context, root string, services []Service, verify func(map[string][sha256.Size]byte) error, use func(snapshot, target, privateRoot string) error) error {
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	cache, err := devcache.Root()
	if err != nil {
		return err
	}
	privateRoot := filepath.Join(root, ".scenery", "rust")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(privateRoot)
	if err != nil || canonical != privateRoot {
		return fmt.Errorf("build root for Rust must remain in the application workspace")
	}
	// Cargo releases its target-directory lock before its caller can read the
	// executable. Hold the lock through snapshot sync and executable publication.
	unlock, err := devprocess.LockSubstrate(cache, "cargo-target", devprocess.LockOptions{})
	if err != nil {
		return err
	}
	defer unlock()
	// Cargo tracks dependency paths and file mtimes. Keep both stable for
	// unchanged files, including the injected bin declarations.
	snapshot := filepath.Join(privateRoot, "snapshot")
	var captured map[string][sha256.Size]byte
	if verify != nil {
		captured = make(map[string][sha256.Size]byte)
	}
	if err := prepareSnapshot(ctx, root, snapshot, services, captured); err != nil {
		return err
	}
	if verify != nil {
		if err := verify(captured); err != nil {
			return err
		}
	}
	return use(snapshot, filepath.Join(cache, "rust-target"), privateRoot)
}

func prepareSnapshot(ctx context.Context, root, snapshot string, services []Service, captured map[string][sha256.Size]byte) error {
	manifests := make(map[string][]byte)
	for _, service := range services {
		worker := filepath.Join(root, service.ModuleDirectory, "scenerycontract", service.ServiceName+"_worker.rs")
		if info, err := os.Lstat(worker); err != nil || !info.Mode().IsRegular() {
			return fmt.Errorf("service %s has no generated worker entrypoint", service.ServiceName)
		}
		manifest := filepath.Join(service.ModuleDirectory, "Cargo.toml")
		manifests[manifest] = fmt.Appendf(manifests[manifest], "\n[[bin]]\nname = %q\npath = %q\n", binaryName(service), "scenerycontract/"+service.ServiceName+"_worker.rs")
	}
	if err := syncSources(ctx, root, snapshot, true, manifests, captured); err != nil {
		return err
	}
	const sdk = ".scenery/gen/rust-sdk"
	for _, dir := range []string{".scenery", ".scenery/gen"} {
		if err := ensureSnapshotDirectory(filepath.Join(snapshot, dir)); err != nil {
			return err
		}
	}
	return syncSources(ctx, filepath.Join(root, sdk), filepath.Join(snapshot, sdk), false, nil, nil)
}

func syncSources(ctx context.Context, root, destination string, skipIgnored bool, suffixes map[string][]byte, captured map[string][sha256.Size]byte) error {
	seen := make(map[string]bool)
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		output := filepath.Join(destination, relative)
		if skipIgnored && relative != "." {
			if entry.IsDir() && appwalk.SkipDir(root, path) {
				return filepath.SkipDir
			}
			switch entry.Name() {
			case ".git", ".scenery", "node_modules", "target", "dist":
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if watchignore.IsLocalEnvironmentFile(entry.Name()) || entry.Name() == ".DS_Store" {
				return nil
			}
		}
		seen[relative] = true
		if entry.IsDir() {
			return ensureSnapshotDirectory(output)
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("build input for Rust is not a regular file: %s", relative)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if captured != nil {
			captured[relative] = sha256.Sum256(data)
		}
		mode, err := entry.Info()
		if err != nil {
			return err
		}
		return syncSnapshotFile(output, append(data, suffixes[relative]...), mode.Mode().Perm()&0o700)
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(destination, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(destination, path)
		if err != nil {
			return err
		}
		if skipIgnored && relative == ".scenery" && entry.IsDir() {
			return filepath.SkipDir
		}
		if seen[relative] {
			return nil
		}
		if err := os.RemoveAll(path); err != nil {
			return err
		}
		if entry.IsDir() {
			return filepath.SkipDir
		}
		return nil
	})
}

func ensureSnapshotDirectory(path string) error {
	if info, err := os.Lstat(path); err == nil && !info.IsDir() {
		if err := os.Remove(path); err != nil {
			return err
		}
	}
	return os.MkdirAll(path, 0o700)
}

func syncSnapshotFile(path string, data []byte, mode fs.FileMode) error {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode().IsRegular() {
			previous, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if bytes.Equal(previous, data) && info.Mode().Perm() == mode {
				return nil
			}
		} else if err := os.RemoveAll(path); err != nil {
			return err
		}
	}
	if err := os.WriteFile(path, data, mode); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}

func binaryName(service Service) string { return "scenery-worker-" + service.ServiceName }

func buildService(ctx context.Context, snapshot, target, privateRoot string, service Service, release, check bool) (Artifact, error) {
	dir := filepath.Join(snapshot, service.Workspace)
	action := "build"
	if check {
		action = "check"
	}
	args := []string{"+" + service.Toolchain, action, "--locked", "--package", service.Crate, "--bin", binaryName(service), "--message-format=json-render-diagnostics"}
	if release {
		args = append(args, "--release")
	}
	command := exec.CommandContext(ctx, "cargo", args...)
	command.Dir = dir
	command.Env = rustBuildEnvironment(target, dir, !release)
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	output, err := command.Output()
	if err != nil {
		for _, line := range bytes.Split(output, []byte{'\n'}) {
			var message struct{ Message struct{ Rendered string } }
			if json.Unmarshal(line, &message) == nil {
				diagnostics.WriteString(message.Message.Rendered)
			}
		}
		return Artifact{}, fmt.Errorf("cargo %s %s: %w\n%s", action, service.Crate, err, diagnostics.String())
	}
	if check {
		return Artifact{}, nil
	}
	executable, err := workerExecutable(output, binaryName(service))
	if err != nil {
		return Artifact{}, err
	}
	relative, err := filepath.Rel(target, executable)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Artifact{}, fmt.Errorf("executable reported by Cargo is outside its target directory")
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		return Artifact{}, err
	}
	digest := sha256.Sum256(data)
	artifact := Artifact{Path: filepath.Join(privateRoot, "bin", fmt.Sprintf("%x", digest)), Digest: fmt.Sprintf("sha256:%x", digest)}
	if err := os.MkdirAll(filepath.Dir(artifact.Path), 0o700); err != nil {
		return Artifact{}, err
	}
	tmp, err := os.CreateTemp(filepath.Dir(artifact.Path), ".worker-")
	if err != nil {
		return Artifact{}, err
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if chmodErr := tmp.Chmod(0o700); err == nil {
		err = chmodErr
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return Artifact{}, err
	}
	return artifact, os.Rename(tmp.Name(), artifact.Path)
}

func workerExecutable(output []byte, name string) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		var message struct {
			Reason     string
			Target     struct{ Name string }
			Executable string
		}
		if err := json.Unmarshal(scanner.Bytes(), &message); err == nil && message.Reason == "compiler-artifact" && message.Target.Name == name && message.Executable != "" {
			return message.Executable, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "", fmt.Errorf("no executable reported by Cargo for %s", name)
}

func rustBuildEnvironment(target, directory string, development bool) []string {
	ignored := []string{"CARGO_TARGET_DIR", "CARGO_ENCODED_RUSTFLAGS", "RUSTFLAGS", "RUSTC", "RUSTC_WRAPPER", "RUSTC_WORKSPACE_WRAPPER", "RUSTUP_TOOLCHAIN"}
	env := slices.DeleteFunc(envpolicy.Environ(), func(entry string) bool {
		key, _, _ := strings.Cut(entry, "=")
		return slices.Contains(ignored, key)
	})
	env = append(env, "CARGO_TARGET_DIR="+target)
	if development && useDefaultDevDebug(directory, env) {
		env = append(env, "CARGO_PROFILE_DEV_DEBUG=0")
	}
	return env
}
