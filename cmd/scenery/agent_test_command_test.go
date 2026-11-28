package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/graph"
)

func TestRunBatchedGoModuleTestsIsolatesFailures(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module example.test/batched\n\ngo 1.23\n",
		"alpha/alpha_test.go": `package alpha
import "testing"
func TestAlpha(t *testing.T) {}
`,
		"beta/beta_test.go": `package beta
import "testing"
func TestBeta(t *testing.T) { t.Fatal("beta boom") }
`,
		"gamma/gamma_test.go": `package gamma
import "testing"
func TestGamma(t *testing.T) {}
`,
	}
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	g := agentGraph{Result: &compiler.Result{Root: root}}
	modules := []agentModule{
		{Name: "alpha", Directory: "alpha", Runtime: "go"},
		{Name: "beta", Directory: "beta", Runtime: "go"},
		{Name: "gamma", Directory: "gamma", Runtime: "go"},
	}
	results := make([]moduleTestResult, len(modules))
	batched := make([]bool, len(modules))
	runBatchedGoModuleTests(context.Background(), g, modules, envpolicy.Environ(), results, batched)

	for i := range modules {
		if !batched[i] {
			t.Fatalf("module %s was not batched", modules[i].Name)
		}
	}
	if !results[0].OK {
		t.Fatalf("alpha should pass, got output: %s", results[0].Output)
	}
	if results[1].OK || !strings.Contains(results[1].Output, "beta boom") {
		t.Fatalf("beta should fail with 'beta boom', got ok=%v output=%q", results[1].OK, results[1].Output)
	}
	if !results[2].OK {
		t.Fatalf("gamma should pass, got output: %s", results[2].Output)
	}
}

func testDependencyGraphFixture() (agentGraph, []agentModule) {
	// Dependency chain: gamma -> beta -> alpha; delta is independent.
	manifest := &graph.Manifest{
		Resources: []graph.Resource{
			{
				Address: "alpha/binding/get",
				Kind:    "scenery.binding",
				Name:    "get",
				Module:  "alpha",
				Spec:    map[string]any{"protocol": "internal"},
			},
			{
				Address: "beta/service/beta",
				Kind:    "scenery.service",
				Name:    "beta",
				Module:  "beta",
				Spec: map[string]any{
					"client": []any{
						map[string]any{"name": "alpha", "binding": map[string]any{"$ref": "alpha/binding/get"}},
					},
				},
			},
			{
				Address: "beta/binding/process",
				Kind:    "scenery.binding",
				Name:    "process",
				Module:  "beta",
				Spec:    map[string]any{"protocol": "internal"},
			},
			{
				Address: "gamma/service/gamma",
				Kind:    "scenery.service",
				Name:    "gamma",
				Module:  "gamma",
				Spec: map[string]any{
					"client": []any{
						map[string]any{"name": "beta", "binding": map[string]any{"$ref": "beta/binding/process"}},
					},
				},
			},
			{
				Address: "delta/service/delta",
				Kind:    "scenery.service",
				Name:    "delta",
				Module:  "delta",
				Spec:    map[string]any{},
			},
		},
	}
	g := agentGraph{Result: &compiler.Result{Manifest: manifest}}
	modules := []agentModule{
		{Name: "alpha", Directory: "alpha", Runtime: "go"},
		{Name: "beta", Directory: "beta", Runtime: "go"},
		{Name: "delta", Directory: "delta", Runtime: "go"},
		{Name: "gamma", Directory: "gamma", Runtime: "go"},
	}
	return g, modules
}

func TestChangedSelectDirectModule(t *testing.T) {
	t.Parallel()
	g, modules := testDependencyGraphFixture()
	selected, sel := selectChangedTestModules(g, modules, []string{"delta/handler.go"}, "")
	if sel.FallbackAll {
		t.Fatalf("unexpected fallback: %+v", sel)
	}
	want := []string{"delta"}
	if !slices.Equal(sel.DirectModules, want) || !slices.Equal(sel.AffectedModules, want) || len(selected) != 1 || selected[0].Name != "delta" {
		t.Fatalf("got selected=%v selection=%+v, want %v", selected, sel, want)
	}
}

func TestChangedSelectTransitiveReverseDependencies(t *testing.T) {
	t.Parallel()
	g, modules := testDependencyGraphFixture()
	selected, sel := selectChangedTestModules(g, modules, []string{"alpha/service.go"}, "")
	if sel.FallbackAll {
		t.Fatalf("unexpected fallback: %+v", sel)
	}
	wantDirect := []string{"alpha"}
	wantAffected := []string{"alpha", "beta", "gamma"}
	if !slices.Equal(sel.DirectModules, wantDirect) {
		t.Fatalf("direct = %v, want %v", sel.DirectModules, wantDirect)
	}
	if !slices.Equal(sel.AffectedModules, wantAffected) {
		t.Fatalf("affected = %v, want %v", sel.AffectedModules, wantAffected)
	}
	gotNames := make([]string, len(selected))
	for i, m := range selected {
		gotNames[i] = m.Name
	}
	if !slices.Equal(gotNames, wantAffected) {
		t.Fatalf("selected modules = %v, want %v", gotNames, wantAffected)
	}
}

