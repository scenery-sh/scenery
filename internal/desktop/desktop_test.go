package desktop

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestRunStreamsOutputAndPreservesExitCode(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	commandPath := filepath.Join(root, "desktop-command")
	writeTestFile(t, commandPath, `#!/bin/sh
printf 'stdout:%s:%s:%s\n' "$PWD" "$1" "$DESKTOP_TEST_VALUE"
printf 'stderr:desktop failed\n' >&2
exit 7
`, 0o755)

	var output strings.Builder
	runErr := Run(context.Background(), Command{Path: commandPath, Args: []string{"build"}, Dir: root}, []string{"DESKTOP_TEST_VALUE=runner-env"}, &output)
	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) || exitErr.ExitCode() != 7 {
		t.Fatalf("error = %v, want child exit 7", runErr)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"stdout:" + resolvedRoot + ":build:runner-env",
		"stderr:desktop failed",
	} {
		if !strings.Contains(output.String(), want) || !strings.Contains(runErr.Error(), want) {
			t.Fatalf("desktop output or error missing %q:\noutput: %s\nerror: %v", want, output.String(), runErr)
		}
	}
}

func TestResolveAndCommands(t *testing.T) {
	root := t.TempDir()
	tauriRoot := filepath.Join(root, "apps", "desktop")
	writeTestFile(t, filepath.Join(tauriRoot, "src-tauri", "tauri.conf.json"), `{}`, 0o644)
	writeTestFile(t, filepath.Join(root, "node_modules", ".bin", "tauri"), "#!/bin/sh\n", 0o755)
	projects, err := Resolve(root, map[string]app.FrontendConfig{
		"web": {
			Root:  "apps/web",
			Tauri: &app.FrontendTauriConfig{Root: "apps/desktop"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 1 || projects[0].FrontendRoot != filepath.Join(root, "apps", "web") || projects[0].TauriRoot != tauriRoot {
		t.Fatalf("projects = %+v", projects)
	}
	dev, err := DevCommand(projects[0], "http://127.0.0.1:5173")
	if err != nil {
		t.Fatal(err)
	}
	assertOverlay(t, dev.Args, "devUrl", "http://127.0.0.1:5173", "beforeDevCommand")
	build, err := BuildCommand(projects[0], filepath.Join(root, "apps", "web", "dist"))
	if err != nil {
		t.Fatal(err)
	}
	assertOverlay(t, build.Args, "frontendDist", filepath.Join(root, "apps", "web", "dist"), "beforeBuildCommand")
}

func TestResolveRejectsMissingDesktopConfiguration(t *testing.T) {
	root := t.TempDir()
	if _, err := Resolve(root, nil); err == nil || !strings.Contains(err.Error(), "frontends.<name>.tauri") {
		t.Fatalf("missing declaration error = %v", err)
	}
	frontends := map[string]app.FrontendConfig{"web": {Tauri: &app.FrontendTauriConfig{}}}
	if _, err := Resolve(root, frontends); err == nil || !strings.Contains(err.Error(), "tauri.conf.json") {
		t.Fatalf("missing project error = %v", err)
	}
	writeTestFile(t, filepath.Join(root, "apps", "web", "src-tauri", "tauri.conf.json"), `{}`, 0o644)
	if _, err := Resolve(root, frontends); err == nil || !strings.Contains(err.Error(), "@tauri-apps/cli") {
		t.Fatalf("missing CLI error = %v", err)
	}
}

func assertOverlay(t *testing.T, args []string, valueKey, value, emptyKey string) {
	t.Helper()
	if len(args) != 3 || args[1] != "--config" {
		t.Fatalf("args = %#v", args)
	}
	var overlay struct {
		Build map[string]any `json:"build"`
	}
	if err := json.Unmarshal([]byte(args[2]), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Build[valueKey] != value || overlay.Build[emptyKey] != "" || len(overlay.Build) != 2 {
		t.Fatalf("overlay = %#v", overlay.Build)
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
