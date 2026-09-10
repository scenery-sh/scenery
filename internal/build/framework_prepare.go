package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/machine"
)

const frameworkSelectionKind = "scenery.framework-selection"
const frameworkSelectionSchema = `{"identity":"artifact","app_root":"path","source":{"root":"path","digest":"digest","build_input_manifest":"go-build-input-manifest"},"source_origin":"path","source_revision":"optional_string","version":"string","executable":"path","executable_digest":"digest"}`

type FrameworkSelection struct {
	machine.ArtifactIdentity
	AppRoot          string          `json:"app_root"`
	Source           FrameworkSource `json:"source"`
	SourceOrigin     string          `json:"source_origin"`
	SourceRevision   string          `json:"source_revision,omitempty"`
	Version          string          `json:"version"`
	Executable       string          `json:"executable"`
	ExecutableDigest string          `json:"executable_digest"`
}

func FrameworkSelectionPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "build", "framework.json")
}

func ReadFrameworkSelection(appRoot string) (FrameworkSelection, error) {
	return readFrameworkSelection(appRoot, FrameworkSelectionPath(appRoot))
}

func readFrameworkSelection(appRoot, path string) (FrameworkSelection, error) {
	var selection FrameworkSelection
	data, err := os.ReadFile(path)
	if err != nil {
		return selection, err
	}
	if err := machine.DecodeArtifact(data, &selection, &selection.ArtifactIdentity, frameworkSelectionKind, frameworkSelectionSchema, "prepare the selected framework again"); err != nil {
		return selection, err
	}
	canonical, err := filepath.EvalSymlinks(appRoot)
	if err != nil || selection.AppRoot != canonical || !validFrameworkDigest(selection.Source.Digest) || !validFrameworkDigest(selection.ExecutableDigest) {
		return selection, fmt.Errorf("framework selection does not belong to this app root; prepare it again")
	}
	return selection, nil
}

// PrepareFramework snapshots the selected module inputs and compiles the CLI
// from those exact bytes. Its receipt reuses the Go build-input manifest; it is
// disposable build state, not another resource-ownership or dependency lock.
func PrepareFramework(ctx context.Context, appRoot, sourceRoot, version, revision string) (FrameworkSelection, error) {
	var selection FrameworkSelection
	canonical, err := filepath.EvalSymlinks(appRoot)
	if err != nil {
		return selection, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return selection, err
	}
	stateRoot := filepath.Join(canonical, ".scenery", "framework")
	if err := ensureFrameworkStateRoot(canonical, stateRoot); err != nil {
		return selection, err
	}
	unlock, err := lockWorkspace(stateRoot)
	if err != nil {
		return selection, err
	}
	defer unlock()
	source, err := FrameworkSourceManifest(sourceRoot)
	if err != nil {
		return selection, err
	}
	snapshotRoot := filepath.Join(stateRoot, "source", strings.TrimPrefix(source.Digest, "sha256:"))
	if err := ensureFrameworkStateRoot(canonical, filepath.Dir(snapshotRoot)); err != nil {
		return selection, err
	}
	if err := materializeFrameworkSource(source, snapshotRoot); err != nil {
		return selection, err
	}
	snapshot, err := FrameworkSourceManifest(snapshotRoot)
	if err != nil {
		return selection, err
	}
	if snapshot.Digest != source.Digest {
		return selection, fmt.Errorf("framework source changed during materialization; retry from a stable source checkout")
	}
	// Repeated preparation can dispatch straight to an existing immutable
	// producer. Verification is required before reuse; a path hit is not proof.
	if retained, err := ReadFrameworkSelection(canonical); err == nil &&
		retained.Source.Digest == source.Digest && retained.Version == version &&
		VerifyPreparedFramework(retained) == nil {
		retained.SourceOrigin, retained.SourceRevision = source.Root, revision
		return retained, nil
	}
	// Finalization runs in the selected producer. Reuse its already verified,
	// content-addressed executable instead of rebuilding it recursively. The
	// source manifest above was constructed by this process, not the bootstrap.
	if executable, err := os.Executable(); err == nil {
		if executable, err = filepath.EvalSymlinks(executable); err == nil {
			if digest, err := digestExecutable(executable); err == nil {
				candidate := FrameworkSelection{
					ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema),
					AppRoot:          canonical, Source: snapshot, SourceOrigin: source.Root, SourceRevision: revision,
					Version: version, Executable: executable, ExecutableDigest: digest,
				}
				if OwnsFrameworkSelection(candidate) && VerifyPreparedFramework(candidate) == nil {
					return candidate, nil
				}
			}
		}
	}
	// Module archives contain dashboard source, not ignored compiled assets.
	// Build only inside the private selected snapshot; never mutate the module
	// cache or original co-development checkout during producer preparation.
	uiBuild := exec.CommandContext(ctx, "bash", "./scripts/build-dashboard-ui-embed.sh")
	uiBuild.Dir, uiBuild.Env = snapshot.Root, frameworkGoEnvironment()
	if output, err := uiBuild.CombinedOutput(); err != nil {
		return selection, fmt.Errorf("prepare selected Scenery dashboard (requires bun): %w: %s", err, strings.TrimSpace(string(output)))
	}
	flags, err := FrameworkProducerLinkerFlags(snapshot.Digest)
	if err != nil {
		return selection, err
	}
	if version == "" {
		version = "dev"
	}
	if strings.ContainsAny(version+revision, " \t\r\n\"'\\") {
		return selection, fmt.Errorf("invalid framework version or revision")
	}
	flags += " -X=main.sceneryVersion=" + version + " -X=main.sceneryCommit=" + revision
	binaryDir := filepath.Join(stateRoot, "bin", strings.TrimPrefix(source.Digest, "sha256:"), runtime.GOOS+"-"+runtime.GOARCH+"-"+runtime.Version())
	if err := ensureFrameworkStateRoot(canonical, binaryDir); err != nil {
		return selection, err
	}
	staging, err := os.MkdirTemp(binaryDir, ".build-*")
	if err != nil {
		return selection, err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	binary := filepath.Join(staging, "scenery")
	command := exec.CommandContext(ctx, "go", "build", "-buildvcs=false", "-ldflags="+flags, "-o", binary, "./cmd/scenery")
	command.Dir, command.Env = snapshot.Root, frameworkGoEnvironment()
	if output, err := command.CombinedOutput(); err != nil {
		return selection, fmt.Errorf("build selected Scenery producer: %w: %s", err, strings.TrimSpace(string(output)))
	}
	after, err := FrameworkSourceManifest(snapshotRoot)
	if err != nil || after.Digest != source.Digest {
		return selection, fmt.Errorf("framework snapshot changed while compiling its CLI: %v", err)
	}
	digest, err := digestExecutable(binary)
	if err != nil {
		return selection, err
	}
	finalDir := filepath.Join(binaryDir, strings.TrimPrefix(digest, "sha256:"))
	finalBinary := filepath.Join(finalDir, "scenery")
	if info, err := os.Lstat(finalDir); errors.Is(err, os.ErrNotExist) {
		if err := os.Rename(staging, finalDir); err != nil {
			return selection, err
		}
	} else if err != nil {
		return selection, err
	} else if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return selection, fmt.Errorf("framework executable directory is not a non-symlink directory: %s", finalDir)
	} else if info, err := os.Lstat(finalBinary); err != nil || !info.Mode().IsRegular() {
		return selection, fmt.Errorf("retained framework executable is not a regular file: %s", finalBinary)
	} else if retainedDigest, err := digestExecutable(finalBinary); err != nil || retainedDigest != digest {
		return selection, fmt.Errorf("retained framework executable content changed: %s", finalBinary)
	}
	selection = FrameworkSelection{
		ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema),
		AppRoot:          canonical, Source: snapshot, SourceOrigin: source.Root, SourceRevision: revision,
		Version: version, Executable: finalBinary, ExecutableDigest: digest,
	}
	return selection, nil
}

