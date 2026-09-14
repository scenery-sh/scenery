package nativebuilddriver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/parser"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Package struct {
	ImportPath                                                                                                                                             string            `json:"ImportPath"`
	Name                                                                                                                                                   string            `json:"Name"`
	Dir                                                                                                                                                    string            `json:"Dir"`
	Imports                                                                                                                                                []string          `json:"Imports"`
	ImportMap                                                                                                                                              map[string]string `json:"ImportMap"`
	GoFiles, CgoFiles, CFiles, CXXFiles, MFiles, HFiles, FFiles, SFiles, SwigFiles, SwigCXXFiles, SysoFiles, EmbedFiles, IgnoredGoFiles, IgnoredOtherFiles []string
	Incomplete                                                                                                                                             bool              `json:"Incomplete"`
	Error                                                                                                                                                  json.RawMessage   `json:"Error"`
	DepsErrors                                                                                                                                             []json.RawMessage `json:"DepsErrors"`
	Module                                                                                                                                                 *struct {
		GoMod   string
		Replace *struct{ GoMod string }
	}
}

type Capture struct {
	Protocol      string             `json:"protocol"`
	Workspace     string             `json:"workspace"`
	StartedAt     time.Time          `json:"started_at"`
	DurationMS    float64            `json:"duration_ms"`
	Packages      map[string]Package `json:"packages"`
	Files         map[string]string  `json:"files"`
	Syntax        map[string]string  `json:"syntax"`
	SnapshotFiles map[string]string  `json:"snapshot_files,omitempty"`
	GoVersion     string             `json:"go_version"`
	GoToolDigest  string             `json:"go_tool_digest"`
	BuildFlags    []string           `json:"build_flags"`
	Environment   map[string]string  `json:"environment"`
	Digest        string             `json:"digest"`
}

func FullCapture(ctx context.Context, goTool, workspace, snapshotRoot string, env []string, buildFlags []string) (Capture, error) {
	started := time.Now()
	result := Capture{Protocol: ProtocolVersion, Workspace: workspace, StartedAt: started.UTC(), Packages: map[string]Package{}, Files: map[string]string{}, Syntax: map[string]string{}, SnapshotFiles: map[string]string{}, BuildFlags: append([]string(nil), buildFlags...)}
	goPath, err := exec.LookPath(goTool)
	if err != nil {
		return result, err
	}
	result.GoToolDigest, _, err = FileDigest(goPath)
	if err != nil {
		return result, err
	}
	versionCmd := exec.CommandContext(ctx, goTool, "version")
	versionCmd.Dir, versionCmd.Env = workspace, env
	version, err := versionCmd.Output()
	if err != nil {
		return result, fmt.Errorf("go version failed: %w", err)
	}
	result.GoVersion = strings.TrimSpace(string(version))
	result.Environment, err = effectiveGoEnvironment(ctx, goTool, workspace, env)
	if err != nil {
		return result, err
	}
	args := []string{"list"}
	args = append(args, buildFlags...)
	args = append(args, "-deps", "-json", "./scenery_internal_main")
	cmd := exec.CommandContext(ctx, goTool, args...)
	cmd.Dir, cmd.Env = workspace, env
	data, err := cmd.Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			return result, fmt.Errorf("go list failed: %w: %s", err, exit.Stderr)
		}
		return result, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	for {
		var pkg Package
		if err := decoder.Decode(&pkg); err == io.EOF {
			break
		} else if err != nil {
			return result, err
		}
		if pkg.ImportPath == "" || pkg.Incomplete || len(pkg.Error) != 0 || len(pkg.DepsErrors) != 0 {
			return result, fmt.Errorf("incomplete package capture: %s", pkg.ImportPath)
		}
		result.Packages[pkg.ImportPath] = pkg
	}
	if len(result.Packages) == 0 {
		return result, fmt.Errorf("empty package closure")
	}
	for _, pkg := range result.Packages {
		groups := [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles, pkg.IgnoredGoFiles, pkg.IgnoredOtherFiles}
		for groupIndex, files := range groups {
			for _, name := range files {
				path := filepath.Join(pkg.Dir, name)
				if err := captureFile(&result, workspace, snapshotRoot, path, groupIndex < 2); err != nil {
					return result, err
				}
			}
		}
		if pkg.Module != nil {
			mod := pkg.Module.GoMod
			if pkg.Module.Replace != nil && pkg.Module.Replace.GoMod != "" {
				mod = pkg.Module.Replace.GoMod
			}
			if mod != "" {
				if err := captureFile(&result, workspace, snapshotRoot, mod, false); err != nil {
					return result, err
				}
				sum := filepath.Join(filepath.Dir(mod), "go.sum")
				if info, statErr := os.Lstat(sum); statErr == nil && info.Mode().IsRegular() {
					if err := captureFile(&result, workspace, snapshotRoot, sum, false); err != nil {
						return result, err
					}
				}
			}
		}
	}
	encoded, err := canonicalCapture(result)
	if err != nil {
		return result, err
	}
	h := sha256.Sum256(encoded)
	result.Digest = "sha256:" + hex.EncodeToString(h[:])
	result.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	return result, nil
}

