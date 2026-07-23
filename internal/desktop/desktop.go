// Package desktop owns the Tauri-specific project, command, and artifact
// contract used by Scenery's CLI orchestration.
package desktop

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"scenery.sh/internal/app"
)

type Project struct {
	Name         string
	FrontendRoot string
	TauriRoot    string
	CLIPath      string
}

type Command struct {
	Path string
	Args []string
	Dir  string
}

func Resolve(appRoot string, frontends map[string]app.FrontendConfig) ([]Project, error) {
	names := make([]string, 0, len(frontends))
	for name, frontend := range frontends {
		if frontend.Tauri != nil {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return nil, fmt.Errorf("desktop mode requires at least one frontends.<name>.tauri block")
	}
	projects := make([]Project, 0, len(names))
	for _, name := range names {
		frontend := frontends[name]
		frontendRoot := strings.TrimSpace(frontend.Root)
		if frontendRoot == "" {
			frontendRoot = filepath.Join("apps", name)
		}
		if !filepath.IsAbs(frontendRoot) {
			frontendRoot = filepath.Join(appRoot, frontendRoot)
		}
		frontendRoot = filepath.Clean(frontendRoot)

		tauriRoot := strings.TrimSpace(frontend.Tauri.Root)
		if tauriRoot == "" {
			tauriRoot = frontendRoot
		} else {
			tauriRoot = filepath.Join(appRoot, filepath.FromSlash(tauriRoot))
		}
		tauriRoot = filepath.Clean(tauriRoot)
		configPath := filepath.Join(tauriRoot, "src-tauri", "tauri.conf.json")
		if info, err := os.Stat(configPath); err != nil || info.IsDir() {
			return nil, fmt.Errorf("desktop frontend %q requires a Tauri 2 config at %s", name, configPath)
		}
		cliPath := localBin(tauriRoot, "tauri")
		if cliPath == "" {
			return nil, fmt.Errorf("desktop frontend %q requires app-local @tauri-apps/cli; expected node_modules/.bin/tauri at or above %s", name, tauriRoot)
		}
		projects = append(projects, Project{
			Name:         name,
			FrontendRoot: frontendRoot,
			TauriRoot:    tauriRoot,
			CLIPath:      cliPath,
		})
	}
	return projects, nil
}

func DevCommand(project Project, devURL string) (Command, error) {
	overlay, err := json.Marshal(struct {
		Build struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		} `json:"build"`
	}{
		Build: struct {
			DevURL           string `json:"devUrl"`
			BeforeDevCommand string `json:"beforeDevCommand"`
		}{DevURL: devURL},
	})
	if err != nil {
		return Command{}, err
	}
	return Command{Path: project.CLIPath, Args: []string{"dev", "--config", string(overlay)}, Dir: project.TauriRoot}, nil
}

func BuildCommand(project Project, frontendDist string) (Command, error) {
	overlay, err := json.Marshal(struct {
		Build struct {
			FrontendDist       string `json:"frontendDist"`
			BeforeBuildCommand string `json:"beforeBuildCommand"`
		} `json:"build"`
	}{
		Build: struct {
			FrontendDist       string `json:"frontendDist"`
			BeforeBuildCommand string `json:"beforeBuildCommand"`
		}{FrontendDist: frontendDist},
	})
	if err != nil {
		return Command{}, err
	}
	return Command{Path: project.CLIPath, Args: []string{"build", "--config", string(overlay)}, Dir: project.TauriRoot}, nil
}

func Run(ctx context.Context, command Command, env []string, output io.Writer) error {
	cmd := exec.CommandContext(ctx, command.Path, command.Args...)
	cmd.Dir = command.Dir
	cmd.Env = env
	var tail bytes.Buffer
	if output == nil {
		output = io.Discard
	}
	cmd.Stdout = io.MultiWriter(output, &tail)
	cmd.Stderr = io.MultiWriter(output, &tail)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w\n%s", command.Path, strings.Join(command.Args, " "), err, lastLines(tail.String(), 20))
	}
	return nil
}

func BundleArtifacts(project Project) ([]string, error) {
	bundleRoot := filepath.Join(project.TauriRoot, "src-tauri", "target", "release", "bundle")
	formats, err := os.ReadDir(bundleRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("tauri build produced no bundle directory at %s", bundleRoot)
		}
		return nil, err
	}
	var artifacts []string
	for _, format := range formats {
		if !format.IsDir() {
			continue
		}
		formatDir := filepath.Join(bundleRoot, format.Name())
		entries, err := os.ReadDir(formatDir)
		if err != nil {
			return nil, err
		}
		for _, entry := range entries {
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			artifacts = append(artifacts, filepath.Join(formatDir, entry.Name()))
		}
	}
	sort.Strings(artifacts)
	if len(artifacts) == 0 {
		return nil, fmt.Errorf("tauri build produced no bundle artifacts under %s", bundleRoot)
	}
	return artifacts, nil
}

func localBin(root, name string) string {
	for dir := filepath.Clean(root); ; dir = filepath.Dir(dir) {
		bin := filepath.Join(dir, "node_modules", ".bin", name)
		if info, err := os.Stat(bin); err == nil && !info.IsDir() {
			return bin
		}
		if parent := filepath.Dir(dir); parent == dir {
			return ""
		}
	}
}

func lastLines(value string, count int) string {
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
