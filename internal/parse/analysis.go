package parse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/tools/go/packages"

	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

const moduleLookupDisabled = "module lookup disabled by GOPROXY=off"

func Analyze(root, name string) (*model.App, error) {
	return analyze(context.Background(), root, name, nil, []string{"./..."}, nil)
}

func AnalyzeTarget(root, name string, overlay map[string][]byte, target gotarget.Context) (*model.App, error) {
	return AnalyzeTargetContext(context.Background(), root, name, overlay, target)
}

// AnalyzeTargetContext keeps cancellation attached to the complete Go loader
// operation, including its toolchain subprocesses.
func AnalyzeTargetContext(ctx context.Context, root, name string, overlay map[string][]byte, target gotarget.Context) (*model.App, error) {
	if len(target.Patterns) == 0 {
		return nil, errors.New("go target has no package patterns")
	}
	return analyze(ctx, root, name, overlay, target.Patterns, &target)
}

// MissingHermeticModulePackages returns imports that the declared target needs
// but the local module cache cannot provide while network lookup is disabled.
// It intentionally checks only the target's authored package patterns; staged
// generated packages are supplied through an overlay during AnalyzeTarget.
func MissingHermeticModulePackages(target gotarget.Context) ([]string, error) {
	if len(target.Patterns) == 0 {
		return nil, errors.New("go target has no package patterns")
	}
	args := []string{"list"}
	args = append(args, target.BuildFlags...)
	if len(target.BuildTags) > 0 {
		args = append(args, "-tags="+strings.Join(target.BuildTags, ","))
	}
	args = append(args, "-deps", "-e", "-json")
	args = append(args, target.Patterns...)
	command := exec.Command("go", args...)
	command.Dir = target.ModuleRoot
	command.Env = gotarget.Hermetic(&target)
	moduleFile, err := os.ReadFile(filepath.Join(target.ModuleRoot, "go.mod"))
	if err != nil {
		return nil, fmt.Errorf("read target Go module: %w", err)
	}
	modulePath := strings.TrimSpace(modfile.ModulePath(moduleFile))
	if modulePath == "" {
		return nil, errors.New("target go.mod has no module path")
	}
	output, err := command.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("inspect hermetic Go dependencies: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("inspect hermetic Go dependencies: %w", err)
	}

	decoder := json.NewDecoder(bytes.NewReader(output))
	var missing []string
	for {
		var pkg struct {
			ImportPath string `json:"ImportPath"`
			Error      *struct {
				Err string `json:"Err"`
			} `json:"Error"`
		}
		if err := decoder.Decode(&pkg); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return nil, fmt.Errorf("decode hermetic Go dependencies: %w", err)
		}
		if pkg.Error != nil &&
			strings.Contains(pkg.Error.Err, moduleLookupDisabled) &&
			pkg.ImportPath != "" &&
			pkg.ImportPath != modulePath &&
			!strings.HasPrefix(pkg.ImportPath, modulePath+"/") {
			missing = append(missing, pkg.ImportPath)
		}
	}
	slices.Sort(missing)
	return slices.Compact(missing), nil
}

