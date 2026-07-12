package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestCheckWarningDiagnosticsReportsMissingGoogleOAuthCredentials(t *testing.T) {
	for _, name := range []string{"GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET"} {
		value, exists := os.LookupEnv(name)
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if exists {
				_ = os.Setenv(name, value)
			} else {
				_ = os.Unsetenv(name)
			}
		})
	}

	root := persistentTestAppRoot(t, "check-google-oauth")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{"name":"googlecheck","auth":{"enabled":true,"google_oauth":{"enabled":true}}}`,
	})
	if err := os.Remove(filepath.Join(root, ".env")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		t.Fatal(err)
	}

	diagnostics, err := checkWarningDiagnostics(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	diag := diagnostics[0]
	if diag.Stage != "auth" || diag.Severity != "warning" || !strings.Contains(diag.Message, "GOOGLE_OAUTH_CLIENT_ID") || !strings.Contains(diag.Message, "GOOGLE_OAUTH_CLIENT_SECRET") {
		t.Fatalf("diagnostic = %+v", diag)
	}

	writeTestAppFile(t, root, ".env", "GOOGLE_OAUTH_CLIENT_ID=test-client\nGOOGLE_OAUTH_CLIENT_SECRET=test-secret\n")
	diagnostics, err = checkWarningDiagnostics(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics with env = %+v", diagnostics)
	}
}
