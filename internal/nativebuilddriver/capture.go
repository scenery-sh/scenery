package nativebuilddriver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go/parser"
	"go/scanner"
	"go/token"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
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
	Protocol      string               `json:"protocol"`
	Workspace     string               `json:"workspace"`
	StartedAt     time.Time            `json:"started_at"`
	DurationMS    float64              `json:"duration_ms"`
	Packages      map[string]Package   `json:"packages"`
	Files         map[string]string    `json:"files"`
	FileStamps    map[string]FileStamp `json:"file_stamps"`
	Syntax        map[string]string    `json:"syntax"`
	Directories   map[string]string    `json:"directories"`
	SnapshotFiles map[string]string    `json:"snapshot_files,omitempty"`
	GoVersion     string               `json:"go_version"`
	GoToolDigest  string               `json:"go_tool_digest"`
	BuildFlags    []string             `json:"build_flags"`
	// Pattern is the entrypoint package this capture describes; an empty value
	// means the application entrypoint.
	Pattern string `json:"pattern,omitempty"`
	// Entrypoint is the import path the pattern resolved to. A pattern may be
	// a directory or an import path, so only the resolved package identifies
	// the recorded main compile action.
	Entrypoint            string            `json:"entrypoint,omitempty"`
	Environment           map[string]string `json:"environment"`
	RequestEnv            map[string]string `json:"request_environment"`
	Digest                string            `json:"digest"`
	Reason                string            `json:"reason,omitempty"`
	PackageLoadingMS      float64           `json:"package_loading_ms,omitempty"`
	DirectoryValidationMS float64           `json:"directory_validation_ms,omitempty"`
	InputHashMS           float64           `json:"input_hash_ms,omitempty"`
	SnapshotMS            float64           `json:"snapshot_ms,omitempty"`
}

type FileStamp struct {
	Size, ModTimeNano, ChangeTimeNano int64
	Mode                              uint32
	Device, Inode                     uint64
}

// ValidateCurrentStamps rejects a capture whose live input domain changed
// after it was recorded. Stock-Go graph refresh reads the live workspace, so
// it must pass this check before publishing either recipe or executable.
func (capture Capture) ValidateCurrentStamps() error {
	for path, before := range capture.FileStamps {
		after, err := os.Lstat(path)
		if err != nil {
			return fmt.Errorf("captured input changed during build: %s: %w", path, err)
		}
		if !after.Mode().IsRegular() || fileStamp(after) != before {
			return fmt.Errorf("captured input changed during build, including a possible restore: %s", path)
		}
	}
	for path, before := range capture.Directories {
		after, err := directoryDigest(path)
		if err != nil || after != before {
			return fmt.Errorf("captured package membership changed during build: %s", path)
		}
	}
	return nil
}

// ApplicationEntrypointPattern is the package pattern of the generated
// application entrypoint, which a capture describes unless it names another.
const ApplicationEntrypointPattern = "./scenery_internal_main"

// EntrypointPattern is the package pattern this capture describes.
func (capture Capture) EntrypointPattern() string {
	if capture.Pattern == "" {
		return ApplicationEntrypointPattern
	}
	return capture.Pattern
}

