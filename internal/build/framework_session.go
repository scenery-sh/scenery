package build

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/machine"
)

// VerifyFrameworkSession checks both selection mechanisms before compilation
// can replace a healthy backend. Local replacement paths may differ only when
// they contain the exact source compiled into the selected CLI.
func VerifyFrameworkSession(ctx context.Context, appRoot string) error {
	_, err := verifyFrameworkSession(ctx, appRoot)
	return err
}

// VerifyFrameworkSessionForBuild verifies the framework session like
// VerifyFrameworkSession and returns a context for one build that carries the
// application framework source it read, so the build's input manifest binds
// that verified source instead of reading the same tree again.
func VerifyFrameworkSessionForBuild(ctx context.Context, appRoot string) (context.Context, error) {
	selected, err := verifyFrameworkSession(ctx, appRoot)
	if err != nil {
		return ctx, err
	}
	return context.WithValue(ctx, verifiedFrameworkSourceKey{}, selected), nil
}

type verifiedFrameworkSourceKey struct{}

// verifiedFrameworkSource returns the framework source a build's context
// verified at root, if any.
func verifiedFrameworkSource(ctx context.Context, root string) (FrameworkSource, bool) {
	if ctx == nil {
		return FrameworkSource{}, false
	}
	source, ok := ctx.Value(verifiedFrameworkSourceKey{}).(FrameworkSource)
	if !ok {
		return FrameworkSource{}, false
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err == nil {
		canonical, err = filepath.Abs(canonical)
	}
	return source, err == nil && canonical == source.Root
}

func verifyFrameworkSession(ctx context.Context, appRoot string) (FrameworkSource, error) {
	producer, err := VerifyFrameworkProducer()
	if err != nil {
		return FrameworkSource{}, err
	}
	selectedRoot, _, err := ResolveFrameworkModule(ctx, appRoot, false)
	if err != nil {
		return FrameworkSource{}, err
	}
	canonical, err := filepath.EvalSymlinks(selectedRoot)
	if err != nil {
		return FrameworkSource{}, err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return FrameworkSource{}, err
	}
	// Producer verification just read this complete tree. One canonical source
	// selected for both roles needs one fresh content read, not a second hash.
	// Different roots still require independent current-byte verification.
	if canonical == producer.Root {
		return producer, nil
	}
	selected, err := FrameworkSourceManifest(canonical)
	if err != nil {
		return FrameworkSource{}, err
	}
	if selected.Digest != producer.Digest {
		return FrameworkSource{}, &FrameworkMismatchError{Selected: selected.Digest, Producer: producer.Digest}
	}
	return selected, nil
}

// FrameworkMismatchError reports that the application selects framework
// source other than the running producer's. Building again cannot succeed
// until the selection or the producer changes.
type FrameworkMismatchError struct {
	Selected string
	Producer string
}

func (e *FrameworkMismatchError) Error() string {
	return fmt.Sprintf("application framework %s does not match this Scenery CLI's source %s; keep the current runtime, run scenery framework use, then restart with its reported executable", e.Selected, e.Producer)
}

// VerifyFrameworkSelection proves cached preparation against current bytes
// and the authored module, without starting either the CLI or application.
func VerifyFrameworkSelection(ctx context.Context, selection FrameworkSelection) error {
	if err := VerifyPreparedFramework(selection); err != nil {
		return err
	}
	moduleRoot, _, err := ResolveFrameworkModule(ctx, selection.AppRoot, false)
	if err != nil {
		return err
	}
	module, err := FrameworkSourceManifest(moduleRoot)
	if err != nil || module.Digest != selection.Source.Digest {
		return fmt.Errorf("application module and prepared Scenery producer disagree; run scenery framework use again")
	}
	return nil
}

// VerifyPreparedFramework checks producer-owned immutable bytes independently
// of a subsequently edited application module. It grants no runtime authority.
func VerifyPreparedFramework(selection FrameworkSelection) error {
	if err := machine.ValidateArtifactIdentity(selection.ArtifactIdentity, frameworkSelectionKind, frameworkSelectionSchema, "prepare the selected framework again"); err != nil {
		return err
	}
	sourceKey := strings.TrimPrefix(selection.Source.Digest, "sha256:")
	expectedSource := filepath.Join(selection.AppRoot, ".scenery", "framework", "source", sourceKey)
	expectedBinaryRoot := filepath.Join(selection.AppRoot, ".scenery", "framework", "bin", sourceKey)
	if !validFrameworkDigest(selection.Source.Digest) || !validFrameworkDigest(selection.ExecutableDigest) ||
		selection.Source.Root != expectedSource || filepath.Base(selection.Executable) != "scenery" ||
		filepath.Base(filepath.Dir(selection.Executable)) != strings.TrimPrefix(selection.ExecutableDigest, "sha256:") ||
		filepath.Dir(filepath.Dir(filepath.Dir(selection.Executable))) != expectedBinaryRoot {
		return fmt.Errorf("framework selection paths do not match their owned content identities")
	}
	for _, path := range []string{selection.Source.Root, selection.Executable} {
		canonical, err := filepath.EvalSymlinks(path)
		if err != nil || canonical != path {
			return fmt.Errorf("framework selection contains a missing or symlinked path: %s", path)
		}
	}
	if selection.Source.Inputs == nil {
		return fmt.Errorf("framework selection has no build-input manifest")
	}
	if err := machine.ValidateArtifactIdentity(selection.Source.Inputs.ArtifactIdentity, buildInputKind, buildInputSchemaDescriptor, "prepare the selected framework again"); err != nil {
		return err
	}
	source, err := FrameworkSourceManifest(selection.Source.Root)
	if err != nil || source.Digest != selection.Source.Digest {
		return fmt.Errorf("prepared framework source content changed: %v", err)
	}
	digest, err := digestExecutable(selection.Executable)
	if err != nil || digest != selection.ExecutableDigest {
		return fmt.Errorf("prepared framework executable content changed: %v", err)
	}
	return nil
}

// DesiredFramework is the framework selection authored in an application's
// go.mod: the pinned scenery.sh requirement, and the local replacement
// directory when one applies to it. Root is empty for a pinned module.
type DesiredFramework struct {
	Version string
	Root    string
}

// ReadDesiredFramework parses the authored selection only. It neither
// resolves nor downloads the module and reads no framework source.
func ReadDesiredFramework(appRoot string) (DesiredFramework, error) {
	data, err := os.ReadFile(filepath.Join(appRoot, "go.mod"))
	if err != nil {
		return DesiredFramework{}, err
	}
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return DesiredFramework{}, err
	}
	desired := DesiredFramework{}
	for _, requirement := range module.Require {
		if requirement.Mod.Path == "scenery.sh" {
			desired.Version = requirement.Mod.Version
		}
	}
	if desired.Version == "" {
		return DesiredFramework{}, fmt.Errorf("application go.mod must pin scenery.sh before preparing its framework")
	}
	for _, replacement := range module.Replace {
		if replacement.Old.Path != "scenery.sh" || (replacement.Old.Version != "" && replacement.Old.Version != desired.Version) {
			continue
		}
		if replacement.New.Version != "" {
			return DesiredFramework{}, fmt.Errorf("select the scenery.sh module directly; remote replacement is not a coherent framework selection")
		}
		root := replacement.New.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(appRoot, root)
		}
		desired.Root = filepath.Clean(root)
		return desired, nil
	}
	return desired, nil
}

