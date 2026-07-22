package scn

import (
	"fmt"
	"path/filepath"
)

const (
	// AppFilename is the required application-root contract file.
	AppFilename = "app.scn"
	// PackageFilename is the required contract file for a module package.
	PackageFilename = "package.scn"
	// AppLockFilename is the optional dependency lock paired with AppFilename.
	AppLockFilename = "app.lock.scn"

	LegacyAppFilename     = "scenery.scn"
	LegacyPackageFilename = "scenery.package.scn"
	LegacyAppLockFilename = "scenery.lock.scn"
)

var legacyFilenameReplacements = map[string]string{
	LegacyAppFilename:     AppFilename,
	LegacyPackageFilename: PackageFilename,
	LegacyAppLockFilename: AppLockFilename,
}

// LegacyFilenameError rejects a pre-cutover contract filename without
// treating it as an alias. Callers may turn it into the stable SCN1021
// diagnostic while preserving the exact path and rename instruction.
type LegacyFilenameError struct {
	Path        string
	Legacy      string
	Replacement string
}

func (err *LegacyFilenameError) Error() string {
	return fmt.Sprintf("legacy Scenery contract filename %q is not supported; rename %q to %q", filepath.ToSlash(err.Path), err.Legacy, err.Replacement)
}

func legacyFilenameError(path string) *LegacyFilenameError {
	name := filepath.Base(path)
	replacement, ok := legacyFilenameReplacements[name]
	if !ok {
		return nil
	}
	return &LegacyFilenameError{Path: path, Legacy: name, Replacement: replacement}
}
