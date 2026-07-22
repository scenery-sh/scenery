package compiler

import (
	"errors"
	"fmt"
	"path/filepath"

	"scenery.sh/internal/scn"
)

func legacyFilenameDiagnostic(err error) (Diagnostic, bool) {
	var legacyErr *scn.LegacyFilenameError
	if !errors.As(err, &legacyErr) {
		return Diagnostic{}, false
	}
	message := fmt.Sprintf("legacy Scenery contract filename %q is not supported; rename %q to %q", legacyErr.Legacy, legacyErr.Legacy, legacyErr.Replacement)
	return Diagnostic{
		Code:        "SCN1021",
		Severity:    "error",
		Message:     message,
		Path:        filepath.ToSlash(legacyErr.Path),
		Suggestions: []string{fmt.Sprintf("rename %q to %q", legacyErr.Legacy, legacyErr.Replacement)},
		Details: map[string]any{
			"legacy_filename":      legacyErr.Legacy,
			"replacement_filename": legacyErr.Replacement,
		},
	}, true
}
