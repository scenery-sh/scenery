package parse

import (
	"bytes"
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
	return analyze(root, name, nil, []string{"./..."}, nil)
}

func AnalyzeTarget(root, name string, overlay map[string][]byte, target gotarget.Context) (*model.App, error) {
	if len(target.Patterns) == 0 {
		return nil, errors.New("go target has no package patterns")
	}
	return analyze(root, name, overlay, target.Patterns, &target)
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

func analyze(root, name string, overlay map[string][]byte, patterns []string, target *gotarget.Context) (*model.App, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedTypes |
			packages.NeedModule,
		Dir:     root,
		Overlay: overlay,
	}
	if overlay != nil {
		cfg.Env = gotarget.Hermetic(nil)
	}
	if target != nil {
		cfg.Dir = target.ModuleRoot
		cfg.Env = gotarget.Hermetic(target)
		cfg.BuildFlags = append([]string(nil), target.BuildFlags...)
		if len(target.BuildTags) > 0 {
			cfg.BuildFlags = append(cfg.BuildFlags, "-tags="+strings.Join(target.BuildTags, ","))
		}
	}

	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, err
	}
	var loadErrors []string
	for _, pkg := range pkgs {
		for _, pkgErr := range pkg.Errors {
			loadErrors = append(loadErrors, pkgErr.Error())
		}
	}
	if len(loadErrors) > 0 {
		slices.Sort(loadErrors)
		return nil, fmt.Errorf("go package loading failed: %s", strings.Join(loadErrors, "; "))
	}

	app := &model.App{Name: name, Root: root}
	for _, pkg := range pkgs {
		paths := packageFilePaths(pkg)
		if len(paths) == 0 {
			continue
		}
		absDir := filepath.Dir(paths[0])
		relDir, err := filepath.Rel(root, absDir)
		if err != nil {
			return nil, err
		}
		mpkg := &model.Package{
			Analysis:   &model.PackageAnalysis{Types: pkg.Types},
			ImportPath: pkg.PkgPath,
			RelDir:     relDir,
		}
		app.Packages = append(app.Packages, mpkg)
		if app.ModulePath == "" && pkg.Module != nil {
			app.ModulePath = pkg.Module.Path
		}
	}
	slices.SortFunc(app.Packages, func(left, right *model.Package) int {
		return strings.Compare(left.RelDir, right.RelDir)
	})
	return app, nil
}

func packageFilePaths(pkg *packages.Package) []string {
	switch {
	case len(pkg.CompiledGoFiles) > 0:
		return pkg.CompiledGoFiles
	default:
		return pkg.GoFiles
	}
}