func analyze(ctx context.Context, root, name string, overlay map[string][]byte, patterns []string, target *gotarget.Context) (*model.App, error) {
	logicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(logicalRoot)
	if err != nil {
		return nil, err
	}
	overlay = canonicalizeAnalysisOverlay(logicalRoot, root, overlay)
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedTypes |
			packages.NeedImports |
			packages.NeedModule,
		Dir:     root,
		Overlay: overlay,
	}
	if overlay != nil {
		cfg.Env = gotarget.Hermetic(nil)
	}
	// Loading types compiles export data through the go command; trimming
	// paths like Scenery-owned compilation makes that the same Go build cache
	// entry the build reuses instead of a second, path-bound copy.
	cfg.BuildFlags = gotarget.WithTrimpath(nil)
	if target != nil {
		cfg.Dir, err = filepath.EvalSymlinks(target.ModuleRoot)
		if err != nil {
			return nil, err
		}
		cfg.Env = gotarget.Hermetic(target)
		cfg.BuildFlags = gotarget.WithTrimpath(append([]string(nil), target.BuildFlags...))
		if overlay != nil {
			modFile, cleanup, err := prepareAnalysisModFile(cfg.Dir)
			if err != nil {
				return nil, err
			}
			defer cleanup()
			// Generated overlays can import implementation-only dependencies
			// which an ordinary source-only `go mod tidy` correctly removes.
			// Let Go update only this private copy while loading the overlay.
			cfg.BuildFlags = append(cfg.BuildFlags, "-mod=mod", "-modfile="+modFile)
		}
		if len(target.BuildTags) > 0 {
			cfg.BuildFlags = append(cfg.BuildFlags, "-tags="+strings.Join(target.BuildTags, ","))
		}
	}

	// An analysis of a prepared workspace is retained, so the next one loads
	// only what changed (see retained.go). An overlay supplies bytes the stamp
	// does not read, and an analysis without a target has no declared context.
	retainKey := ""
	var stamp analysisStamp
	if overlay == nil && target != nil {
		stamp, err = captureAnalysisStamp(cfg.Dir, target, cfg.Env, cfg.BuildFlags, patterns)
		if err == nil {
			// Several targets of one workspace are analyzed in turn, so each
			// retains its own analysis.
			retainKey = root + "\x00" + cfg.Dir + "\x00" + stamp.target
		}
	}
	if retainKey != "" {
		if retained := retainedAnalysisFor(retainKey); retained != nil {
			plan := planAnalysis(retained, stamp)
			if !plan.full {
				if app, ok := analyzeRetained(cfg, root, name, retainKey, retained, stamp, plan); ok {
					return app, nil
				}
			}
		}
	}
	loaded, modulePath, err := loadAnalysisPackages(cfg, root, patterns)
	if err != nil {
		if retainKey != "" {
			forgetAnalysis(retainKey)
		}
		return nil, err
	}
	analysis := &retainedAnalysis{stamp: stamp, modulePath: modulePath, packages: make(map[string]*retainedPackage, len(loaded))}
	for _, pkg := range loaded {
		analysis.packages[pkg.model.ImportPath] = pkg
	}
	if retainKey != "" {
		retainAnalysis(retainKey, analysis)
	}
	return analysis.app(name, root), nil
}

// analyzeRetained answers an analysis from the retained one, loading the
// planned directories. It reports false when the whole target must be loaded:
// the load failed or returned other packages than planned, and a whole load
// then produces the authoritative result, including its errors.
func analyzeRetained(cfg *packages.Config, root, name, key string, retained *retainedAnalysis, stamp analysisStamp, plan analysisPlan) (*model.App, bool) {
	load := func(dirs []string) ([]*retainedPackage, bool) {
		if len(dirs) == 0 {
			return nil, true
		}
		patterns := make([]string, 0, len(dirs))
		for _, dir := range dirs {
			if dir == "" {
				patterns = append(patterns, ".")
			} else {
				patterns = append(patterns, "./"+dir)
			}
		}
		loaded, _, err := loadAnalysisPackages(cfg, root, patterns)
		return loaded, err == nil
	}
	// The packages whose own sources changed are loaded first. Their importers
	// are loaded only when what they can observe of one changed, which a body
	// edit never does; otherwise the application's main, which imports every
	// package, would cost a whole load after every edit.
	loaded, ok := load(plan.dirs)
	if !ok {
		return nil, false
	}
	dirs, loadedDirs := slices.Clone(plan.dirs), map[string]bool{}
	var observable []string
	for _, pkg := range loaded {
		loadedDirs[pkg.dir] = true
		if previous := retained.packages[pkg.model.ImportPath]; previous == nil || previous.api != pkg.api {
			observable = append(observable, pkg.model.ImportPath)
		}
	}
	if importers := retained.importerDirs(observable, loadedDirs); len(importers) > 0 {
		more, ok := load(importers)
		if !ok {
			return nil, false
		}
		loaded, dirs = append(loaded, more...), append(dirs, importers...)
	}
	merged, ok := mergeAnalysis(retained, stamp, dirs, loaded)
	if !ok {
		return nil, false
	}
	retainAnalysis(key, merged)
	return merged.app(name, root), true
}

