package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
)

func TestCheckWarningDiagnosticsReportsMissingGoogleOAuthCredentials(t *testing.T) {
	for _, name := range []string{"GoogleOAuthClientID", "GoogleOAuthClientSecret", "GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET"} {
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
	if diag.Stage != "auth" || diag.Severity != "warning" || !strings.Contains(diag.Message, "GoogleOAuthClientID") || !strings.Contains(diag.Message, "GoogleOAuthClientSecret") {
		t.Fatalf("diagnostic = %+v", diag)
	}

	writeTestAppFile(t, root, ".env", "GoogleOAuthClientID=test-client\nGoogleOAuthClientSecret=test-secret\n")
	diagnostics, err = checkWarningDiagnostics(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics with env = %+v", diagnostics)
	}
}
