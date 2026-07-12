package parse

import (
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"golang.org/x/tools/go/packages"

	"scenery.sh/internal/envpolicy"
	"scenery.sh/internal/model"
)

type GoTargetContext struct {
	ModuleRoot           string
	Patterns             []string
	ToolchainVersion     string
	GOOS                 string
	GOARCH               string
	CGOEnabled           bool
	Experiments          []string
	BuildTags            []string
	BuildFlags           []string
	ArchitectureEnv      map[string]string
	NativeToolEnv        map[string]string
	ToolchainIdentity    map[string]string
	NativeToolIdentities []map[string]string
}

const goAnalysisMaxProcs = 2

func Analyze(root, name string) (*model.App, error) {
	return analyze(root, name, nil, []string{"./..."}, nil)
}

func AnalyzeTarget(root, name string, overlay map[string][]byte, target GoTargetContext) (*model.App, error) {
	if len(target.Patterns) == 0 {
		return nil, errors.New("Go target has no package patterns")
	}
	return analyze(root, name, overlay, target.Patterns, &target)
}

func analyze(root, name string, overlay map[string][]byte, patterns []string, target *GoTargetContext) (*model.App, error) {
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
		cfg.Env = hermeticGoEnvironment(nil)
	}
	if target != nil {
		cfg.Dir = target.ModuleRoot
		cfg.Env = hermeticGoEnvironment(target)
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
		return nil, fmt.Errorf("Go package loading failed: %s", strings.Join(loadErrors, "; "))
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

func GoTargetEnvironment(target GoTargetContext) []string {
	return hermeticGoEnvironment(&target)
}

func hermeticGoEnvironment(target *GoTargetContext) []string {
	blocked := map[string]bool{
		"AR": true, "CC": true, "CXX": true, "PKG_CONFIG": true,
		"CGO_CFLAGS": true, "CGO_CPPFLAGS": true, "CGO_CXXFLAGS": true, "CGO_FFLAGS": true, "CGO_LDFLAGS": true,
		"CPATH": true, "C_INCLUDE_PATH": true, "CPLUS_INCLUDE_PATH": true, "LIBRARY_PATH": true, "LD_LIBRARY_PATH": true,
		"DYLD_LIBRARY_PATH": true, "DYLD_FALLBACK_LIBRARY_PATH": true,
		"CGO_ENABLED": true, "GOARCH": true, "GOENV": true, "GOEXPERIMENT": true, "GOFLAGS": true,
		"GOMAXPROCS": true, "GOOS": true, "GOPROXY": true, "GOTOOLCHAIN": true, "GOWORK": true,
		"GO386": true, "GOAMD64": true, "GOARM": true, "GOARM64": true, "GOMIPS": true,
		"GOMIPS64": true, "GOPPC64": true, "GORISCV64": true, "GOWASM": true,
	}
	processEnvironment := envpolicy.Environ()
	environment := make([]string, 0, len(processEnvironment)+10)
	for _, value := range processEnvironment {
		name, _, _ := strings.Cut(value, "=")
		if !blocked[name] {
			environment = append(environment, value)
		}
	}
	goos, goarch, cgo, experiments := runtime.GOOS, runtime.GOARCH, "0", ""
	if target != nil {
		goos, goarch = target.GOOS, target.GOARCH
		if target.CGOEnabled {
			cgo = "1"
		}
		experiments = strings.Join(target.Experiments, ",")
	}
	environment = append(environment,
		"CGO_ENABLED="+cgo,
		"GOARCH="+goarch,
		"GOENV=off",
		"GOEXPERIMENT="+experiments,
		"GOFLAGS=",
		"GOMAXPROCS="+strconv.Itoa(min(runtime.GOMAXPROCS(0), goAnalysisMaxProcs)),
		"GOOS="+goos,
		"GOPROXY=off",
		"GOTOOLCHAIN="+resolvedGoToolchain(target),
		"GOWORK=off",
	)
	if target != nil {
		for name, value := range target.ArchitectureEnv {
			environment = append(environment, name+"="+value)
		}
		for name, value := range target.NativeToolEnv {
			environment = append(environment, name+"="+value)
		}
	}
	slices.Sort(environment)
	return environment
}

func resolvedGoToolchain(target *GoTargetContext) string {
	if target == nil || strings.TrimSpace(target.ToolchainVersion) == "" {
		return "local"
	}
	return "go" + strings.TrimPrefix(strings.TrimSpace(target.ToolchainVersion), "go")
}

func packageFilePaths(pkg *packages.Package) []string {
	switch {
	case len(pkg.CompiledGoFiles) > 0:
		return pkg.CompiledGoFiles
	default:
		return pkg.GoFiles
	}
}
