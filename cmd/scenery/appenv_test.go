package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestNamedEnvironmentDotenvPrecedenceAndInjection(t *testing.T) {
	root := t.TempDir()
	for name, value := range map[string]string{
		".env":                  "VALUE=base\n",
		".env.production":       "VALUE=environment\n",
		".env.local":            "VALUE=machine\n",
		".env.production.local": "VALUE=environment-machine\n",
	} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	env, err := appEnvWithDotEnv(nil, root, ".env", ".env.production", ".env.local", ".env.production.local")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := lookupEnvValue(env, "VALUE"); value != "environment-machine" {
		t.Fatalf("dotenv value = %q", value)
	}
	env, err = appEnvWithDotEnv([]string{"VALUE=process"}, root, ".env", ".env.production", ".env.local", ".env.production.local")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := lookupEnvValue(env, "VALUE"); value != "process" {
		t.Fatalf("process value = %q", value)
	}

	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local":      {Default: true},
		"production": {Deploy: &app.EnvDeployConfig{}},
	}}
	processEnv, err := appProcessEnv(root, cfg, "json", "production")
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(processEnv, "\n")
	if !strings.Contains(joined, "SCENERY_ENV=production") || !strings.Contains(joined, "SCENERY_RUNTIME_ENV=production") {
		t.Fatalf("environment identity missing: %s", joined)
	}
	if _, err := os.Stat(filepath.Join(root, ".env.local.local")); !os.IsNotExist(err) {
		t.Fatalf("local environment must not use .env.local.local")
	}
}

func TestAppProcessEnvInjectsResolvedLibraryLinkage(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local": {Default: true, Libraries: map[string]app.EnvLibraryConfig{
			"maps3d": {Linkage: "shared", Manifest: "artifacts/maps3d.json"},
		}},
	}}
	processEnv, err := appProcessEnv(root, cfg, "json", "local")
	if err != nil {
		t.Fatal(err)
	}
	if value, _ := lookupEnvValue(processEnv, "SCENERY_LIBRARY_MAPS3D_LINKAGE"); value != "shared" {
		t.Fatalf("library linkage = %q", value)
	}
	wantManifest := filepath.Join(root, "artifacts", "maps3d.json")
	if value, _ := lookupEnvValue(processEnv, "SCENERY_LIBRARY_MAPS3D_MANIFEST"); value != wantManifest {
		t.Fatalf("library manifest = %q, want %q", value, wantManifest)
	}
}
