package build

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/mod/semver"

	"scenery.sh/internal/app"
	"scenery.sh/internal/machine"
)

// RuntimeFrameworkPath is a producer locator, not process or resource authority.
// The lifetime-lock owner publishes it; normal control commands still validate
// their current protocol, retained root and exact live process fingerprints.
func RuntimeFrameworkPath(appRoot string) string {
	return filepath.Join(appRoot, ".scenery", "build", "runtime-framework.json")
}

func ReadRuntimeFramework(appRoot string) (FrameworkSelection, error) {
	return readFrameworkSelection(appRoot, RuntimeFrameworkPath(appRoot))
}

// WriteRuntimeFramework must be called only while holding the worktree lifetime
// lock. Retaining the last owner after shutdown allows its exact producer to
// inspect stopped records without silently upgrading them to a newer spec.
func WriteRuntimeFramework(appRoot, version, revision string) error {
	pinned, err := app.ReadFrameworkVersion(appRoot)
	if err != nil {
		return err
	}
	var source FrameworkSource
	if pinned == "" {
		source, err = VerifyFrameworkProducer()
	} else {
		err = VerifyPrebuiltFramework(appRoot, pinned, version)
	}
	if err != nil {
		return err
	}
	root, err := filepath.EvalSymlinks(appRoot)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return err
	}
	digest, err := executableDigest()
	if err != nil {
		return err
	}
	selection := FrameworkSelection{
		ArtifactIdentity: machine.NewArtifactIdentity(frameworkSelectionKind, frameworkSelectionSchema),
		AppRoot:          root, Source: source, SourceOrigin: source.Root,
		Version: version, Executable: executable, ExecutableDigest: digest,
	}
	if source.Root != "" {
		selection.SourceRevision = revision
	}
	if err := VerifyRuntimeFramework(selection); err != nil {
		return err
	}
	return writeFrameworkSelectionAt(selection, RuntimeFrameworkPath(root))
}

// VerifyRuntimeFramework deliberately does not read go.mod or mutable source.
// Those describe a desired candidate, not the producer controlling retained state.
// The strict form is used when the current producer publishes the locator.
func VerifyRuntimeFramework(selection FrameworkSelection) error {
	return verifyRuntimeFramework(selection, true)
}

// VerifyRuntimeFrameworkControl validates a retained producer locator from a
// newer bootstrap producer. It proves the private artifact and executable
// bytes without requiring the inspecting executable to be the retained one;
// runtime ownership is checked separately by the worktree records and locks.
func VerifyRuntimeFrameworkControl(selection FrameworkSelection) error {
	return verifyRuntimeFramework(selection, false)
}

func verifyRuntimeFramework(selection FrameworkSelection, requireCurrentProducer bool) error {
	if err := machine.ValidateArtifactIdentity(selection.ArtifactIdentity, frameworkSelectionKind, frameworkSelectionSchema, "use the runtime's own Scenery executable"); err != nil {
		return err
	}
	if selection.Source == (FrameworkSource{}) {
		if selection.Version == "" || semver.Canonical(selection.Version) != selection.Version {
			return fmt.Errorf("prebuilt runtime producer has no exact release version")
		}
		if requireCurrentProducer {
			executable, err := os.Executable()
			if err != nil || canonicalPath(executable) != selection.Executable {
				return fmt.Errorf("runtime locator does not identify this Scenery executable")
			}
		}
	} else {
		if requireCurrentProducer && !OwnsFrameworkSelection(selection) {
			return fmt.Errorf("runtime locator does not identify this Scenery executable")
		}
		if selection.Source.Inputs == nil {
			return fmt.Errorf("runtime producer has no source input identity")
		}
		if err := machine.ValidateArtifactIdentity(selection.Source.Inputs.ArtifactIdentity, buildInputKind, buildInputSchemaDescriptor, "use the runtime's own Scenery executable"); err != nil {
			return err
		}
	}
	var digest string
	var err error
	if requireCurrentProducer {
		digest, err = executableDigest()
	} else {
		digest, err = digestExecutable(selection.Executable)
	}
	if err != nil || digest != selection.ExecutableDigest {
		return fmt.Errorf("runtime producer executable content changed: %v", err)
	}
	return nil
}
