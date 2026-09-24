package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

// A dotenv file in the app root is not a configuration source: none of its
// names reaches an application process, in any environment.
func TestPoisonedDotenvNeverReachesApplicationProcesses(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{".env", ".env.local", ".env.production", ".env.production.local"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("POISONED_DOTENV_VALUE=from-file\nPOISONED_SIGNING_VALUE=from-file\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := app.Config{Name: "demo", Envs: map[string]app.EnvConfig{
		"local":      {Default: true},
		"production": {Deploy: &app.EnvDeployConfig{}},
	}}
	for _, name := range []string{"local", "production"} {
		processEnv, err := appProcessEnv(root, cfg, nil, "json", name)
		if err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(processEnv, "\n")
		if strings.Contains(joined, "from-file") {
			t.Fatalf("%s process environment contains dotenv values", name)
		}
		if !strings.Contains(joined, "SCENERY_ENV="+name) || !strings.Contains(joined, "SCENERY_RUNTIME_ENV="+name) {
			t.Fatalf("environment identity missing: %s", joined)
		}
	}
}

func TestAppProcessEnvValidatesDatabaseURLWithoutDotenv(t *testing.T) {
	root := t.TempDir()
	cfg := app.Config{
		Name: "demo", Envs: map[string]app.EnvConfig{"local": {Default: true}},
	}
	for _, tc := range []struct{ value, want string }{
		{"", "app SQL requirements need an external database"},
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
