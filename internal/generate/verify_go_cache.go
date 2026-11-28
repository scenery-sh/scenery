package generate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"scenery.sh/internal/atomicfile"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

const goVerifyCacheVersion = 1

var analyzeTargetWithSnapshotFunc = parse.AnalyzeTargetWithSnapshot

type goVerifyCacheState struct {
	Version int                             `json:"version"`
	Targets map[string]goVerifyTargetRecord `json:"targets"`
}

type goVerifyTargetRecord struct {
	Producer        string                           `json:"producer"`
	AppName         string                           `json:"app_name"`
	Role            string                           `json:"role"`
	Identity        string                           `json:"identity"`
	ModulePath      string                           `json:"module_path"`
	Dirs            map[string]string                `json:"dirs"`
	OverlayDirs     map[string]string                `json:"overlay_dirs"`
	ModuleContracts map[string]string                `json:"module_contracts"`
	Packages        map[string]parse.PackageSnapshot `json:"packages"`
}

func goVerifyCachePath(root string) string {
	return filepath.Join(root, ".scenery", "cache", "go-verify-state.json")
}

func readGoVerifyCache(root string) (goVerifyCacheState, bool) {
	path := goVerifyCachePath(root)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return goVerifyCacheState{}, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return goVerifyCacheState{}, false
	}
	var state goVerifyCacheState
	if err := json.Unmarshal(data, &state); err != nil || state.Version != goVerifyCacheVersion || state.Targets == nil {
		return goVerifyCacheState{}, false
	}
	return state, true
}

func writeGoVerifyCache(root string, state goVerifyCacheState) {
	encoded, err := json.Marshal(state)
	if err != nil {
		return
	}
	_ = atomicfile.Write(goVerifyCachePath(root), encoded, 0o644, atomicfile.Options{})
}

func verifyGoTargetsIncremental(ctx context.Context, result *compiler.Result, root string, overlay map[string][]byte, targets []compiler.GoBuildTarget) []Diagnostic {
	producerPtr := implementationProofProducer.Load()
	if producerPtr == nil || *producerPtr == "" {
		return verifyGoTargets(ctx, result, root, overlay, targets)
	}
	producer := *producerPtr
	state, hasState := readGoVerifyCache(root)
	if !hasState {
		state = goVerifyCacheState{Version: goVerifyCacheVersion, Targets: map[string]goVerifyTargetRecord{}}
	}
	moduleContracts := goVerifyModuleContractDigests(result.Manifest.Resources)
	updated := make(map[string]goVerifyTargetRecord, len(targets))
	var diagnostics []Diagnostic
	for _, target := range targets {
		record, diags := verifySingleGoTargetIncremental(ctx, result, root, overlay, target, producer, moduleContracts, state.Targets[target.Address])
		diagnostics = append(diagnostics, diags...)
		if record != nil {
			updated[target.Address] = *record
		}
	}
	if len(diagnostics) == 0 && len(updated) == len(targets) {
		for addr, rec := range updated {
			state.Targets[addr] = rec
		}
		writeGoVerifyCache(root, state)
	}
	return diagnostics
}

func verifySingleGoTargetIncremental(
	ctx context.Context,
	result *compiler.Result,
	root string,
	overlay map[string][]byte,
	target compiler.GoBuildTarget,
	producer string,
	moduleContracts map[string]string,
	cached goVerifyTargetRecord,
) (*goVerifyTargetRecord, []Diagnostic) {
	identity, dirs, stampErr := parse.CaptureTargetAnalysisStamp(target.Context)
	overlayDirs := overlayDirDigests(target.Context.ModuleRoot, overlay)
	if stampErr == nil && cached.Producer == producer && cached.AppName == result.Manifest.Application.Name && cached.Role == target.Role && cached.Identity == identity && len(cached.Packages) > 0 {
		if changedDirs, ok := planIncrementalGoVerifyDirs(result, target.Context.ModuleRoot, dirs, overlayDirs, moduleContracts, cached); ok {
			if len(changedDirs) == 0 {
				next := cached
				next.Dirs = dirs
				next.OverlayDirs = overlayDirs
				next.ModuleContracts = moduleContracts
				return &next, nil
			}
			if len(changedDirs) < len(cached.Packages) {
				if next, ok := runIncrementalGoVerify(ctx, result, root, overlay, target, dirs, overlayDirs, moduleContracts, cached, changedDirs); ok {
					return next, nil
				}
			}
		}
	}
	// Full verification fallback (guarantees identical diagnostics on errors or structural changes).
	sourceContext := target.Context
	verificationContext := sourceContext
	verificationContext.Patterns = slices.Clone(sourceContext.Patterns)
	appModel, snapshots, modulePath, err := analyzeTargetWithSnapshotFunc(ctx, root, result.Manifest.Application.Name, overlay, verificationContext, nil)
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil
		}
		if missing, mErr := parse.MissingHermeticModulePackages(sourceContext); mErr == nil && len(missing) > 0 {
			return nil, []Diagnostic{hermeticModuleCacheDiagnostic(target.Address, missing)}
		}
		return nil, []Diagnostic{{Code: "SCN6202", Severity: "error", Message: fmt.Sprintf("staged Go implementation verification failed for %s: %v", target.Address, err), Address: target.Address}}
	}
	var diagnostics []Diagnostic
	if target.Role != "contract" {
		diagnostics = append(diagnostics, validateNativeGoServices(appModel, result.Manifest.Resources)...)
		diagnostics = append(diagnostics, validateNativeGoHandlers(appModel, result.Manifest.Resources)...)
	}
	if len(diagnostics) > 0 || stampErr != nil {
		return nil, diagnostics
	}
	return &goVerifyTargetRecord{
		Producer:        producer,
		AppName:         result.Manifest.Application.Name,
		Role:            target.Role,
		Identity:        identity,
		ModulePath:      modulePath,
		Dirs:            dirs,
		OverlayDirs:     overlayDirs,
		ModuleContracts: moduleContracts,
		Packages:        snapshots,
	}, nil
}

