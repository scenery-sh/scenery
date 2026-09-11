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
	producer, err := VerifyFrameworkProducer()
	if err != nil {
		return err
	}
	selectedRoot, _, err := ResolveFrameworkModule(ctx, appRoot, false)
	if err != nil {
		return err
	}
	canonical, err := filepath.EvalSymlinks(selectedRoot)
	if err != nil {
		return err
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return err
	}
	// Producer verification just read this complete tree. One canonical source
	// selected for both roles needs one fresh content read, not a second hash.
	// Different roots still require independent current-byte verification.
	if canonical == producer.Root {
		return nil
	}
	selected, err := FrameworkSourceManifest(canonical)
	if err != nil {
		return err
	}
	if selected.Digest != producer.Digest {
		return fmt.Errorf("application framework %s does not match this Scenery CLI's source %s; keep the current runtime, run scenery framework use, then restart with its reported executable", selected.Digest, producer.Digest)
	}
	return nil
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

// ResolveFrameworkModule consumes the authored module selection. Download is
// explicit preparation only; starting/rebuilding a session never changes it.
func ResolveFrameworkModule(ctx context.Context, appRoot string, download bool) (string, string, error) {
	data, err := os.ReadFile(filepath.Join(appRoot, "go.mod"))
	if err != nil {
		return "", "", err
	}
	module, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return "", "", err
	}
	version := ""
	for _, requirement := range module.Require {
		if requirement.Mod.Path == "scenery.sh" {
			version = requirement.Mod.Version
		}
	}
	if version == "" {
		return "", "", fmt.Errorf("application go.mod must pin scenery.sh before preparing its framework")
	}
	for _, replacement := range module.Replace {
		if replacement.Old.Path != "scenery.sh" || (replacement.Old.Version != "" && replacement.Old.Version != version) {
			continue
		}
		if replacement.New.Version != "" {
			return "", "", fmt.Errorf("select the scenery.sh module directly; remote replacement is not a coherent framework selection")
		}
		root := replacement.New.Path
		if !filepath.IsAbs(root) {
			root = filepath.Join(appRoot, root)
		}
		return filepath.Clean(root), version, nil
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