// FullCapture loads the complete package graph of one entrypoint. An empty
// pattern captures the application entrypoint.
func FullCapture(ctx context.Context, goTool, workspace, snapshotRoot string, env []string, buildFlags []string, pattern string) (Capture, error) {
	if pattern == "" {
		pattern = ApplicationEntrypointPattern
	}
	started := time.Now()
	packageLoadingStarted := started
	result := Capture{Protocol: ProtocolVersion, Workspace: workspace, StartedAt: started.UTC(), Packages: map[string]Package{}, Files: map[string]string{}, FileStamps: map[string]FileStamp{}, Syntax: map[string]string{}, Directories: map[string]string{}, SnapshotFiles: map[string]string{}, BuildFlags: append([]string(nil), buildFlags...), Pattern: pattern, RequestEnv: relevantRequestEnvironment(env)}
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
	args = append(args, "-deps", "-json", pattern)
	cmd := exec.CommandContext(ctx, goTool, args...)
	cmd.Dir, cmd.Env = workspace, env
	data, err := cmd.Output()
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
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
		// A dependency is always listed before the package that imports it, so
		// the named pattern is the last package of the listing.
		result.Packages[pkg.ImportPath], result.Entrypoint = pkg, pkg.ImportPath
	}
	if len(result.Packages) == 0 {
		return result, fmt.Errorf("empty package closure")
	}
	if result.Packages[result.Entrypoint].Name != "main" {
		return result, fmt.Errorf("package pattern %s does not name an executable", pattern)
	}
	result.PackageLoadingMS = elapsedMS(packageLoadingStarted)
	var directoryDuration, inputHashDuration, snapshotDuration time.Duration
	for _, pkg := range result.Packages {
		directoryStarted := time.Now()
		if err := capturePackageDirectories(&result, pkg); err != nil {
			return result, err
		}
		directoryDuration += time.Since(directoryStarted)
		groups := [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles, pkg.IgnoredGoFiles, pkg.IgnoredOtherFiles}
		for groupIndex, files := range groups {
			for _, name := range files {
				path := filepath.Join(pkg.Dir, name)
				if err := captureFile(&result, workspace, snapshotRoot, path, groupIndex < 2, &inputHashDuration, &snapshotDuration); err != nil {
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
				if err := captureFile(&result, workspace, snapshotRoot, mod, false, &inputHashDuration, &snapshotDuration); err != nil {
					return result, err
				}
				sum := filepath.Join(filepath.Dir(mod), "go.sum")
				if info, statErr := os.Lstat(sum); statErr == nil && info.Mode().IsRegular() {
					if err := captureFile(&result, workspace, snapshotRoot, sum, false, &inputHashDuration, &snapshotDuration); err != nil {
						return result, err
					}
				}
			}
		}
	}
	result.DirectoryValidationMS = float64(directoryDuration.Nanoseconds()) / 1e6
	result.InputHashMS = float64(inputHashDuration.Nanoseconds()) / 1e6
	result.SnapshotMS = float64(snapshotDuration.Nanoseconds()) / 1e6
	encoded, err := canonicalCapture(result)
	if err != nil {
		return result, err
	}
	h := sha256.Sum256(encoded)
	result.Digest = "sha256:" + hex.EncodeToString(h[:])
	result.DurationMS = float64(time.Since(started).Nanoseconds()) / 1e6
	return result, nil
}

func captureFile(result *Capture, workspace, snapshotRoot, path string, goSyntax bool, inputHashDuration, snapshotDuration *time.Duration) error {
	hashStarted := time.Now()
	path, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	before, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat captured input %s: %w", path, err)
	}
	if !before.Mode().IsRegular() {
		return fmt.Errorf("captured input is not regular: %s", path)
	}
	beforeStamp := fileStamp(before)
	digest, _, err := FileDigest(path)
	if err != nil {
		return err
	}
	after, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat captured input after hashing %s: %w", path, err)
	}
	if fileStamp(after) != beforeStamp {
		return fmt.Errorf("source changed while hashing: %s", path)
	}
	result.Files[path] = digest
	result.FileStamps[path] = beforeStamp
	if goSyntax && strings.HasSuffix(path, ".go") {
		syntax, err := sourceSelectionIdentity(path)
		if err != nil {
			return err
		}
		result.Syntax[path] = syntax
	}
	*inputHashDuration += time.Since(hashStarted)
	rel, ok := workspaceRelative(workspace, path)
	if !ok {
		return nil
	}
	dst := filepath.Join(snapshotRoot, "workspace", rel)
	snapshotStarted := time.Now()
	copy, err := CopyRegular(path, dst)
	*snapshotDuration += time.Since(snapshotStarted)
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
	file, err := parser.ParseFile(token.NewFileSet(), path, data, parser.ImportsOnly)
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
	fileSet := token.NewFileSet()
	position := fileSet.AddFile(path, -1, len(data))
	var sourceScanner scanner.Scanner
	sourceScanner.Init(position, data, nil, scanner.ScanComments)
	for {
		_, tok, literal := sourceScanner.Scan()
		if tok == token.EOF {
			break
		}
		if tok != token.COMMENT {
			continue
		}
		for _, line := range strings.Split(literal, "\n") {
			text := strings.TrimSpace(line)
			if strings.HasPrefix(text, "//go:") || strings.HasPrefix(text, "// +build") {
				values = append(values, "directive="+text)
			}
		}
	}
	slices.Sort(values[1:])
	h := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return "sha256:" + hex.EncodeToString(h[:]), nil
}

