package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"scenery.sh/internal/tscheck"
)

const harnessTypeScriptCheckerProbeName = "TypeScript checker probes"

type harnessTypeScriptCheckerCheck func(context.Context, string) (map[string]any, []checkDiagnostic, error)

func runHarnessTypeScriptCheckerProbeStep(ctx context.Context, repoRoot string) harnessStep {
	return runHarnessTypeScriptCheckerProbeStepWithCheck(ctx, repoRoot, runHarnessTypeScriptCheckerProbeCheck)
}

func runHarnessTypeScriptCheckerProbeStepWithCheck(ctx context.Context, repoRoot string, check harnessTypeScriptCheckerCheck) harnessStep {
	started := time.Now()
	step := harnessStep{
		Name:    harnessTypeScriptCheckerProbeName,
		Command: []string{harnessLocalSceneryBinaryPath(repoRoot), "harness", "self", "--release", "--summary"},
	}
	var err error
	step.Summary, step.Diagnostics, err = check(ctx, repoRoot)
	step.DurationMS = time.Since(started).Milliseconds()
	if err != nil {
		step.OK = false
		step.Error = strings.TrimSpace(err.Error())
		if len(step.Diagnostics) == 0 {
			step.Diagnostics = []checkDiagnostic{{
				Stage:           step.Name,
				Severity:        "error",
				Message:         step.Error,
				SuggestedAction: "Fix the TypeScript checker process boundary, then rerun `scenery harness self --release --summary --write`.",
			}}
		}
		return step
	}
	step.OK = !hasErrorDiagnostics(step.Diagnostics)
	return step
}

func runHarnessTypeScriptCheckerProbeCheck(ctx context.Context, _ string) (map[string]any, []checkDiagnostic, error) {
	if runtime.GOOS == "windows" {
		return map[string]any{
			"proof":  "not_applicable_on_windows",
			"reason": "the checker process fixture uses a POSIX shell",
		}, nil, nil
	}
	root, err := os.MkdirTemp("", "scenery-tscheck-process-probe-*")
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := os.MkdirAll(filepath.Join(root, "node_modules"), 0o755); err != nil {
		return nil, nil, err
	}
	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte("{}\n"), 0o644); err != nil {
		return nil, nil, err
	}
	outputRoot := filepath.Join(root, "generated", "client")
	files := []tscheck.File{{Path: filepath.Join(outputRoot, "react", "orders.generated.tsx"), Bytes: []byte("export {}\n")}}

	generatedOutput := filepath.Join(filepath.Dir(outputRoot), ".scenery-tscheck-probe", "react", "orders.generated.tsx") + "(1,1): error TS2322"
	generatedBinary, err := writeHarnessFailingTypeScriptChecker(root, "tsc-generated", generatedOutput)
	if err != nil {
		return nil, nil, err
	}
	if err := requireHarnessTypeScriptCheckCode(ctx, generatedBinary, root, outputRoot, files, "SCN6320"); err != nil {
		return nil, nil, err
	}

	applicationOutput := filepath.Join(root, "components", "broken.tsx") + "(1,1): error TS2304"
	applicationBinary, err := writeHarnessFailingTypeScriptChecker(root, "tsc-application", applicationOutput)
	if err != nil {
		return nil, nil, err
	}
	if err := requireHarnessTypeScriptCheckCode(ctx, applicationBinary, root, outputRoot, files, "SCN6321"); err != nil {
		return nil, nil, err
	}

	if err := os.WriteFile(filepath.Join(root, "tsconfig.json"), []byte(`{"compilerOptions":{"paths":{"@/*":["./src/*"],"@scenery/ui":["./old/index.ts"]}}}`), 0o644); err != nil {
		return nil, nil, err
	}
	capturePath := filepath.Join(root, "staged-alias-config.json")
	aliasBinary := filepath.Join(root, "tsc-alias")
	aliasScript := `#!/bin/sh
if [ "$1" = "--showConfig" ]; then
  printf '%s\n' '{"compilerOptions":{"paths":{"@/*":["./src/*"],"@scenery/ui":["./old/index.ts"]}}}'
  exit 0
fi
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--project" ]; then
    cp "$2" ` + shellQuote(capturePath) + `
    exit 0
  fi
  shift
done
exit 1
`
	if err := os.WriteFile(aliasBinary, []byte(aliasScript), 0o755); err != nil {
		return nil, nil, err
	}
	aliasFiles := []tscheck.File{
		{Path: filepath.Join(outputRoot, "react", "scenery-ui", "index.ts"), Bytes: []byte("export {}\n")},
		{Path: filepath.Join(outputRoot, "react", "scenery-ui", "tokens.stylex.ts"), Bytes: []byte("export {}\n")},
	}
	if err := tscheck.Check(ctx, aliasBinary, root, outputRoot, "tsconfig.json", aliasFiles); err != nil {
		return nil, nil, err
	}
	encoded, err := os.ReadFile(capturePath)
	if err != nil {
		return nil, nil, err
	}
	var staged struct {
		CompilerOptions struct {
			Paths map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(encoded, &staged); err != nil {
		return nil, nil, err
	}
	uiPath := staged.CompilerOptions.Paths["@scenery/ui"]
	if len(uiPath) != 1 || !strings.Contains(uiPath[0], ".scenery-tscheck-") || strings.Contains(uiPath[0], "/old/") {
		return nil, nil, fmt.Errorf("staged @scenery/ui path = %v", uiPath)
	}
	appPath := staged.CompilerOptions.Paths["@/*"]
	if len(appPath) != 1 || appPath[0] != filepath.ToSlash(filepath.Join(root, "src", "*")) {
		return nil, nil, fmt.Errorf("preserved application alias = %v", appPath)
	}

	return map[string]any{
		"proof":                  "typescript_checker_failures_classified_and_staged_ui_alias_verified",
		"generated_error_code":   "SCN6320",
		"application_error_code": "SCN6321",
		"staged_ui_alias":        true,
	}, nil, nil
}

func writeHarnessFailingTypeScriptChecker(root, name, output string) (string, error) {
	path := filepath.Join(root, name)
	script := "#!/bin/sh\nprintf '%s\\n' " + shellQuote(output) + "\nexit 1\n"
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		return "", err
	}
	return path, nil
}

func requireHarnessTypeScriptCheckCode(ctx context.Context, binary, root, outputRoot string, files []tscheck.File, want string) error {
	err := tscheck.Check(ctx, binary, root, outputRoot, "tsconfig.json", files)
	var classified *tscheck.Error
	if !errors.As(err, &classified) || classified.Code != want {
		return fmt.Errorf("TypeScript checker error = %#v, want %s", err, want)
	}
	return nil
}
