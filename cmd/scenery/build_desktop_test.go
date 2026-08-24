package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/app"
	"scenery.sh/internal/desktop"
)

func TestBuildDesktopBuildsFrontendAndTauriBundle(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	frontendRoot := filepath.Join(root, "apps", "web")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "package.json"), `{"scripts":{"build":"vite build"}}`)
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "tauri.conf.json"), `{}`)
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "node_modules", ".bin", "vite"), "")
	writeDesktopTestFile(t, filepath.Join(frontendRoot, "node_modules", ".bin", "tauri"), "")
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
	type invocation struct {
		command desktop.Command
		env     []string
	}
	var invocations []invocation
	run := func(_ context.Context, command desktop.Command, env []string, _ io.Writer) error {
		invocations = append(invocations, invocation{
			command: desktop.Command{Path: command.Path, Args: append([]string(nil), command.Args...), Dir: command.Dir},
			env:     append([]string(nil), env...),
		})
		switch filepath.Base(command.Path) {
		case "vite":
			writeDesktopTestFile(t, filepath.Join(frontendRoot, "dist", "index.html"), "<html>desktop</html>")
		case "tauri":
			writeDesktopTestFile(t, filepath.Join(frontendRoot, "src-tauri", "target", "release", "bundle", "dmg", "desktop.dmg"), "bundle")
		default:
			t.Fatalf("unexpected desktop command: %+v", command)
		}
		return nil
	}
	var commandOutput bytes.Buffer
	result, err := buildDesktopWithRunner(context.Background(), root, cfg, env, &commandOutput, run)
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

	if len(invocations) != 2 {
		t.Fatalf("desktop invocations = %#v", invocations)
	}
	frontendInvocation := invocations[0]
	if filepath.Base(frontendInvocation.command.Path) != "vite" || frontendInvocation.command.Dir != frontendRoot || strings.Join(frontendInvocation.command.Args, " ") != "build" {
		t.Fatalf("frontend invocation = %#v", frontendInvocation)
	}
	if got := envValueFromList(frontendInvocation.env, "VITE_API_BASE_URL"); got != "https://desktop.example.com" {
		t.Fatalf("frontend API = %q", got)
	}
	tauriInvocation := invocations[1]
	if filepath.Base(tauriInvocation.command.Path) != "tauri" || tauriInvocation.command.Dir != frontendRoot || len(tauriInvocation.command.Args) != 3 || tauriInvocation.command.Args[0] != "build" || tauriInvocation.command.Args[1] != "--config" {
		t.Fatalf("tauri invocation = %#v", tauriInvocation)
	}
	var overlay struct {
		Build struct {
			FrontendDist       string `json:"frontendDist"`
			BeforeBuildCommand string `json:"beforeBuildCommand"`
		} `json:"build"`
	}
	if err := json.Unmarshal([]byte(tauriInvocation.command.Args[2]), &overlay); err != nil {
		t.Fatal(err)
	}
	if overlay.Build.FrontendDist != filepath.Join(frontendRoot, "dist") || overlay.Build.BeforeBuildCommand != "" {
		t.Fatalf("overlay = %+v", overlay)
	}
	if got := envValueFromList(tauriInvocation.env, "SCENERY_ENV"); got != "production" {
		t.Fatalf("SCENERY_ENV = %q", got)
	}

	payload := desktopBuildPayload(result)
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.build.desktop.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
		t.Fatalf("desktop build payload diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
}

func TestBuildDesktopRejectsConflictingFlags(t *testing.T) {
	t.Parallel()

	for _, args := range [][]string{
		{"--desktop", "--target", "development"},
		{"--desktop", "--lib", "geometry"},
		{"--desktop", "--output", "dist"},
	} {
		if err := buildCommand(io.Discard, args); err == nil || !strings.Contains(err.Error(), "--desktop cannot be combined") {
			t.Fatalf("buildCommand(%v) error = %v", args, err)
		}
	}
	if err := buildCommand(io.Discard, []string{"--env", "production"}); err == nil || !strings.Contains(err.Error(), "--env is only supported") {
		t.Fatalf("non-desktop --env error = %v", err)
	}
}

func TestBuildDesktopCommandEmitsSchemaValidJSON(t *testing.T) {
	t.Parallel()

	payload := desktopBuildPayload(desktopBuildResult{
		Environment: "local",
		Frontends: []desktopFrontendBuildResult{{
			Name:         "web",
			TauriRoot:    "/tmp/apps/web/src-tauri",
			FrontendDist: "/tmp/apps/web/dist",
			Artifacts:    []string{"/tmp/apps/web/src-tauri/target/release/bundle/dmg/app.dmg"},
		}},
	})
	schemaPath := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.build.desktop.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(schemaPath, payload); len(diagnostics) != 0 {
		t.Fatalf("desktop build payload diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
	var stdout bytes.Buffer
	if err := writeCLIJSON(&stdout, payload); err != nil {
		t.Fatal(err)
	}
	var envelope map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	cliSchema := filepath.Join(repoRootForTest(t), "docs", "schemas", "scenery.cli.schema.json")
	if diagnostics := validateHarnessJSONSchemaFile(cliSchema, envelope); len(diagnostics) != 0 {
		t.Fatalf("desktop build CLI envelope diagnostics = %s", strings.Join(diagnostics, "\n"))
	}
}
