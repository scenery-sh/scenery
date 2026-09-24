package main

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
	"scenery.sh/internal/build"
	"scenery.sh/internal/graph"
	"scenery.sh/runtime"
)

// The host serves standard authentication and the public configuration, so
// its snapshot must carry the auth secrets that service processes verify
// tokens with, and the public values.
func TestGenerationConfigGivesTheHostAuthAndPublicValues(t *testing.T) {
	home := t.TempDir()
	appconfig.DurableFlush = func(*os.File) error { return nil }
	t.Cleanup(func() { appconfig.DurableFlush = (*os.File).Sync })
	store, err := appconfig.OpenStore(home, "shop")
	if err != nil {
		t.Fatal(err)
	}
	secrets := &memorySecrets{values: map[string][]byte{}}
	version, err := secrets.Create(context.Background(), "local", "auth.jwt_secret", []byte("configured-jwt"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(context.Background(), "local", appconfig.Mutation{Key: "auth.jwt_secret", Secret: &version}); err != nil {
		t.Fatal(err)
	}
	result, err := store.Mutate(context.Background(), "local", appconfig.Mutation{Key: "maps.api_key", Value: json.RawMessage(`"browser-key"`)})
	if err != nil {
		t.Fatal(err)
	}
	document, err := store.ReadRevision("local", result.Revision)
	if err != nil {
		t.Fatal(err)
	}
	manifest := &graph.Manifest{Resources: []graph.Resource{
		{Address: "app/module/maps", Kind: "scenery.module", Name: "maps", Module: "app", Spec: map[string]any{
			"interface_inputs": map[string]any{"api_key": map[string]any{"type": map[string]any{"$expression": "optional(string)"}, "phase": "deployment", "public": true}},
		}},
	}}
	cfg := app.Config{Name: "shop", ID: "shop", Auth: app.AuthConfig{Enabled: true}, Envs: map[string]app.EnvConfig{"local": {Default: true}}}
	env, _ := cfg.ResolveEnv("local")
	set := &build.DevelopmentProcessSet{Services: []build.DevelopmentProcess{{Service: "maps/service/maps"}, {Service: "shop/service/orders"}}}
	resolution, err := resolveDevConfig(context.Background(), manifest, cfg, env, store, document, generationConsumers(set), func() (appconfig.SecretBackend, error) { return secrets, nil })
	if err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []string{hostConsumer, "maps/service/maps", "shop/service/orders"} {
		var snapshot runtime.ConfigSnapshot
		if err := json.Unmarshal(resolution.snapshotFor(consumer).data, &snapshot); err != nil {
			t.Fatalf("%s: %v", consumer, err)
		}
		if string(snapshot.Secrets["auth.jwt_secret"]) != "configured-jwt" {
			t.Fatalf("%s lacks the configured JWT secret", consumer)
		}
		wantPublic := consumer != "shop/service/orders"
		if hasPublic := len(snapshot.Public) == 1 && snapshot.Public[0] == "maps.api_key"; hasPublic != wantPublic {
			t.Fatalf("%s public = %v", consumer, snapshot.Public)
		}
	}
}
