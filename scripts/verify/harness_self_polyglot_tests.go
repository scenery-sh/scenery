package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

func runHarnessPolyglotTests(ctx context.Context, repo string) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    "polyglot native tests",
		Command: []string{"scenery", "test", "-o", "json"},
	}
	summary, err := verifyPolyglotTests(ctx, repo)
	step.DurationMS, step.Summary, step.OK = time.Since(started).Milliseconds(), summary, err == nil
	if err != nil {
		step.Error = err.Error()
	}
	return step
}

func verifyPolyglotTests(ctx context.Context, repo string) (map[string]any, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	root, err := os.MkdirTemp("", "scenery-polyglot-tests-")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	app := filepath.Join(root, "app")
	if err := copyHarnessPolyglotFixture(ctx, repo, app); err != nil {
		return nil, err
	}
	output, err := runHarnessAppCLI(ctx, repo, app, filepath.Join(root, "agent"), "test", "-o", "json")
	if err != nil {
		return nil, err
	}
	var payload map[string]any
	if err := decodeCLIJSON(output, &payload); err != nil {
		return nil, err
	}
	if errs := validateHarnessJSONSchemaFile(filepath.Join(repo, "docs/schemas/scenery.test.result.schema.json"), payload); len(errs) > 0 {
		return nil, fmt.Errorf("native test report: %s", strings.Join(errs, "; "))
	}
	var result struct {
		Modules []struct {
			Name    string `json:"module"`
			Runtime string `json:"runtime"`
			OK      bool   `json:"ok"`
		} `json:"modules"`
	}
	if err := decodeCLIJSON(output, &result); err != nil {
		return nil, err
	}
	var modules []string
	for _, module := range result.Modules {
		if module.OK {
			modules = append(modules, module.Name+":"+module.Runtime)
		}
	}
	slices.Sort(modules)
	if !slices.Equal(modules, []string{"consumer:go", "mirror:ts", "native:go", "pricing:rust", "python:python", "typescript:ts"}) {
		return nil, fmt.Errorf("unexpected passing native test modules: %v", modules)
	}
	return map[string]any{"modules": result.Modules}, nil
}

func copyHarnessPolyglotFixture(ctx context.Context, repo, app string) error {
	const fixture = "testdata/apps/polyglot"
	files, err := runHarnessGit(ctx, repo, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", fixture)
	if err != nil {
		return err
	}
	for path := range strings.SplitSeq(files, "\x00") {
		relative, ok := strings.CutPrefix(path, fixture+"/")
		if !ok || !filepath.IsLocal(relative) {
			continue
		}
		if err := copyHarnessFile(filepath.Join(repo, path), filepath.Join(app, relative), 0o600); err != nil {
			return err
		}
	}
	if err := harnessReplaceInFile(filepath.Join(app, "go.mod"), "=> ../../..", "=> "+repo); err != nil {
		return err
	}
	return nil
}