func captureFile(result *Capture, workspace, snapshotRoot, path string, goSyntax bool) error {
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	digest, _, err := FileDigest(path)
	if err != nil {
		return err
	}
	result.Files[path] = digest
	if goSyntax && strings.HasSuffix(path, ".go") {
		syntax, err := sourceSelectionIdentity(path)
		if err != nil {
			return err
		}
		result.Syntax[path] = syntax
	}
	rel, err := filepath.Rel(workspace, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil
	}
	dst := filepath.Join(snapshotRoot, "workspace", rel)
	copy, err := CopyRegular(path, dst)
	if err != nil {
		return err
	}
	if copy.Digest != digest {
		return fmt.Errorf("source changed during capture: %s", path)
	}
	result.SnapshotFiles[path] = dst
	return nil
}

func sourceSelectionIdentity(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly|parser.ParseComments)
	if err != nil {
		return "", err
	}
	values := []string{"package=" + file.Name.Name}
	for _, imp := range file.Imports {
		name := ""
		if imp.Name != nil {
			name = imp.Name.Name
		}
		value, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			return "", err
		}
		values = append(values, "import="+name+":"+value)
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			text := strings.TrimSpace(comment.Text)
			if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "// +build") {
				values = append(values, "directive="+text)
			}
		}
	}
	slices.Sort(values[1:])
	h := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

func canonicalCapture(value Capture) ([]byte, error) {
	type file struct{ Path, Digest, Syntax string }
	var files []file
	for path, digest := range value.Files {
		files = append(files, file{path, digest, value.Syntax[path]})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	type pkg struct {
		ImportPath  string
		Name        string
		Dir         string
		Imports     []string
		ImportMap   map[string]string
		ModuleGoMod string
		Files       []string
	}
	var packages []pkg
	for _, p := range value.Packages {
		imports := append([]string(nil), p.Imports...)
		sort.Strings(imports)
		all := append([]string(nil), p.GoFiles...)
		all = append(all, p.CgoFiles...)
		all = append(all, p.CFiles...)
		all = append(all, p.CXXFiles...)
		all = append(all, p.MFiles...)
		all = append(all, p.HFiles...)
		all = append(all, p.FFiles...)
		all = append(all, p.SFiles...)
		all = append(all, p.SwigFiles...)
		all = append(all, p.SwigCXXFiles...)
		all = append(all, p.SysoFiles...)
		all = append(all, p.EmbedFiles...)
		all = append(all, p.IgnoredGoFiles...)
		all = append(all, p.IgnoredOtherFiles...)
		sort.Strings(all)
		moduleGoMod := ""
		if p.Module != nil {
			moduleGoMod = p.Module.GoMod
			if p.Module.Replace != nil && p.Module.Replace.GoMod != "" {
				moduleGoMod = p.Module.Replace.GoMod
			}
		}
		packages = append(packages, pkg{p.ImportPath, p.Name, p.Dir, imports, p.ImportMap, moduleGoMod, all})
	}
	sort.Slice(packages, func(i, j int) bool { return packages[i].ImportPath < packages[j].ImportPath })
	return json.Marshal(struct {
		Protocol, Workspace, GoVersion, GoToolDigest string
		BuildFlags                                   []string
		Environment                                  map[string]string
		Files                                        []file
		Packages                                     []pkg
	}{ProtocolVersion, value.Workspace, value.GoVersion, value.GoToolDigest, value.BuildFlags, value.Environment, files, packages})
}

func effectiveGoEnvironment(ctx context.Context, goTool, workspace string, env []string) (map[string]string, error) {
	keys := []string{"GOROOT", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOEXPERIMENT", "GOTOOLCHAIN", "GOFLAGS", "GOWORK", "CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"}
	args := append([]string{"env", "-json"}, keys...)
	cmd := exec.CommandContext(ctx, goTool, args...)
	cmd.Dir, cmd.Env = workspace, env
	data, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("go env failed: %w", err)
	}
	result := map[string]string{}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, fmt.Errorf("decode go env: %w", err)
	}
	return result, nil
}