func planIncrementalGoVerifyDirs(
	result *compiler.Result,
	moduleRoot string,
	dirs, overlayDirs, moduleContracts map[string]string,
	cached goVerifyTargetRecord,
) ([]string, bool) {
	if len(dirs) != len(cached.Dirs) || len(overlayDirs) != len(cached.OverlayDirs) || len(moduleContracts) != len(cached.ModuleContracts) {
		return nil, false
	}
	pkgByDir := make(map[string]parse.PackageSnapshot, len(cached.Packages))
	for _, pkg := range cached.Packages {
		pkgByDir[pkg.Dir] = pkg
	}
	changed := map[string]bool{}
	for dir, digest := range dirs {
		prev, ok := cached.Dirs[dir]
		if !ok {
			return nil, false
		}
		if prev != digest {
			if _, loaded := pkgByDir[dir]; !loaded {
				return nil, false
			}
			changed[dir] = true
		}
	}
	for dir, digest := range overlayDirs {
		prev, ok := cached.OverlayDirs[dir]
		if !ok {
			return nil, false
		}
		if prev != digest {
			if _, loaded := pkgByDir[dir]; !loaded {
				return nil, false
			}
			changed[dir] = true
		}
	}
	for mod, digest := range moduleContracts {
		prev, ok := cached.ModuleContracts[mod]
		if !ok {
			return nil, false
		}
		if prev != digest {
			dir, ok := moduleRelativeDir(result, moduleRoot, mod)
			if !ok {
				return nil, false
			}
			if _, loaded := pkgByDir[dir]; !loaded {
				return nil, false
			}
			changed[dir] = true
		}
	}
	out := make([]string, 0, len(changed))
	for dir := range changed {
		out = append(out, dir)
	}
	sort.Strings(out)
	return out, true
}

func runIncrementalGoVerify(
	ctx context.Context,
	result *compiler.Result,
	root string,
	overlay map[string][]byte,
	target compiler.GoBuildTarget,
	dirs, overlayDirs, moduleContracts map[string]string,
	cached goVerifyTargetRecord,
	changedDirs []string,
) (*goVerifyTargetRecord, bool) {
	toPatterns := func(relDirs []string) []string {
		patterns := make([]string, 0, len(relDirs))
		for _, dir := range relDirs {
			if dir == "" {
				patterns = append(patterns, ".")
			} else {
				patterns = append(patterns, "./"+dir)
			}
		}
		return patterns
	}
	appModel, loadedSnaps, _, err := analyzeTargetWithSnapshotFunc(ctx, root, result.Manifest.Application.Name, overlay, target.Context, toPatterns(changedDirs))
	if err != nil {
		return nil, false
	}
	loadedDirs := map[string]bool{}
	var apiChanged []string
	for importPath, snap := range loadedSnaps {
		prev, exists := cached.Packages[importPath]
		if !exists || prev.Dir != snap.Dir {
			return nil, false
		}
		loadedDirs[snap.Dir] = true
		if prev.API != snap.API {
			apiChanged = append(apiChanged, importPath)
		}
	}
	if len(loadedDirs) != len(changedDirs) {
		return nil, false
	}
	if importers := cachedImporterDirs(cached.Packages, apiChanged, loadedDirs); len(importers) > 0 {
		moreModel, moreSnaps, _, err := analyzeTargetWithSnapshotFunc(ctx, root, result.Manifest.Application.Name, overlay, target.Context, toPatterns(importers))
		if err != nil {
			return nil, false
		}
		moreDirs := map[string]bool{}
		for importPath, snap := range moreSnaps {
			prev, exists := cached.Packages[importPath]
			if !exists || prev.Dir != snap.Dir {
				return nil, false
			}
			moreDirs[snap.Dir] = true
			loadedSnaps[importPath] = snap
		}
		if len(moreDirs) != len(importers) {
			return nil, false
		}
		appModel.Packages = append(appModel.Packages, moreModel.Packages...)
	}
	if target.Role != "contract" {
		affected := affectedModulesForLoadedPackages(appModel, result.Manifest.Resources)
		if len(validateNativeGoServicesForModules(appModel, result.Manifest.Resources, affected)) > 0 {
			return nil, false
		}
		if len(validateNativeGoHandlersForModules(appModel, result.Manifest.Resources, affected)) > 0 {
			return nil, false
		}
	}
	mergedPkgs := make(map[string]parse.PackageSnapshot, len(cached.Packages))
	for k, v := range cached.Packages {
		mergedPkgs[k] = v
	}
	for k, v := range loadedSnaps {
		mergedPkgs[k] = v
	}
	return &goVerifyTargetRecord{
		Producer:        cached.Producer,
		AppName:         cached.AppName,
		Role:            cached.Role,
		Identity:        cached.Identity,
		ModulePath:      cached.ModulePath,
		Dirs:            dirs,
		OverlayDirs:     overlayDirs,
		ModuleContracts: moduleContracts,
		Packages:        mergedPkgs,
	}, true
}