// FrameworkSnapshotDigest returns the source digest that names an app-local
// framework snapshot directory, as `scenery framework use --source` selects
// it. Any other directory is not a snapshot of this app root.
func FrameworkSnapshotDigest(appRoot, root string) (string, bool) {
	relative, err := filepath.Rel(filepath.Join(canonicalPath(appRoot), ".scenery", "framework", "source"), canonicalPath(root))
	if err != nil || strings.ContainsRune(relative, filepath.Separator) {
		return "", false
	}
	digest := "sha256:" + relative
	return digest, validFrameworkDigest(digest)
}

// LinkedFrameworkDigest is the framework source digest linked into this
// producer, or empty for an unbound executable.
func LinkedFrameworkDigest() string {
	return linkedFrameworkDigest
}

// RunsPreparedFramework reports whether this process is a framework
// executable that `scenery framework use` prepared for appRoot, as opposed to
// a repository harness binary or another explicitly chosen executable.
func RunsPreparedFramework(appRoot string) bool {
	executable, err := os.Executable()
	if err != nil || linkedFrameworkDigest == "" {
		return false
	}
	return preparedFrameworkExecutable(appRoot, linkedFrameworkDigest, canonicalPath(executable))
}

func preparedFrameworkExecutable(appRoot, sourceDigest, executable string) bool {
	binaries := filepath.Join(canonicalPath(appRoot), ".scenery", "framework", "bin", strings.TrimPrefix(sourceDigest, "sha256:"))
	relative, err := filepath.Rel(binaries, executable)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// canonicalPath resolves symlinks in the longest existing prefix, so /tmp and
// /private/tmp spellings of one app root compare equal even for a snapshot
// directory that a fresh checkout names but has not materialized yet.
func canonicalPath(path string) string {
	path = filepath.Clean(path)
	missing := ""
	for current := path; ; current = filepath.Dir(current) {
		if resolved, err := filepath.EvalSymlinks(current); err == nil {
			return filepath.Join(resolved, missing)
		}
		if parent := filepath.Dir(current); parent == current {
			return path
		}
		missing = filepath.Join(filepath.Base(current), missing)
	}
}

// ResolveFrameworkModule consumes the authored module selection. Download is
// explicit preparation only; a build never changes it. A running `scenery up`
// downloads only while preparing a framework handoff.
func ResolveFrameworkModule(ctx context.Context, appRoot string, download bool) (string, string, error) {
	desired, err := ReadDesiredFramework(appRoot)
	if err != nil {
		return "", "", err
	}
	version := desired.Version
	if desired.Root != "" {
		return desired.Root, version, nil
	}
	arguments := []string{"list", "-m", "-json", "scenery.sh"}
	environment := gotarget.Hermetic(nil)
	if download {
		arguments = []string{"mod", "download", "-json", "scenery.sh@" + version}
		environment = frameworkGoEnvironment()
	}
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir, command.Env = appRoot, environment
	output, err := command.Output()
	if err != nil {
		return "", "", fmt.Errorf("resolve pinned scenery.sh %s; run scenery framework use to prepare it: %w", version, err)
	}
	var resolved struct {
		Dir     string
		Path    string
		Version string
		Error   string
	}
	if err := json.Unmarshal(output, &resolved); err != nil {
		return "", "", err
	}
	if resolved.Path != "scenery.sh" || resolved.Version != version || strings.TrimSpace(resolved.Dir) == "" || resolved.Error != "" {
		return "", "", fmt.Errorf("pinned scenery.sh %s did not resolve to its exact module source", version)
	}
	return resolved.Dir, version, nil
}