// SourceSelectionIdentity identifies the package/import/build-directive part
// of one Go source file while deliberately ignoring function-body changes.
func SourceSelectionIdentity(path string) (string, error) {
	return sourceSelectionIdentity(path)
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
	type directory struct{ Path, Digest string }
	var directories []directory
	for path, digest := range value.Directories {
		directories = append(directories, directory{path, digest})
	}
	sort.Slice(directories, func(i, j int) bool { return directories[i].Path < directories[j].Path })
	return json.Marshal(struct {
		Protocol, Workspace, GoVersion, GoToolDigest string
		BuildFlags                                   []string
		Environment                                  map[string]string
		RequestEnvironment                           map[string]string
		Files                                        []file
		Packages                                     []pkg
		Directories                                  []directory
	}{ProtocolVersion, value.Workspace, value.GoVersion, value.GoToolDigest, value.BuildFlags, value.Environment, value.RequestEnv, files, packages, directories})
}

func capturePackageDirectories(result *Capture, pkg Package) error {
	if pkg.Dir == "" {
		return fmt.Errorf("package %s has no source directory", pkg.ImportPath)
	}
	if err := captureDirectory(result.Directories, pkg.Dir); err != nil {
		return err
	}
	if len(pkg.EmbedFiles) == 0 {
		return nil
	}
	return filepath.WalkDir(pkg.Dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() || path == pkg.Dir {
			return nil
		}
		return captureDirectory(result.Directories, path)
	})
}

func captureDirectory(target map[string]string, path string) error {
	digest, err := directoryDigest(path)
	if err != nil {
		return err
	}
	target[path] = digest
	return nil
}

func directoryDigest(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", err
	}
	values := make([]string, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			return "", err
		}
		values = append(values, entry.Name()+"\x00"+info.Mode().String())
	}
	sort.Strings(values)
	digest := sha256.Sum256([]byte(strings.Join(values, "\n")))
	return "sha256:" + hex.EncodeToString(digest[:]), nil
}

func relevantRequestEnvironment(env []string) map[string]string {
	keys := map[string]bool{}
	for _, key := range []string{"GOROOT", "GOOS", "GOARCH", "GOAMD64", "GOARM64", "GOEXPERIMENT", "GOTOOLCHAIN", "GOFLAGS", "GOWORK", "CGO_ENABLED", "CC", "CXX", "CGO_CFLAGS", "CGO_CPPFLAGS", "CGO_CXXFLAGS", "CGO_LDFLAGS"} {
		keys[key] = true
	}
	result := map[string]string{}
	for _, entry := range env {
		name, value, found := strings.Cut(entry, "=")
		if found && keys[name] {
			result[name] = value
		}
	}
	return result
}

func fileStamp(info os.FileInfo) FileStamp {
	stamp := FileStamp{Size: info.Size(), ModTimeNano: info.ModTime().UnixNano(), Mode: uint32(info.Mode())}
	if info.Sys() == nil {
		return stamp
	}
	value := reflect.ValueOf(info.Sys())
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return stamp
		}
		value = value.Elem()
	}
	if value.Kind() != reflect.Struct {
		return stamp
	}
	readUint := func(name string) uint64 {
		field := value.FieldByName(name)
		if field.IsValid() && field.CanUint() {
			return field.Uint()
		}
		return 0
	}
	stamp.Device, stamp.Inode = readUint("Dev"), readUint("Ino")
	for _, name := range []string{"Ctimespec", "Ctim", "Ctimen"} {
		field := value.FieldByName(name)
		if !field.IsValid() || field.Kind() != reflect.Struct {
			continue
		}
		seconds, nanos := field.FieldByName("Sec"), field.FieldByName("Nsec")
		if seconds.IsValid() && nanos.IsValid() && seconds.CanInt() && nanos.CanInt() {
			stamp.ChangeTimeNano = seconds.Int()*int64(time.Second) + nanos.Int()
			break
		}
	}
	return stamp
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