func cachedImporterDirs(pkgs map[string]parse.PackageSnapshot, changed []string, loaded map[string]bool) []string {
	if len(changed) == 0 {
		return nil
	}
	importers := map[string][]string{}
	for path, pkg := range pkgs {
		for _, imported := range pkg.Imports {
			importers[imported] = append(importers[imported], path)
		}
	}
	affected := map[string]bool{}
	queue := slices.Clone(changed)
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		for _, imp := range importers[path] {
			if !affected[imp] {
				affected[imp] = true
				queue = append(queue, imp)
			}
		}
	}
	var dirs []string
	seen := map[string]bool{}
	for path := range affected {
		dir := pkgs[path].Dir
		if !loaded[dir] && !seen[dir] {
			seen[dir] = true
			dirs = append(dirs, dir)
		}
	}
	sort.Strings(dirs)
	return dirs
}

func affectedModulesForLoadedPackages(appModel *model.App, resources []Resource) map[string]bool {
	affected := map[string]bool{}
	for _, r := range resources {
		if r.Kind != "scenery.module" {
			continue
		}
		mod := moduleInstancePath(r)
		if nativeGoPackage(appModel, resources, mod) != nil {
			affected[mod] = true
		}
	}
	return affected
}

func overlayDirDigests(moduleRoot string, overlay map[string][]byte) map[string]string {
	if len(overlay) == 0 {
		return map[string]string{}
	}
	physModuleRoot, err := filepath.EvalSymlinks(moduleRoot)
	if err != nil {
		physModuleRoot = filepath.Clean(moduleRoot)
	}
	type entry struct {
		name string
		data []byte
	}
	byDir := map[string][]entry{}
	for path, data := range overlay {
		clean := filepath.Clean(path)
		if resolved, err := filepath.EvalSymlinks(filepath.Dir(clean)); err == nil {
			clean = filepath.Join(resolved, filepath.Base(clean))
		}
		rel, err := filepath.Rel(physModuleRoot, clean)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			byDir["__external__"] = append(byDir["__external__"], entry{name: filepath.ToSlash(clean), data: data})
			continue
		}
		dir := filepath.ToSlash(filepath.Dir(rel))
		if dir == "." {
			dir = ""
		}
		byDir[dir] = append(byDir[dir], entry{name: filepath.Base(clean), data: data})
	}
	out := make(map[string]string, len(byDir))
	for dir, entries := range byDir {
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		h := sha256.New()
		for _, e := range entries {
			sum := sha256.Sum256(e.data)
			_, _ = h.Write([]byte(e.name + "\x00" + hex.EncodeToString(sum[:]) + "\x00"))
		}
		out[dir] = hex.EncodeToString(h.Sum(nil))
	}
	return out
}

func goVerifyModuleContractDigests(resources []Resource) map[string]string {
	byMod := map[string][]Resource{}
	for _, r := range resources {
		mod := r.Module
		if r.Kind == "scenery.module" {
			mod = moduleInstancePath(r)
		}
		if mod == "" {
			mod = "app"
		}
		byMod[mod] = append(byMod[mod], r)
	}
	out := make(map[string]string, len(byMod))
	for mod, list := range byMod {
		sort.Slice(list, func(i, j int) bool { return list[i].Address < list[j].Address })
		b, err := MarshalCanonical(list)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		out[mod] = hex.EncodeToString(sum[:])
	}
	return out
}

func moduleRelativeDir(result *compiler.Result, moduleRoot, module string) (string, bool) {
	for _, r := range result.Manifest.Resources {
		if r.Kind != "scenery.module" || moduleInstancePath(r) != module {
			continue
		}
		src := stringValue(r.Spec["workspace_package_root"])
		if src == "" {
			src = stringValue(r.Spec["source"])
		}
		if src == "" {
			return "", false
		}
		absPkg := filepath.Join(result.Root, filepath.FromSlash(src))
		rel, err := filepath.Rel(moduleRoot, absPkg)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", false
		}
		slash := filepath.ToSlash(filepath.Clean(rel))
		if slash == "." {
			return "", true
		}
		return slash, true
	}
	return "", false
}
