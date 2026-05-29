package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pbrazdil/onlava/internal/build"
)

func TestParseCheckArgs(t *testing.T) {
	t.Parallel()

	opts, err := parseCheckArgs([]string{"--app-root", "/tmp/app", "--json"})
	if err != nil {
		t.Fatalf("parseCheckArgs returned error: %v", err)
	}
	if opts.AppRoot != "/tmp/app" {
		t.Fatalf("parseCheckArgs app root = %q", opts.AppRoot)
	}
	if !opts.JSON {
		t.Fatal("expected --json to be true")
	}
}

func TestRunOnlavaCheckCompilesApp(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"checkapp"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/checkapp\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaCheck(context.Background(), &out, nil); err != nil {
		t.Fatalf("runOnlavaCheck returned error: %v", err)
	}
	if strings.TrimSpace(out.String()) != "onlava: check ok" {
		t.Fatalf("stdout = %q", out.String())
	}

	matches, err := filepath.Glob(filepath.Join(cacheRoot, "build", "checkapp-*", "onlava-app"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) == 0 {
		t.Fatal("expected compiled workspace binary for onlava check")
	}
}

func TestRunOnlavaCheckJSONSuccess(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"checkjson","id":"check-id"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/checkjson\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaCheck(context.Background(), &out, []string{"--json"}); err != nil {
		t.Fatalf("runOnlavaCheck(--json) returned error: %v", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		App           struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		} `json:"app"`
		Diagnostics []any `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(success): %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.check.result.v1" || !payload.OK {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.App.Name != "checkjson" || payload.App.ID != "check-id" {
		t.Fatalf("app = %+v", payload.App)
	}
	if len(payload.Diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want empty", payload.Diagnostics)
	}
}

func TestRunOnlavaCheckJSONReportsTypeScriptTemporalContractFailure(t *testing.T) {
	t.Parallel()

	root := persistentTestAppRoot(t, "check-ts-contract")
	preparePersistentTestApp(t, root, map[string]string{
		".onlava.json": `{"name":"checkts"}`,
		"go.mod":       "module example.com/checkts\n\ngo 1.26.0\n\nrequire github.com/pbrazdil/onlava v0.0.0\n\nreplace github.com/pbrazdil/onlava => " + repoRootForTest(t) + "\n",
		"svc/api.go":   "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n",
		"jobs/runtime.go": `package jobs

import "github.com/pbrazdil/onlava/temporal"

type RenderInput struct{}
type RenderOutput struct{}

var _ = temporal.NewExternalActivity[*RenderInput, *RenderOutput]("house.Render/v1", temporal.ActivityConfig{TaskQueue: "onlv.house.preview.ts"})
`,
		"house/preview.worker.ts": `import { activity } from "onlava/worker";
export type RenderInput = { id: string };
export type RenderOutput = { url: string };
export const render = activity<RenderInput, RenderOutput>({
  name: "house.Render/v1",
  taskQueue: "wrong.queue.ts"
}, async (_ctx, input) => ({ url: input.id }));
`,
	})

	var out bytes.Buffer
	err := runOnlavaCheck(context.Background(), &out, []string{"--app-root", root, "--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("expected silent TypeScript contract error, got %v", err)
	}
	var payload checkResponse
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal: %v\n%s", err, out.String())
	}
	if payload.OK || len(payload.Diagnostics) == 0 {
		t.Fatalf("payload = %+v", payload)
	}
	if payload.Diagnostics[0].Stage != "temporal-typescript" || !strings.Contains(payload.Diagnostics[0].Message, "wrong.queue.ts") {
		t.Fatalf("diagnostic = %+v", payload.Diagnostics[0])
	}
}

func TestRunOnlavaCheckReusesFreshCompiledBuild(t *testing.T) {
	useFakeBuildGoRunner(t)

	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"checkcache"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/checkcache\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaCheck(context.Background(), &out, []string{"--json"}); err != nil {
		t.Fatalf("initial runOnlavaCheck returned error: %v", err)
	}
	manifest, ok, err := build.ReadLatestBuildManifest(root)
	if err != nil {
		t.Fatalf("ReadLatestBuildManifest: %v", err)
	}
	if !ok {
		t.Fatal("expected latest build manifest")
	}
	if manifest.Build.GraphFingerprint == "" {
		t.Fatalf("expected check to persist graph fingerprint: manifest=%+v", manifest.Build)
	}
	sentinel := time.Now().Add(-2 * time.Hour).Round(time.Second)
	if err := os.Chtimes(manifest.Build.BinaryPath, sentinel, sentinel); err != nil {
		t.Fatalf("Chtimes(binary): %v", err)
	}

	out.Reset()
	if err := runOnlavaCheck(context.Background(), &out, []string{"--json"}); err != nil {
		t.Fatalf("cached runOnlavaCheck returned error: %v", err)
	}
	info, err := os.Stat(manifest.Build.BinaryPath)
	if err != nil {
		t.Fatalf("Stat(binary): %v", err)
	}
	if !info.ModTime().Equal(sentinel) {
		t.Fatalf("binary modtime changed; check did not reuse compiled build: got %s want %s", info.ModTime(), sentinel)
	}
}

func TestRunOnlavaCheckRecompilesAfterSourceChange(t *testing.T) {
	restoreRunner := build.SetGoRunnerForTesting(func(_ context.Context, dir string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if len(args) >= 4 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" {
			source, err := os.ReadFile(filepath.Join(dir, "svc", "api.go"))
			if err != nil {
				return err
			}
			if strings.Contains(string(source), "MissingSymbol") {
				return errors.New("svc/api.go:6:37: undefined: MissingSymbol")
			}
			out := args[3]
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			return os.WriteFile(out, []byte("#!/bin/sh\nexit 0\n"), 0o755)
		}
		return errors.New("unexpected fake go command: " + strings.Join(args, " "))
	})
	t.Cleanup(restoreRunner)

	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"checkchanged"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/checkchanged\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return nil }\n")

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	if err := runOnlavaCheck(context.Background(), &out, []string{"--json"}); err != nil {
		t.Fatalf("initial runOnlavaCheck returned error: %v", err)
	}
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return MissingSymbol }\n")

	out.Reset()
	err := runOnlavaCheck(context.Background(), &out, []string{"--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("expected changed source to be recompiled, got %v", err)
	}
	if !strings.Contains(out.String(), "undefined: MissingSymbol") {
		t.Fatalf("expected compile diagnostic after source change, got %s", out.String())
	}
}

func TestRunOnlavaCheckJSONCompileFailure(t *testing.T) {
	restoreRunner := build.SetGoRunnerForTesting(func(_ context.Context, _ string, args ...string) error {
		if len(args) >= 2 && args[0] == "mod" && args[1] == "tidy" {
			return nil
		}
		if len(args) >= 4 && args[0] == "build" && args[1] == "-buildvcs=false" && args[2] == "-o" {
			return errors.New("go build -buildvcs=false failed: exit status 1\nsvc/api.go:6:37: undefined: MissingSymbol")
		}
		return errors.New("unexpected fake go command: " + strings.Join(args, " "))
	})
	t.Cleanup(restoreRunner)

	root := t.TempDir()
	cacheRoot := filepath.Join(t.TempDir(), "cache")
	t.Setenv("ONLAVA_DEV_CACHE_DIR", cacheRoot)
	writeTestAppFile(t, root, ".onlava.json", `{"name":"checkfail"}`)
	writeTestAppFile(t, root, "go.mod", "module example.com/checkfail\n\ngo 1.26.0\n")
	writeTestAppFile(t, root, "svc/api.go", "package svc\n\nimport \"context\"\n\n//onlava:api public\nfunc Ping(context.Context) error { return MissingSymbol }\n")

	restore := chdirForTest(t, root)
	defer restore()

	var out bytes.Buffer
	err := runOnlavaCheck(context.Background(), &out, []string{"--json"})
	var silent *silentCLIError
	if !errors.As(err, &silent) {
		t.Fatalf("expected silentCLIError, got %v", err)
	}
	var payload struct {
		SchemaVersion string `json:"schema_version"`
		OK            bool   `json:"ok"`
		Diagnostics   []struct {
			Stage           string `json:"stage"`
			Severity        string `json:"severity"`
			File            string `json:"file"`
			Line            int    `json:"line"`
			Column          int    `json:"column"`
			Message         string `json:"message"`
			SuggestedAction string `json:"suggested_action"`
		} `json:"diagnostics"`
	}
	if err := json.Unmarshal(out.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal(failure): %v\n%s", err, out.String())
	}
	if payload.SchemaVersion != "onlava.check.result.v1" || payload.OK {
		t.Fatalf("payload = %+v", payload)
	}
	if len(payload.Diagnostics) == 0 {
		t.Fatalf("expected diagnostics, got none: %s", out.String())
	}
	first := payload.Diagnostics[0]
	if first.Stage != "compile" || first.Severity != "error" {
		t.Fatalf("first diagnostic = %+v", first)
	}
	if first.File != "svc/api.go" || first.Line == 0 {
		t.Fatalf("expected file/line in diagnostic, got %+v", first)
	}
	if !strings.Contains(first.Message, "undefined: MissingSymbol") {
		t.Fatalf("message = %q", first.Message)
	}
	if first.SuggestedAction == "" {
		t.Fatal("expected suggested action")
	}
}
