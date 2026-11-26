package main

import (
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/connectionapi"
)

func TestDisabledStandardAuthDoesNotRequirePreparedConnectionInputs(t *testing.T) {
	cfg := app.Config{Auth: app.AuthConfig{Connections: []connectionapi.ProviderConfig{{ID: "google", Preset: "google"}}}}
	for _, enabled := range []bool{false, true} {
		cfg.Auth.Enabled = enabled
		catalog, err := appconfig.BuildCatalog(nil, configFrameworkOptions(cfg))
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"auth.connections.google.client_id", "auth.connections.google.callback_uri", "auth.token_cipher_key"} {
			if _, exists := catalog.Lookup(key); exists != enabled {
				t.Fatalf("enabled=%t key=%s exists=%t", enabled, key, exists)
			}
		}
		if resolved := appconfig.Resolve(catalog, appconfig.Document{}); !enabled && (!resolved.Valid() || len(resolved.Missing()) != 0) {
			t.Fatalf("disabled auth blocks a start: %#v", resolved)
		}
	}
}