// loadAnalysisPackages loads patterns and returns each package with the
// directory and direct imports a retained analysis needs.
func loadAnalysisPackages(cfg *packages.Config, root string, patterns []string) ([]*retainedPackage, string, error) {
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, "", err
	}
	if len(pkgs) == 0 && len(patterns) > 0 {
		return nil, "", fmt.Errorf("go package loading returned no Go packages for target %s; check its package patterns and go.mod", strings.Join(patterns, ", "))
	}
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			loadErrors = append(loadErrors, pkgErr.Error())
		}
	}
	if len(loadErrors) > 0 {
		slices.Sort(loadErrors)
		return nil, "", fmt.Errorf("go package loading failed: %s", strings.Join(loadErrors, "; "))
	}
	modulePath := ""
	var loaded []*retainedPackage
	for _, pkg := range pkgs {
		paths := packageFilePaths(pkg)
		if len(paths) == 0 {
			continue
		}
		absDir := filepath.Dir(paths[0])
		relDir, err := filepath.Rel(root, absDir)
		if err != nil {
			return nil, "", err
		}
		moduleDir, err := filepath.Rel(cfg.Dir, absDir)
		if err != nil {
			return nil, "", err
		}
		if moduleDir == "." {
			moduleDir = ""
		}
		imports := make([]string, 0, len(pkg.Imports))
		for path := range pkg.Imports {
			imports = append(imports, path)
		}
		slices.Sort(imports)
		loaded = append(loaded, &retainedPackage{
			model:   &model.Package{Analysis: &model.PackageAnalysis{Types: pkg.Types}, ImportPath: pkg.PkgPath, RelDir: relDir},
			dir:     filepath.ToSlash(moduleDir),
			imports: imports,
			api:     packageAPIDigest(pkg.Types),
		})
		if modulePath == "" && pkg.Module != nil {
			modulePath = pkg.Module.Path
		}
	}
	return loaded, modulePath, nil
}

// Go canonicalizes its working directory before matching overlay paths. Keep
// virtual generated files on that same physical-root spelling, especially on
// macOS where /var resolves to /private/var.
func canonicalizeAnalysisOverlay(logicalRoot, physicalRoot string, overlay map[string][]byte) map[string][]byte {
	if len(overlay) == 0 || logicalRoot == physicalRoot {
		return overlay
	}
	canonical := make(map[string][]byte, len(overlay))
	for path, data := range overlay {
		relative, err := filepath.Rel(logicalRoot, path)
		if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			path = filepath.Join(physicalRoot, relative)
		}
		canonical[path] = data
	}
	return canonical
}

func prepareAnalysisModFile(moduleRoot string) (string, func(), error) {
	modData, err := os.ReadFile(filepath.Join(moduleRoot, "go.mod"))
	if err != nil {
		return "", nil, fmt.Errorf("read target go.mod for staged analysis: %w", err)
	}
	file, err := os.CreateTemp("", "scenery-analysis-*.mod")
	if err != nil {
		return "", nil, fmt.Errorf("create staged analysis go.mod: %w", err)
	}
	modPath := file.Name()
	sumPath := strings.TrimSuffix(modPath, ".mod") + ".sum"
	cleanup := func() {
		_ = os.Remove(modPath)
		_ = os.Remove(sumPath)
	}
	if _, err := file.Write(modData); err != nil {
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write staged analysis go.mod: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close staged analysis go.mod: %w", err)
	}
	sumData, err := os.ReadFile(filepath.Join(moduleRoot, "go.sum"))
	if err == nil {
		if err := os.WriteFile(sumPath, sumData, 0o600); err != nil {
			cleanup()
			return "", nil, fmt.Errorf("write staged analysis go.sum: %w", err)
		}
	} else if !os.IsNotExist(err) {
		cleanup()
		return "", nil, fmt.Errorf("read target go.sum for staged analysis: %w", err)
	}
	return modPath, cleanup, nil
}

func packageFilePaths(pkg *packages.Package) []string {
	switch {
	case len(pkg.CompiledGoFiles) > 0:
		return pkg.CompiledGoFiles
	default:
		return pkg.GoFiles
	}
}