func WriteFrameworkSelection(selection FrameworkSelection) error {
	if !OwnsFrameworkSelection(selection) {
		return fmt.Errorf("only the selected executable may publish its framework selection")
	}
	if err := VerifyPreparedFramework(selection); err != nil {
		return err
	}
	return writeFrameworkSelectionAt(selection, FrameworkSelectionPath(selection.AppRoot))
}

func writeFrameworkSelectionAt(selection FrameworkSelection, path string) error {
	if err := ensureFrameworkStateRoot(selection.AppRoot, filepath.Dir(path)); err != nil {
		return err
	}
	data, err := json.MarshalIndent(selection, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(path, append(data, '\n'), 0o600, atomicfile.Options{SyncFile: true, SyncDir: true})
}

// OwnsFrameworkSelection is the finalization boundary, not an old-spec decoder.
// Bootstrap results locate a candidate but cannot authorize publishing its receipt.
func OwnsFrameworkSelection(selection FrameworkSelection) bool {
	if linkedFrameworkDigest == "" || linkedFrameworkDigest != selection.Source.Digest {
		return false
	}
	executable, err := os.Executable()
	if err != nil {
		return false
	}
	executable, err = filepath.EvalSymlinks(executable)
	return err == nil && executable == selection.Executable
}

func materializeFrameworkSource(source FrameworkSource, destination string) error {
	if info, err := os.Lstat(destination); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("framework snapshot is not a non-symlink directory: %s", destination)
		}
		retained, err := FrameworkSourceManifest(destination)
		if err != nil || retained.Digest != source.Digest {
			return fmt.Errorf("retained framework source is not the selected content: %s", destination)
		}
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".source-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(staging) }()
	for _, input := range source.Inputs.Entries {
		relative := strings.TrimPrefix(input.Identity, "framework/source/")
		data, err := os.ReadFile(filepath.Join(source.Root, filepath.FromSlash(relative)))
		if err != nil {
			return err
		}
		target := filepath.Join(staging, filepath.FromSlash(relative))
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		// The containing tree is unpublished until its verified rename.
		if err := os.WriteFile(target, data, 0o444); err != nil {
			return err
		}
	}
	staged, err := FrameworkSourceManifest(staging)
	if err != nil || staged.Digest != source.Digest {
		return fmt.Errorf("framework source changed while copying its inputs: %v", err)
	}
	return os.Rename(staging, destination)
}

func ensureFrameworkStateRoot(appRoot, stateRoot string) error {
	relative, err := filepath.Rel(appRoot, stateRoot)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("framework state must remain beneath the app root")
	}
	path := appRoot
	for _, segment := range strings.Split(relative, string(filepath.Separator)) {
		path = filepath.Join(path, segment)
		info, err := os.Lstat(path)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(path, 0o700); err != nil {
				return err
			}
			continue
		}
		if err != nil {
			return err
		}
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("framework state root must be a non-symlink directory: %s", path)
		}
	}
	return nil
}

func frameworkGoEnvironment() []string {
	values := gotarget.Hermetic(nil)
	proxy := envpolicy.Get("GOPROXY")
	if proxy == "" {
		proxy = "https://proxy.golang.org,direct"
	}
	for index, value := range values {
		if strings.HasPrefix(value, "GOPROXY=") {
			values[index] = "GOPROXY=" + proxy
		}
	}
	return values
}
