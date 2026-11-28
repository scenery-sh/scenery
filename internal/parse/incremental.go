package parse

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"

	"scenery.sh/internal/app"
	"scenery.sh/internal/gotarget"
	"scenery.sh/internal/model"
)

// PackageSnapshot records the persistent metadata of one analyzed Go package
// so later processes can reload only changed packages and affected importers.
type PackageSnapshot struct {
	Dir        string   `json:"dir"`
	ImportPath string   `json:"import_path"`
	Imports    []string `json:"imports"`
	API        string   `json:"api"`
}

// CaptureTargetAnalysisStamp returns the target identity digest and per-dir
// Go source digests for target, matching AnalysisInputDigests.
func CaptureTargetAnalysisStamp(target gotarget.Context) (string, map[string]string, error) {
	if len(target.Patterns) == 0 {
		return "", nil, errors.New("go target has no package patterns")
	}
	moduleRoot, err := filepath.EvalSymlinks(target.ModuleRoot)
	if err != nil {
		return "", nil, err
	}
	tool, resolved := gotarget.ToolIdentity(gotarget.Hermetic(&target))
	if !resolved {
		return "", nil, errors.New("the target environment selects no go command")
	}
	flags := gotarget.WithTrimpath(append([]string(nil), target.BuildFlags...))
	if len(target.BuildTags) > 0 {
		flags = append(flags, "-tags="+strings.Join(target.BuildTags, ","))
	}
	linkedRoot, _, err := app.LinkedFrameworkReplaceRoot(moduleRoot)
	if err != nil {
		return "", nil, err
	}
	stamp, err := captureAnalysisStamp(moduleRoot, &target, tool, flags, target.Patterns, linkedRoot)
	if err != nil {
		return "", nil, err
	}
	return stamp.identity, stamp.dirs, nil
}

// AnalyzeTargetWithSnapshot loads patterns (or target.Patterns when empty) for
// target with overlay, returning the analyzed model and per-package snapshots.
func AnalyzeTargetWithSnapshot(ctx context.Context, root, name string, overlay map[string][]byte, target gotarget.Context, patterns []string) (*model.App, map[string]PackageSnapshot, string, error) {
	if len(patterns) == 0 {
		patterns = target.Patterns
	}
	if len(patterns) == 0 {
		return nil, nil, "", errors.New("go target has no package patterns")
	}
	logicalRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, nil, "", err
	}
	physicalRoot, err := filepath.EvalSymlinks(logicalRoot)
	if err != nil {
		return nil, nil, "", err
	}
	canonicalOverlay := canonicalizeAnalysisOverlay(logicalRoot, physicalRoot, overlay)
	moduleRoot, err := filepath.EvalSymlinks(target.ModuleRoot)
	if err != nil {
		return nil, nil, "", err
	}
	cfg := &packages.Config{
		Context: ctx,
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedTypes |
			packages.NeedImports |
			packages.NeedModule,
		Dir:        moduleRoot,
		Overlay:    canonicalOverlay,
		Env:        gotarget.Hermetic(&target),
		BuildFlags: gotarget.WithTrimpath(append([]string(nil), target.BuildFlags...)),
	}
	if len(target.BuildTags) > 0 {
		cfg.BuildFlags = append(cfg.BuildFlags, "-tags="+strings.Join(target.BuildTags, ","))
	}
	linkedRoot, linked, err := app.LinkedFrameworkReplaceRoot(cfg.Dir)
	if err != nil {
		return nil, nil, "", err
	}
	if canonicalOverlay != nil || linked {
		modFile, cleanup, err := prepareAnalysisModFile(cfg.Dir, linkedRoot)
		if err != nil {
			return nil, nil, "", err
		}
		defer cleanup()
		if canonicalOverlay != nil {
			cfg.BuildFlags = append(cfg.BuildFlags, "-mod=mod", "-modfile="+modFile)
		} else {
			cfg.BuildFlags = append(cfg.BuildFlags, "-modfile="+modFile)
		}
	}
	loaded, modulePath, err := loadAnalysisPackages(cfg, physicalRoot, patterns)
	if err != nil {
		return nil, nil, "", err
	}
	snapshots := make(map[string]PackageSnapshot, len(loaded))
	appModel := &model.App{Name: name, Root: physicalRoot, ModulePath: modulePath}
	for _, pkg := range loaded {
		appModel.Packages = append(appModel.Packages, pkg.model)
		snapshots[pkg.model.ImportPath] = PackageSnapshot{
			Dir:        pkg.dir,
			ImportPath: pkg.model.ImportPath,
			Imports:    slices.Clone(pkg.imports),
			API:        pkg.api,
		}
	}
	slices.SortFunc(appModel.Packages, func(left, right *model.Package) int {
		return strings.Compare(left.RelDir, right.RelDir)
	})
	return appModel, snapshots, modulePath, nil
}