func TestChangedSelectNoChanges(t *testing.T) {
	t.Parallel()
	g, modules := testDependencyGraphFixture()
	selected, sel := selectChangedTestModules(g, modules, nil, "")
	if len(selected) != 0 || len(sel.Files) != 0 || len(sel.AffectedModules) != 0 || sel.FallbackAll {
		t.Fatalf("expected empty selection, got selected=%v selection=%+v", selected, sel)
	}
}

func TestChangedSelectSharedRootAndUnownedFallback(t *testing.T) {
	t.Parallel()
	g, modules := testDependencyGraphFixture()
	for _, path := range []string{"app.scn", "go.mod", "pkg/shared/util.go"} {
		selected, sel := selectChangedTestModules(g, modules, []string{path}, "")
		if !sel.FallbackAll || !slices.Equal(sel.FallbackFiles, []string{path}) {
			t.Fatalf("path %q: expected fallback_all with [%s], got %+v", path, path, sel)
		}
		if len(selected) != len(modules) {
			t.Fatalf("path %q: selected %d modules, want all %d", path, len(selected), len(modules))
		}
	}
}

func initTestGitRepo(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.com")
	runGit("config", "commit.gpgsign", "false")
	runGit("add", ".")
	runGit("commit", "-m", "initial")
}

func TestChangedGitWorkingTreeAndUntrackedFile(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initTestGitRepo(t, root, map[string]string{
		"go.mod":         "module example.test/changed\n\ngo 1.23\n",
		"alpha/alpha.go": "package alpha\n",
		"beta/beta.go":   "package beta\n",
	})

	// Clean working tree -> no changed files.
	files, err := gitChangedFiles(context.Background(), root, "")
	if err != nil {
		t.Fatalf("gitChangedFiles clean: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("clean repo changed files = %v, want empty", files)
	}

	// Untracked file inside module alpha.
	untracked := filepath.Join(root, "alpha", "new_test.go")
	if err := os.WriteFile(untracked, []byte("package alpha\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	files, err = gitChangedFiles(context.Background(), root, "")
	if err != nil {
		t.Fatalf("gitChangedFiles untracked: %v", err)
	}
	if !slices.Equal(files, []string{"alpha/new_test.go"}) {
		t.Fatalf("untracked changed files = %v, want [alpha/new_test.go]", files)
	}

	g, modules := testDependencyGraphFixture()
	selected, sel := selectChangedTestModules(g, modules, files, "")
	want := []string{"alpha", "beta", "gamma"}
	if len(selected) != len(want) || !slices.Equal(sel.AffectedModules, want) {
		t.Fatalf("untracked selection = %+v, want %v", sel, want)
	}
}

func TestChangedFlagValidation(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	if err := runAgentTests(context.Background(), &buf, []string{"--changed-base", "HEAD"}); err == nil || !strings.Contains(err.Error(), "--changed-base requires --changed") {
		t.Fatalf("expected --changed-base requires --changed, got %v", err)
	}
	if err := runAgentTests(context.Background(), &buf, []string{"--changed", "--changed-base="}); err == nil || !strings.Contains(err.Error(), "--changed-base must not be empty") {
		t.Fatalf("expected --changed-base must not be empty, got %v", err)
	}
	if err := runAgentTests(context.Background(), &buf, []string{"--changed", "alpha"}); err == nil || !strings.Contains(err.Error(), "cannot be combined with explicit module targets") {
		t.Fatalf("expected conflict with explicit targets, got %v", err)
	}
}

func TestAgentTestChangedNoChangesExitsZeroJSONAndHuman(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	initTestGitRepo(t, root, map[string]string{
		"app.scn": `application "changed_demo" {}
module "alpha" {
  source = "./alpha"
}
`,
		"go.mod": "module example.test/changeddemo\n\ngo 1.23\n",
		"alpha/package.scn": `package "alpha" {}
service "alpha" {
  runtime = "go"
  implementation {
    constructor = "NewService"
  }
}
`,
		"alpha/alpha_test.go": "package alpha\nimport \"testing\"\nfunc TestAlpha(t *testing.T) {}\n",
	})

	var human bytes.Buffer
	if err := runAgentTests(context.Background(), &human, []string{"--app-root", root, "--changed"}); err != nil {
		t.Fatalf("human --changed clean failed: %v", err)
	}
	if !strings.Contains(human.String(), "no changed modules to test") {
		t.Fatalf("human output = %q, want 'no changed modules to test'", human.String())
	}

	var jsonBuf bytes.Buffer
	if err := runAgentTests(context.Background(), &jsonBuf, []string{"--app-root", root, "--changed", "-o", "json"}); err != nil {
		t.Fatalf("json --changed clean failed: %v", err)
	}
	var envelope struct {
		OK   bool `json:"ok"`
		Data struct {
			OK      bool                  `json:"ok"`
			Modules []moduleTestResult    `json:"modules"`
			Changed *changedTestSelection `json:"changed"`
		} `json:"data"`
	}
	if err := json.Unmarshal(jsonBuf.Bytes(), &envelope); err != nil {
		t.Fatalf("unmarshal json: %v\nraw: %s", err, jsonBuf.String())
	}
	if !envelope.OK || !envelope.Data.OK || len(envelope.Data.Modules) != 0 || envelope.Data.Changed == nil || len(envelope.Data.Changed.AffectedModules) != 0 {
		t.Fatalf("unexpected clean json payload: %+v\nraw: %s", envelope, jsonBuf.String())
	}
}
