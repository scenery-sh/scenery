package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/appconfig"
)

func TestConnectionCredentialMigrationUsesSelectedAuthorityAndSafelyReruns(t *testing.T) {
	for _, environment := range []string{"local", "production"} {
		t.Run(environment, func(t *testing.T) {
			e := newConfigTestEnv(t)
			cfg := `{"name":"clean-tech","id":"clean-tech","envs":{"local":{"default":true},"production":{"deploy":{"ssh":["prod-host"]}}},"auth":{"enabled":true,"google_oauth":{"enabled":true},"connections":[{"id":"google","preset":"google","allowed_scopes":["calendar.read"]}]}}`
			if err := os.WriteFile(filepath.Join(e.root, app.PrimaryConfigFilename), []byte(cfg), 0o644); err != nil {
				t.Fatal(err)
			}
			store := mustStore(t, e)
			backend := e.local
			if environment == "production" {
				var err error
				store, err = appconfig.OpenStore(e.target.home, "clean-tech")
				if err != nil {
					t.Fatal(err)
				}
				backend = e.target.secrets
			}
			if _, err := store.Mutate(t.Context(), environment, appconfig.Mutation{Key: "auth.google_client_id", Value: json.RawMessage(`"client"`)}); err != nil {
				t.Fatal(err)
			}
			for _, entry := range []struct{ key, value string }{{"auth.google_client_secret", "original-secret"}, {"auth.token_cipher_key", "same-cipher-key"}} {
				version, err := backend.Create(t.Context(), environment, entry.key, []byte(entry.value))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.Mutate(t.Context(), environment, appconfig.Mutation{Key: entry.key, Secret: &version}); err != nil {
					t.Fatal(err)
				}
			}

			before, _, err := store.Read(environment)
			if err != nil {
				t.Fatal(err)
			}
			output, err := e.run(t, "", "migrate-connections", "--env", environment, "--expect-revision", before.Revision, "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			var result configChangeResult
			if decodeCLIJSON([]byte(output), &result) != nil || !result.Changed || result.Operation != "migrate_connections" {
				t.Fatalf("migration result: %s", output)
			}
			if strings.Contains(output, "original-secret") || strings.Contains(output, "same-cipher-key") {
				t.Fatal("migration exposed secret material")
			}
			after, _, err := store.Read(environment)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(after.Values["auth.connections.google.client_id"], before.Values["auth.google_client_id"]) || after.Secrets["auth.google_client_secret"] != before.Secrets["auth.google_client_secret"] || after.Secrets["auth.token_cipher_key"] != before.Secrets["auth.token_cipher_key"] {
				t.Fatal("migration replaced sign-in credentials or encryption key")
			}
			secret, err := backend.Resolve(t.Context(), environment, "auth.connections.google.client_secret", after.Secrets["auth.connections.google.client_secret"])
			if err != nil || string(secret) != "original-secret" {
				t.Fatal("copied credential differs")
			}
			original, err := backend.Resolve(t.Context(), environment, "auth.google_client_secret", before.Secrets["auth.google_client_secret"])
			if err != nil || string(original) != "original-secret" {
				t.Fatal("original credential changed")
			}
			output, err = e.run(t, "", "migrate-connections", "--env", environment, "-o", "json")
			if err != nil {
				t.Fatal(err)
			}
			if decodeCLIJSON([]byte(output), &result) != nil || result.Changed || result.Revision != after.Revision {
				t.Fatalf("unsafe rerun: %s", output)
			}
			_, err = e.run(t, "", "migrate-connections", "--env", environment, "--expect-revision", before.Revision)
			if err == nil || cliExitCode(err) != 3 {
				t.Fatalf("stale configuration revision classification: %v", err)
			}
			if environment == "production" {
				local, _, err := mustStore(t, e).Read(environment)
				if err != nil || len(local.Values) != 0 || len(local.Secrets) != 0 {
					t.Fatal("deployable migration wrote a local fallback")
				}
			}
		})
	}
}

func TestConnectionCredentialMigrationRefusesConflictsAndResumesPartialCopy(t *testing.T) {
	e := newConfigTestEnv(t)
	store := mustStore(t, e)
	old, err := e.local.Create(t.Context(), "local", "auth.google_client_secret", []byte("preserve"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(t.Context(), "local", appconfig.Mutation{Key: "auth.google_client_id", Value: json.RawMessage(`"client"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(t.Context(), "local", appconfig.Mutation{Key: "auth.google_client_secret", Secret: &old}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Mutate(t.Context(), "local", appconfig.Mutation{Key: "auth.connections.google.client_id", Value: json.RawMessage(`"other"`)}); err != nil {
		t.Fatal(err)
	}
	before, _, _ := store.Read("local")
	if _, err := store.MigrateGoogleConnectionCredentials(t.Context(), "local", "", e.local); !errors.Is(err, appconfig.ErrConnectionCredentialConflict) {
		t.Fatalf("conflict: %v", err)
	}
	after, _, _ := store.Read("local")
	if after.Revision != before.Revision {
		t.Fatal("conflict changed stored inputs")
	}
	if _, err := store.Mutate(t.Context(), "local", appconfig.Mutation{Key: "auth.connections.google.client_id", Value: json.RawMessage(`"client"`)}); err != nil {
		t.Fatal(err)
	}
	result, err := store.MigrateGoogleConnectionCredentials(t.Context(), "local", "", e.local)
	if err != nil || !result.Changed {
		t.Fatalf("partial resume: %+v %v", result, err)
	}
}

func TestConnectionCredentialMigrationClassifiesExpectedFailures(t *testing.T) {
	e := newConfigTestEnv(t)
	store := mustStore(t, e)
	request := configRemoteRequest{Operation: "migrate_connections", Key: "auth.connections.google"}
	backend := func() (appconfig.SecretBackend, error) { return e.local, nil }
	if _, err := applyLocalConfigMutation(t.Context(), store, "local", request, backend); cliExitCode(err) != 3 {
		t.Fatalf("missing source must be a precondition: %v", err)
	}
	version, err := e.local.Create(t.Context(), "local", "auth.google_client_secret", []byte("source-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []appconfig.Mutation{{Key: "auth.google_client_id", Value: json.RawMessage(`"client"`)}, {Key: "auth.google_client_secret", Secret: &version}} {
		if _, err := store.Mutate(t.Context(), "local", mutation); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := applyLocalConfigMutation(t.Context(), store, "local", request, func() (appconfig.SecretBackend, error) { return nil, nil }); cliExitCode(err) != 4 {
		t.Fatalf("missing secret backend must be unavailable: %v", err)
	}
	if _, err := store.Mutate(t.Context(), "local", appconfig.Mutation{Key: "auth.connections.google.client_id", Value: json.RawMessage(`"other"`)}); err != nil {
		t.Fatal(err)
	}
	if _, err := applyLocalConfigMutation(t.Context(), store, "local", request, backend); cliExitCode(err) != 3 {
		t.Fatalf("destination conflict must be a precondition: %v", err)
	}
}
