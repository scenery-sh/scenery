package runtime

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestPopulateSecretsFromDotEnv(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeFile(t, dir, ".env", "JWTSecret=top-secret\nDatabaseURL=\"postgres://localhost/db\"\n")

	prevDir, restoreDir := chdirRuntimeTest(t, dir)
	defer restoreDir(prevDir)
	resetSecretsEnvCache()

	var secrets struct {
		JWTSecret   string
		DatabaseURL string
	}
	if err := PopulateSecrets(&secrets); err != nil {
		t.Fatalf("PopulateSecrets returned error: %v", err)
	}
	if secrets.JWTSecret != "top-secret" {
		t.Fatalf("JWTSecret = %q, want %q", secrets.JWTSecret, "top-secret")
	}
	if secrets.DatabaseURL != "postgres://localhost/db" {
		t.Fatalf("DatabaseURL = %q, want %q", secrets.DatabaseURL, "postgres://localhost/db")
	}
}

func TestPopulateSecretsUsesEnvironmentOverrideAndSnakeCase(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeFile(t, dir, ".env", "DatabaseURL=from-file\n")

	prevDir, restoreDir := chdirRuntimeTest(t, dir)
	defer restoreDir(prevDir)
	t.Setenv("DATABASE_URL", "from-env")
	resetSecretsEnvCache()

	var secrets struct {
		DatabaseURL string
	}
	if err := PopulateSecrets(&secrets); err != nil {
		t.Fatalf("PopulateSecrets returned error: %v", err)
	}
	if secrets.DatabaseURL != "from-env" {
		t.Fatalf("DatabaseURL = %q, want %q", secrets.DatabaseURL, "from-env")
	}
}

func TestLoadDotEnvIntoEnvAddsMissingValuesWithoutOverridingEnvironment(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeFile(t, dir, ".env", "Present=from-file\nMissing=from-file\n")
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(oldwd)
		resetSecretsEnvCache()
	})
	t.Setenv("Present", "from-env")
	t.Setenv("Missing", "")
	if err := os.Unsetenv("Missing"); err != nil {
		t.Fatal(err)
	}
	resetSecretsEnvCache()

	if err := LoadDotEnvIntoEnv(); err != nil {
		t.Fatalf("LoadDotEnvIntoEnv: %v", err)
	}
	if got := os.Getenv("Present"); got != "from-env" {
		t.Fatalf("Present = %q, want from-env", got)
	}
	if got := os.Getenv("Missing"); got != "from-file" {
		t.Fatalf("Missing = %q, want from-file", got)
	}
}

func TestPopulateSecretsRejectsNonStringFields(t *testing.T) {
	resetSecretsEnvCache()

	var secrets struct {
		Enabled bool
	}
	if err := PopulateSecrets(&secrets); err == nil {
		t.Fatal("PopulateSecrets returned nil error for non-string field")
	}
}

func TestPopulateSecretsLogsMissingFields(t *testing.T) {
	dir := t.TempDir()
	writeRuntimeFile(t, dir, ".env", "PresentSecret=top-secret\n")

	prevDir, restoreDir := chdirRuntimeTest(t, dir)
	defer restoreDir(prevDir)
	resetSecretsEnvCache()

	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prevLogger)

	var secrets struct {
		PresentSecret string
		MissingSecret string
	}
	if err := PopulateSecrets(&secrets); err != nil {
		t.Fatalf("PopulateSecrets returned error: %v", err)
	}
	if secrets.PresentSecret != "top-secret" {
		t.Fatalf("PresentSecret = %q, want %q", secrets.PresentSecret, "top-secret")
	}
	FlushMissingSecretsWarnings()
	gotLogs := logs.String()
	for _, want := range []string{"onlava secrets missing", "MissingSecret", "MISSING_SECRET"} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs %q do not contain %q", gotLogs, want)
		}
	}
}

func TestPopulateSecretsFailsForMissingProductionSecrets(t *testing.T) {
	dir := t.TempDir()

	prevDir, restoreDir := chdirRuntimeTest(t, dir)
	defer restoreDir(prevDir)
	t.Setenv("ONLAVA_RUNTIME_ENV", "production")
	resetSecretsEnvCache()

	var secrets struct {
		MissingSecret string
	}
	err := PopulateSecrets(&secrets)
	if err == nil {
		t.Fatal("PopulateSecrets returned nil error for missing production secret")
	}
	got := err.Error()
	for _, want := range []string{"missing required secrets for production", "MissingSecret", "MISSING_SECRET"} {
		if !strings.Contains(got, want) {
			t.Fatalf("error %q does not contain %q", got, want)
		}
	}
}

func TestFlushMissingSecretsWarningsCombinesFields(t *testing.T) {
	dir := t.TempDir()

	prevDir, restoreDir := chdirRuntimeTest(t, dir)
	defer restoreDir(prevDir)
	resetSecretsEnvCache()

	var logs bytes.Buffer
	prevLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	defer slog.SetDefault(prevLogger)

	var first struct {
		ResendAPIKey string
	}
	var second struct {
		ElectricURL string
	}
	if err := PopulateSecrets(&first); err != nil {
		t.Fatalf("PopulateSecrets returned error: %v", err)
	}
	if err := PopulateSecrets(&second); err != nil {
		t.Fatalf("PopulateSecrets returned error: %v", err)
	}
	FlushMissingSecretsWarnings()

	gotLogs := logs.String()
	if strings.Count(gotLogs, "onlava secrets missing") != 1 {
		t.Fatalf("expected single warning, got logs %q", gotLogs)
	}
	for _, want := range []string{"ResendAPIKey", "ElectricURL"} {
		if !strings.Contains(gotLogs, want) {
			t.Fatalf("logs %q do not contain %q", gotLogs, want)
		}
	}
}

func resetSecretsEnvCache() {
	secretsEnvOnce = sync.Once{}
	secretsEnvData = nil
	secretsEnvErr = nil
	secretsWarnedFields = nil
	secretsPendingKeys = nil
	secretsFlushed = false
}

func chdirRuntimeTest(t *testing.T, dir string) (string, func(string)) {
	t.Helper()
	prevDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	return prevDir, func(path string) {
		t.Helper()
		if err := os.Chdir(path); err != nil {
			t.Fatal(err)
		}
	}
}

func writeRuntimeFile(t *testing.T, root, rel, data string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
}
