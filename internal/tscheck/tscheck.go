package tscheck

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"scenery.sh/internal/toolchain"
)

type File struct {
	Path  string
	Bytes []byte
}

type Error struct {
	Code           string
	Classification string
	Output         string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s: TypeScript React verification failed (%s): %s", e.Code, e.Classification, strings.TrimSpace(e.Output))
}

func ManagedBinary(ctx context.Context, appRoot string) (string, error) {
	manifest, err := toolchain.LoadBundledManifest()
	if err != nil {
		return "", err
	}
	store, err := toolchain.NewStore(toolchain.DefaultStoreDir(appRoot), manifest)
	if err != nil {
		return "", err
	}
	store.ManifestSHA256 = toolchain.BundledManifestSHA256()
	if _, err := store.Sync(ctx, toolchain.Options{RootDir: appRoot, Tool: "tsgo"}); err != nil {
		return "", fmt.Errorf("SCN6322: native TypeScript checker is unavailable: %w", err)
	}
	status, err := store.Path(ctx, "tsgo", toolchain.CurrentPlatform())
	if err != nil {
		return "", fmt.Errorf("SCN6322: native TypeScript checker is unavailable: %w", err)
	}
	if status.Status != "installed" {
		return "", fmt.Errorf("SCN6322: native TypeScript checker is unavailable: status %s", status.Status)
	}
	return status.ManagedPath, nil
}

func Check(ctx context.Context, binary, appRoot, outputRoot, tsconfig string, files []File) error {
	configPath := filepath.Join(appRoot, filepath.FromSlash(tsconfig))
	if info, err := os.Stat(configPath); err != nil || !info.Mode().IsRegular() {
		return &Error{Code: "SCN6322", Classification: "readiness", Output: "declared react tsconfig is unavailable: " + filepath.ToSlash(tsconfig)}
	}
	if NodeModulesPath(filepath.Dir(configPath), appRoot) == "" {
		return &Error{Code: "SCN6322", Classification: "readiness", Output: "node_modules is unavailable for the declared react tsconfig; install application dependencies"}
	}
	if err := os.MkdirAll(filepath.Dir(outputRoot), 0o755); err != nil {
		return err
	}
	stageRoot, err := os.MkdirTemp(filepath.Dir(outputRoot), ".scenery-tscheck-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stageRoot)
	for _, file := range files {
		relative, relErr := filepath.Rel(outputRoot, file.Path)
		if relErr != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			continue
		}
		path := filepath.Join(stageRoot, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(path, file.Bytes, 0o644); err != nil {
			return err
		}
	}
	config := map[string]any{
		"extends":         filepath.ToSlash(configPath),
		"compilerOptions": map[string]any{"noEmit": true},
		"include":         []string{filepath.ToSlash(filepath.Join(stageRoot, "**", "*.ts")), filepath.ToSlash(filepath.Join(stageRoot, "**", "*.tsx"))},
	}
	encoded, _ := json.Marshal(config)
	stageConfig := filepath.Join(stageRoot, "tsconfig.scenery.json")
	if err := os.WriteFile(stageConfig, encoded, 0o644); err != nil {
		return err
	}
	command := exec.CommandContext(ctx, binary, "--project", stageConfig, "--pretty", "false", "--noEmit")
	command.Dir = appRoot
	output, err := command.CombinedOutput()
	if err == nil {
		return nil
	}
	classification, code := "unrelated application error", "SCN6321"
	if strings.Contains(string(output), ".scenery-tscheck-") && strings.Contains(string(output), ".generated.tsx") {
		classification, code = "incompatible declared override", "SCN6320"
	}
	if len(output) == 0 {
		output = []byte(err.Error())
	}
	return &Error{Code: code, Classification: classification, Output: string(output)}
}

func NodeModulesPath(start, root string) string {
	root, _ = filepath.Abs(root)
	for current, _ := filepath.Abs(start); ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, "node_modules")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		if current == root || !strings.HasPrefix(current, root+string(filepath.Separator)) || filepath.Dir(current) == current {
			return ""
		}
	}
}
