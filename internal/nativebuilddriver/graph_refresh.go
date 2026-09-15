package nativebuilddriver

import (
	"path/filepath"
	"sort"
)

// GraphRefreshPackages returns the exact packages whose stock-Go actions must
// be observed after the captured package graph changes. Changed packages seed
// the frontier; only their current transitive consumers are added. The target
// main package is always included so the refresh records a current link action.
func (recipe *Recipe) GraphRefreshPackages(current Capture) []string {
	baseline := recipe.currentCapture()
	seeded := map[string]bool{}
	for importPath, pkg := range current.Packages {
		previous, existed := baseline.Packages[importPath]
		if importPath != "unsafe" && (!existed || !samePackageSelection(previous, pkg) || recipe.Compiles[importPath] == nil) {
			seeded[importPath] = true
		}
		if importPath != "unsafe" && pkg.Name == "main" && withinWorkspace(recipe.Workspace, pkg.Dir) {
			seeded[importPath] = true
		}
	}
	changedFiles := map[string]bool{}
	for path, digest := range baseline.Files {
		if current.Files[path] != digest {
			changedFiles[path] = true
		}
	}
	for path, digest := range current.Files {
		if baseline.Files[path] != digest {
			changedFiles[path] = true
		}
	}
	for path := range changedFiles {
		for _, importPath := range packageOwnersForFile(current.Packages, path) {
			if importPath != "unsafe" {
				seeded[importPath] = true
			}
		}
		for _, importPath := range packageOwnersForFile(baseline.Packages, path) {
			if _, exists := current.Packages[importPath]; importPath != "unsafe" && exists {
				seeded[importPath] = true
			}
		}
	}
	queue := make([]string, 0, len(seeded))
	for importPath := range seeded {
		queue = append(queue, importPath)
	}
	for len(queue) > 0 {
		dependency := queue[0]
		queue = queue[1:]
		for consumer, pkg := range current.Packages {
			if consumer != "unsafe" && !seeded[consumer] && contains(pkg.Imports, dependency) {
				seeded[consumer] = true
				queue = append(queue, consumer)
			}
		}
	}
	result := make([]string, 0, len(seeded))
	for importPath := range seeded {
		result = append(result, importPath)
	}
	sort.Strings(result)
	return result
}

func packageOwnersForFile(packages map[string]Package, path string) []string {
	path = filepath.Clean(path)
	var owners []string
	for importPath, pkg := range packages {
		groups := [][]string{pkg.GoFiles, pkg.CgoFiles, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles, pkg.EmbedFiles, pkg.IgnoredGoFiles, pkg.IgnoredOtherFiles}
		owned := false
		for _, group := range groups {
			for _, file := range group {
				if filepath.Clean(filepath.Join(pkg.Dir, file)) == path {
					owned = true
					break
				}
			}
			if owned {
				break
			}
		}
		if !owned && pkg.Module != nil {
			module := pkg.Module.GoMod
			if pkg.Module.Replace != nil && pkg.Module.Replace.GoMod != "" {
				module = pkg.Module.Replace.GoMod
			}
			owned = module != "" && (filepath.Clean(module) == path || filepath.Clean(filepath.Join(filepath.Dir(module), "go.sum")) == path)
		}
		if owned {
			owners = append(owners, importPath)
		}
	}
	sort.Strings(owners)
	return owners
}
