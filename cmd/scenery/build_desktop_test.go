package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
)

func TestBuildDesktopBuildsFrontendAndTauriBundle(t *testing.T) {
	root := t.TempDir()
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "package.json"), `{"scripts":{"build":"vite build"}}`)
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	frontendMarker := filepath.Join(root, "frontend-build.json")
	tauriMarker := filepath.Join(root, "tauri-build.json")
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "vite"), `#!/bin/sh
set -eu
mkdir -p dist
printf '<html>desktop</html>' > dist/index.html
printf '%s\n%s\n' "$1" "$VITE_API_BASE_URL" > "$FRONTEND_BUILD_MARKER"
`)
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), `#!/bin/sh
set -eu
mkdir -p src-tauri/target/release/bundle/dmg
printf 'bundle' > src-tauri/target/release/bundle/dmg/desktop.dmg
printf '%s\n%s\n%s\n%s\n%s\n' "$PWD" "$1" "$2" "$3" "$SCENERY_ENV" > "$TAURI_BUILD_MARKER"
`)
	t.Setenv("FRONTEND_BUILD_MARKER", frontendMarker)
	t.Setenv("TAURI_BUILD_MARKER", tauriMarker)
	cfg := app.Config{
		Name: "desktop-demo",
		Frontends: map[string]app.FrontendConfig{
			"web": {Root: "apps/web", Tauri: &app.FrontendTauriConfig{}},
		},
		Envs: map[string]app.EnvConfig{
			"local":      {Default: true},
			"production": {Domain: "desktop.example.com", Deploy: &app.EnvDeployConfig{}},
		},
	}
	env, err := cfg.ResolveEnv("production")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Frontends = env.Frontends
	var commandOutput bytes.Buffer
	result, err := buildDesktop(context.Background(), root, cfg, env, &commandOutput)
	if err != nil {
		t.Fatalf("buildDesktop: %v\n%s", err, commandOutput.String())
	}
	if result.Environment != "production" || len(result.Frontends) != 1 {
		t.Fatalf("result = %+v", result)
	}
	frontend := result.Frontends[0]
	wantArtifact := filepath.Join(frontendRoot, "src-tauri", "target", "release", "bundle", "dmg", "desktop.dmg")
	if len(frontend.Artifacts) != 1 || frontend.Artifacts[0] != wantArtifact {
		t.Fatalf("artifacts = %#v, want %q", frontend.Artifacts, wantArtifact)
	}

	frontendInvocation := readDesktopTestLines(t, frontendMarker)
	if len(frontendInvocation) != 2 || frontendInvocation[0] != "build" {
		t.Fatalf("frontend invocation = %#v", frontendInvocation)
	}
	if frontendInvocation[1] != "https://desktop.example.com" {
		t.Fatalf("frontend API = %q", frontendInvocation[1])
	}
	tauriInvocation := readDesktopTestLines(t, tauriMarker)
	if len(tauriInvocation) != 5 || tauriInvocation[1] != "build" || tauriInvocation[2] != "--config" {
		t.Fatalf("tauri invocation = %#v", tauriInvocation)
	}
	var overlay struct {
		Build struct {
			FrontendDist       string `json:"frontendDist"`
			BeforeBuildCommand string `json:"beforeBuildCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(tauriInvocation[3]), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Build.FrontendDist != filepath.Join(frontendRoot, "dist") || overlay.Build.BeforeBuildCommand != "" {
		t.Fatalf("overlay = %+v", overlay)
	}
	if tauriInvocation[4] != "production" {
		t.Fatalf("SCENERY_ENV = %q", tauriInvocation[4])
	}

	payload := desktopBuildPayload(result)
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.build.desktop.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
		t.Fatalf("desktop build payload diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
}

func TestBuildDesktopRejectsConflictingFlags(t *testing.T) {
	for _, args := range [][]string{
		{"--desktop", "--target", "development"},
		{"--desktop", "--lib", "geometry"},
		{"--desktop", "--output", "dist"},
	} {
		if err := buildCommand(args); err == nil || !strings.Contains(err.Error(), "--desktop cannot be combined") {
			t.Fatalf("buildCommand(%v) error = %v", args, err)
		}
	}
	if err := buildCommand([]string{"--env", "production"}); err == nil || !strings.Contains(err.Error(), "--env is only supported") {
		t.Fatalf("non-desktop --env error = %v", err)
	}
}

func TestBuildDesktopCommandEmitsSchemaValidJSON(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(repoRootForTest(t), "internal", "compiler", "testdata", "native")
	if err := os.CopyFS(root, os.DirFS(fixture)); err != nil {
		t.Fatal(err)
	}
	goModPath := filepath.Join(root, "go.mod")
	goMod, err := os.ReadFile(goModPath)
	if err != nil {
		t.Fatal(err)
	}
	goMod = []byte(strings.Replace(string(goMod), "replace scenery.sh => ../../../..", "replace scenery.sh => "+repoRootForTest(t), 1))
	if err := os.WriteFile(goModPath, goMod, 0o644); err != nil {
		t.Fatal(err)
	}
	writeDesktopTestFile(t, filepath.Join(root, ".scenery.json"), `{
		"name": "nativeapp",
		"frontends": {
			"web": {"root": "apps/web", "tauri": {}}
		},
		"envs": {"local": {"default": true}}
	}`)
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "package.json"), `{"scripts":{"build":"vite build"}}`)
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "vite"), `#!/bin/sh
set -eu
mkdir -p dist
printf '<html>desktop</html>' > dist/index.html
`)
	writeDesktopTestExecutable(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), `#!/bin/sh
set -eu
mkdir -p src-tauri/target/release/bundle/dmg
printf 'bundle' > src-tauri/target/release/bundle/dmg/nativeapp.dmg
`)
	output := captureStdout(t, func() error {
		return buildCommand([]string{"--desktop", "--app-root", root, "-o", "json"})
	})
	var envelope struct {
		OK   bool           `json:"ok"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(output), &envelope); err != nil {
		t.Fatalf("decode output: %v\n%s", err, output)
	}
	if !envelope.OK {
		t.Fatalf("build envelope = %s", output)
	}
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.build.desktop.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, envelope.Data); len(diagnostics) != 0 {
		t.Fatalf("desktop build command payload diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
}

func readDesktopTestLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}
