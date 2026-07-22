package main

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"scenery.sh/internal/doctor"
	"scenery.sh/internal/scn"
)

func TestDoctorSurfacesLegacyContractFilenameRename(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, scn.LegacyAppFilename), []byte(`application "legacy" {}`), 0o644); err != nil {
		t.Fatal(err)
	}
	checks := doctorContractFilenameChecks(root)
	if len(checks) != 1 {
		t.Fatalf("checks = %#v", checks)
	}
	want := fmt.Sprintf("rename %q to %q", scn.LegacyAppFilename, scn.AppFilename)
	if checks[0].Status != doctor.StatusError || checks[0].SuggestedAction != want {
		t.Fatalf("check = %#v", checks[0])
	}
}
