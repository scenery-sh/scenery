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
	compilerOptions, err := stagedCompilerOptions(ctx, binary, configPath, stageRoot)
	if err != nil {
		return err
	}
	config := map[string]any{
		"extends":         filepath.ToSlash(configPath),
		"compilerOptions": compilerOptions,
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

func stagedCompilerOptions(ctx context.Context, binary, configPath, stageRoot string) (map[string]any, error) {
	options := map[string]any{"noEmit": true}
	indexPath := filepath.Join(stageRoot, "react", "scenery-ui", "index.ts")
	if info, err := os.Stat(indexPath); err != nil || !info.Mode().IsRegular() {
		return options, nil
	}

	command := exec.CommandContext(ctx, binary, "--showConfig", "--project", configPath)
	command.Dir = filepath.Dir(configPath)
	output, err := command.CombinedOutput()
	if err != nil {
		if len(output) == 0 {
			output = []byte(err.Error())
		}
		return nil, &Error{Code: "SCN6322", Classification: "readiness", Output: "could not resolve declared TypeScript path aliases: " + strings.TrimSpace(string(output))}
	}

	paths, err := stagedUIPaths(output, configPath, stageRoot)
	if err != nil {
		return nil, &Error{Code: "SCN6322", Classification: "readiness", Output: "could not resolve declared TypeScript path aliases: " + err.Error()}
	}
	options["paths"] = paths
	return options, nil
}

func stagedUIPaths(config []byte, configPath, stageRoot string) (map[string][]string, error) {
	var resolved struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(config, &resolved); err != nil {
		return nil, err
	}

	base := filepath.Dir(configPath)
	if resolved.CompilerOptions.BaseURL != "" {
		base = resolved.CompilerOptions.BaseURL
		if !filepath.IsAbs(base) {
			base = filepath.Join(filepath.Dir(configPath), filepath.FromSlash(base))
		}
	}
	paths := make(map[string][]string, len(resolved.CompilerOptions.Paths)+2)
	for alias, targets := range resolved.CompilerOptions.Paths {
		paths[alias] = make([]string, 0, len(targets))
		for _, target := range targets {
			path := filepath.FromSlash(target)
			if !filepath.IsAbs(path) {
				path = filepath.Join(base, path)
			}
			paths[alias] = append(paths[alias], filepath.ToSlash(filepath.Clean(path)))
		}
	}
	paths["@scenery/ui"] = []string{filepath.ToSlash(filepath.Join(stageRoot, "react", "scenery-ui", "index.ts"))}
	paths["@scenery/ui/tokens.stylex"] = []string{filepath.ToSlash(filepath.Join(stageRoot, "react", "scenery-ui", "tokens.stylex.ts"))}
	return paths, nil
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
