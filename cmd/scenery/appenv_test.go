package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestNamedEnvironmentDotenvPrecedenceAndInjection(t *testing.T) {
	t.Parallel()

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
	if value := lookupEnvValue(env, "VALUE"); value != "environment-machine" {
		t.Fatalf("dotenv value = %q", value)
	}
	env, err = appEnvWithDotEnv([]string{"VALUE=process"}, root, ".env", ".env.production", ".env.local", ".env.production.local")
	if err != nil {
		t.Fatal(err)
	}
	if value := lookupEnvValue(env, "VALUE"); value != "process" {
		t.Fatalf("process value = %q", value)
	}

	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local":      {Default: true},
		"production": {Deploy: &app.EnvDeployConfig{}},
	}}
	processEnv, err := appProcessEnv(root, cfg, nil, "json", "production")
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
	t.Parallel()

	root := t.TempDir()
	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local": {Default: true, Libraries: map[string]app.EnvLibraryConfig{
			"maps3d": {Linkage: "shared", Manifest: "artifacts/maps3d.json"},
		}},
	}}
	processEnv, err := appProcessEnv(root, cfg, nil, "json", "local")
	if err != nil {
		t.Fatal(err)
	}
	if value := lookupEnvValue(processEnv, "SCENERY_LIBRARY_MAPS3D_LINKAGE"); value != "shared" {
		t.Fatalf("library linkage = %q", value)
	}
	wantManifest := filepath.Join(root, "artifacts", "maps3d.json")
	if value := lookupEnvValue(processEnv, "SCENERY_LIBRARY_MAPS3D_MANIFEST"); value != wantManifest {
		t.Fatalf("library manifest = %q, want %q", value, wantManifest)
	}
}

func TestAppEnvironmentOptionalDotenvSources(t *testing.T) {
	t.Setenv("DOTENV_PROCESS_VALUE", "from-process")
	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local":      {Default: true},
		"preview":    {},
		"production": {Deploy: &app.EnvDeployConfig{}},
	}}
	for _, name := range []string{"local", "preview", "production"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			resolved, err := cfg.ResolveEnv(name)
			if err != nil {
				t.Fatal(err)
			}
			for _, withOverlay := range []bool{false, true} {
				if withOverlay {
					if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("DOTENV_PROCESS_VALUE=from-file\nDOTENV_FILE_VALUE=from-file\n"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				if err := validateLocalSecretsFiles(root, cfg, resolved); err != nil {
					t.Fatalf("validate optional dotenv (overlay=%v): %v", withOverlay, err)
				}
				processEnv, err := appProcessEnv(root, cfg, nil, "json", name)
				if err != nil {
					t.Fatal(err)
				}
				if value := lookupEnvValue(processEnv, "DOTENV_PROCESS_VALUE"); value != "from-process" {
					t.Fatalf("process value = %q", value)
				}
				if withOverlay && lookupEnvValue(processEnv, "DOTENV_FILE_VALUE") != "from-file" {
					t.Fatal("dotenv overlay was not loaded without the base file")
				}
			}
		})
	}
}

func TestAppEnvironmentRejectsDotenvErrors(t *testing.T) {
	t.Parallel()
	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{"local": {Default: true}}}
	resolved, err := cfg.ResolveEnv("local")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{".env", ".env.local"} {
		for _, tc := range []struct {
			name    string
			content string
			want    string
		}{
			{name: "malformed line", content: "MALFORMED\n", want: "invalid .env line 1"},
			{name: "malformed quoted value", content: "VALUE=\"bad\\q\"\n", want: "parse .env line 1"},
			{name: "directory", want: "read "},
			{name: "unreadable", content: "VALUE=private\n", want: "read "},
		} {
			t.Run(name+"/"+tc.name, func(t *testing.T) {
				root := t.TempDir()
				path := filepath.Join(root, name)
				if tc.name == "directory" {
					if err := os.Mkdir(path, 0o700); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.WriteFile(path, []byte(tc.content), 0o600); err != nil {
						t.Fatal(err)
					}
					if tc.name == "unreadable" {
						if os.Geteuid() == 0 {
							t.Skip("root can read files without read permissions")
						}
						if err := os.Chmod(path, 0); err != nil {
							t.Fatal(err)
						}
					}
				}
				_, processErr := appProcessEnv(root, cfg, nil, "json", "local")
				for _, err := range []error{validateLocalSecretsFiles(root, cfg, resolved), processErr} {
					if err == nil || !strings.Contains(err.Error(), tc.want) {
						t.Fatalf("dotenv error = %v, want %q", err, tc.want)
					}
					if tc.name == "unreadable" && !errors.Is(err, os.ErrPermission) {
						t.Fatalf("dotenv error = %v, want permission error", err)
					}
				}
			})
		}
	}
}

func TestLocalSecretsValidationRequiresValuesWithoutDotenv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{Name: "demo", Auth: app.AuthConfig{
		Enabled: true, GoogleOAuth: app.AuthGoogleConfig{Enabled: true},
	}, Envs: map[string]app.EnvConfig{
		"local": {Default: true}, "production": {Deploy: &app.EnvDeployConfig{}},
	}}
	resolved, err := cfg.ResolveEnv("production")
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{"JWT_SECRET", "GOOGLE_OAUTH_CLIENT_ID", "GOOGLE_OAUTH_CLIENT_SECRET", "AUTH_TOKEN_CIPHER_KEY"}
	for _, key := range keys {
		t.Setenv(key, "provided")
	}
	if err := validateLocalSecretsFiles(root, cfg, resolved); err != nil {
		t.Fatalf("process-only secrets rejected: %v", err)
	}
	for _, key := range keys {
		t.Run(key, func(t *testing.T) {
			t.Setenv(key, " \t")
			want := "environment \"production\" is missing required secrets: " + key
			if err := validateLocalSecretsFiles(root, cfg, resolved); err == nil || err.Error() != want {
				t.Fatalf("secret validation = %v, want %q", err, want)
			}
		})
	}
}

func TestAppProcessEnvValidatesDatabaseURLWithoutDotenv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo", Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	for _, tc := range []struct{ value, want string }{
		{"", "app SQL requirements need DATABASE_URL"},
		{"not-a-postgres-url", "DATABASE_URL must be a postgres:// or postgresql:// URL"},
		{"postgres://user:private-password@host/%ZZ", "DATABASE_URL must be a postgres:// or postgresql:// URL"},
		{"postgres://user:secret@localhost/reports", ""},
	} {
		t.Setenv("DATABASE_URL", tc.value)
		processEnv, err := appProcessEnv(root, cfg, testSQLRequirements(t, "reports"), "json", "local")
		if tc.want != "" {
			if err == nil || cliExitCode(err) != 3 || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private-password") {
				t.Fatalf("database validation = %v, want %q", err, tc.want)
			}
		} else if err != nil || lookupEnvValue(processEnv, "DATABASE_URL") != tc.value {
			t.Fatalf("valid process-only database URL rejected: %v", err)
		}
	}
}
