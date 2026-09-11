package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	generateapi "scenery.sh/internal/generate/api"
)

// PrepareNativeExperiment selects the private projection before rendering or
// materializing any executable. Its workspace is owned separately from ordinary
// build state; failed preparation never authorizes pruning retained artifacts.
func PrepareNativeExperiment(ctx context.Context, appRoot string, cfg app.Config, snapshot *SourceSnapshot, workspace string, project func(*compiler.Result) (generateapi.GoWorkspaceProjection, error)) (*Result, error) {
	if project == nil {
		return nil, fmt.Errorf("native experiment requires a projection renderer")
	}
	ordinary, err := workspaceDir(appRoot, cfg.Name)
	if err != nil {
		return nil, err
	}
	if err := claimNativeExperimentWorkspace(appRoot, ordinary, workspace); err != nil {
		return nil, err
	}
	return prepareForCompileWithProjection(ctx, appRoot, cfg, snapshot, project, workspace)
}

func claimNativeExperimentWorkspace(appRoot, ordinary, workspace string) error {
	if !filepath.IsAbs(workspace) || filepath.Clean(workspace) != workspace {
		return fmt.Errorf("native experiment workspace must be an absolute clean path")
	}
	if err := os.MkdirAll(filepath.Dir(workspace), 0700); err != nil {
		return err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(workspace))
	if err != nil {
		return err
	}
	if parent != filepath.Dir(workspace) {
		return fmt.Errorf("native experiment workspace parent must be canonical")
	}
	for _, reserved := range []string{appRoot, ordinary} {
		reserved, err = filepath.Abs(reserved)
		if err != nil {
			return err
		}
		if pathContains(workspace, reserved) || pathContains(reserved, workspace) {
			return fmt.Errorf("native experiment workspace overlaps app or ordinary workspace")
		}
	}
	if info, err := os.Lstat(workspace); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("native experiment workspace must be a real directory")
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	marker := workspace + ".native-experiment-owner"
	want := appRoot + "\n" + workspace + "\n"
	if data, err := os.ReadFile(marker); err == nil {
		if string(data) != want {
			return fmt.Errorf("native experiment workspace belongs to another preparation")
		}
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	entries, err := os.ReadDir(workspace)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	if len(entries) != 0 {
		return fmt.Errorf("native experiment cannot adopt a nonempty unowned workspace")
	}
	file, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(want)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

func pathContains(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func validateNativeExperimentProjection(files map[string][]byte) error {
	for _, entry := range []string{"scenery_native_worker/main.go", "scenery_framework_kernel/main.go"} {
		if len(files[entry]) == 0 {
			return fmt.Errorf("native experiment entrypoint missing: %s", entry)
		}
	}
	for relative := range files {
		if !filepath.IsLocal(relative) || filepath.ToSlash(filepath.Clean(relative)) != relative {
			return fmt.Errorf("invalid native experiment generated path: %s", relative)
		}
		if strings.HasPrefix(relative, "scenery_internal_main/") {
			return fmt.Errorf("native experiment includes ordinary entrypoint: %s", relative)
		}
	}
	return nil
}

// RefreshNativeExperiment preserves explicit projection ownership on graph hits.
// Native generations always receive a fresh pending full verifier; only source,
// dependency and generator preparation caches are reused, never an executable.
func RefreshNativeExperiment(ctx context.Context, appRoot string, cfg app.Config, snapshot *SourceSnapshot, workspace string, project func(*compiler.Result) (generateapi.GoWorkspaceProjection, error), previous *Result) (*Result, error) {
	if previous == nil || !previous.nativeExperiment || previous.AppRoot != appRoot || previous.Dir != workspace {
		return nil, fmt.Errorf("native refresh requires its own accepted projection workspace")
	}
	return PrepareNativeExperiment(ctx, appRoot, cfg, snapshot, workspace, project)
}
