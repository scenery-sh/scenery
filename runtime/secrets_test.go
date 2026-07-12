package runtime

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestLoadDotEnvIntoEnvAddsMissingValuesWithoutOverridingEnvironment(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("Present=from-file\nMissing=from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	oldwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldwd) })
	t.Setenv("Present", "from-env")
	_ = os.Unsetenv("Missing")
	dotEnvOnce, dotEnvData, dotEnvErr = sync.Once{}, nil, nil

	if err := LoadDotEnvIntoEnv(); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("Present"); got != "from-env" {
		t.Fatalf("Present = %q, want from-env", got)
	}
	if got := os.Getenv("Missing"); got != "from-file" {
		t.Fatalf("Missing = %q, want from-file", got)
	}
}
