package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	localagent "scenery.sh/internal/agent"
	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
)

func TestCheckWarningDiagnosticsReportsMissingGoogleOAuthConfiguration(t *testing.T) {
	home := t.TempDir()
	paths := localagent.PathsForHome(home)
	commandAgentPathsOverride = &paths
	t.Cleanup(func() { commandAgentPathsOverride = nil })
	t.Setenv("GOOGLE_OAUTH_CLIENT_ID", "ambient-client")
	t.Setenv("GOOGLE_OAUTH_CLIENT_SECRET", "ambient-secret")

	root := persistentTestAppRoot(t, "check-google-oauth")
	preparePersistentTestApp(t, root, map[string]string{
		".scenery.json": `{"name":"googlecheck","id":"googlecheck","auth":{"enabled":true,"google_oauth":{"enabled":true}}}`,
	})
	// Neither ambient variables nor a dotenv file configure the environment.
	writeTestAppFile(t, root, ".env", "GOOGLE_OAUTH_CLIENT_ID=test-client\nGOOGLE_OAUTH_CLIENT_SECRET=test-secret\n")
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
	if diag.Stage != "auth" || diag.Severity != "warning" || !strings.Contains(diag.Message, "auth.google_client_id") || !strings.Contains(diag.Message, "auth.google_client_secret") {
		t.Fatalf("diagnostic = %+v", diag)
	}

	store, err := appconfig.OpenStore(home, "googlecheck")
	if err != nil {
		t.Fatal(err)
	}
	version, err := appconfig.NewSecretVersion("memory")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := store.Mutate(ctx, "local", appconfig.Mutation{Key: "auth.google_client_id", Value: json.RawMessage(`"test-client"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(ctx, "local", appconfig.Mutation{Key: "auth.google_client_secret", Secret: &version}); err != nil {
		t.Fatal(err)
	}
	diagnostics, err = checkWarningDiagnostics(root, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics with configuration = %+v", diagnostics)
	}
}
