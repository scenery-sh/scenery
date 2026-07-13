package generate

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
)

func parallelIntegrationTest(t *testing.T) {
	t.Helper()
	t.Parallel()
}

func hasDiagnostic(diagnostics []compiler.Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}

func copyTree(t *testing.T, source, target string) {
	t.Helper()
	if err := filepath.WalkDir(source, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, contents, 0o644)
	}); err != nil {
		t.Fatal(err)
	}
}

func rewriteFixtureSceneryReplace(t *testing.T, root string) {
	t.Helper()
	workingDirectory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repositoryRoot := filepath.Clean(filepath.Join(workingDirectory, "..", ".."))
	path := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), "replace scenery.sh => ../../../..", "replace scenery.sh => "+filepath.ToSlash(repositoryRoot), 1))
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func boundedGoCommand(arguments ...string) *exec.Cmd {
	command := exec.Command("go", arguments...)
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GOMAXPROCS=") {
			command.Env = append(command.Env, entry)
		}
	}
	command.Env = append(command.Env, "GOMAXPROCS="+strconv.Itoa(min(runtime.GOMAXPROCS(0), 2)))
	return command
}
