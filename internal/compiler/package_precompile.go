package compiler

import (
	"runtime"
	"sync"
	"sync/atomic"
)

type precompiledPackageKey struct{ directory, module, logicalBase string }

type precompiledPackage struct {
	resources   []Resource
	sources     []*Source
	diagnostics []Diagnostic
}

// precompiledPackages holds packages loaded ahead of the module loop. Loading
// a package reads and parses its own source files and validates them against
// the block schemas; it consumes nothing of another module, so the root
// modules' packages are loaded together while their instantiation, which
// resolves inputs and exports against the modules before it, stays in order.
type precompiledPackages map[precompiledPackageKey]precompiledPackage

// compile returns the package loaded ahead for exactly these arguments, once,
// and loads any other package now.
func (packages precompiledPackages) compile(root, directory, module, logicalBase string) ([]Resource, []*Source, []Diagnostic) {
	key := precompiledPackageKey{directory, module, logicalBase}
	if loaded, ok := packages[key]; ok {
		delete(packages, key)
		return loaded.resources, loaded.sources, loaded.diagnostics
	}
	return compilePackageLogical(root, directory, module, logicalBase)
}

// precompileRootPackages loads the package of every root module whose source
// resolves. A module whose source does not resolve is left to the module
// loop, which reports it.
func precompileRootPackages(root string, modules []*Block, lockfile *Lockfile) precompiledPackages {
	keys := make([]precompiledPackageKey, 0, len(modules))
	seen := map[precompiledPackageKey]bool{}
	for _, module := range modules {
		if module == nil || len(module.Labels) != 1 {
			continue
		}
		source, err := requireLiteralString(module, "source")
		if err != nil {
			continue
		}
		location, err := resolveModuleLocation(root, root, source, lockfile)
		if err != nil {
			continue
		}
		key := precompiledPackageKey{location.Directory, module.Labels[0], location.LogicalBase}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) < 2 {
		return nil
	}
	loaded := make([]precompiledPackage, len(keys))
	var next atomic.Int64
	var workers sync.WaitGroup
	for range min(runtime.GOMAXPROCS(0), 8, len(keys)) {
		workers.Go(func() {
			for index := int(next.Add(1)) - 1; index < len(keys); index = int(next.Add(1)) - 1 {
				key := keys[index]
				loaded[index].resources, loaded[index].sources, loaded[index].diagnostics = compilePackageLogical(root, key.directory, key.module, key.logicalBase)
			}
		})
	}
	workers.Wait()
	packages := make(precompiledPackages, len(keys))
	for index, key := range keys {
		packages[key] = loaded[index]
	}
	return packages
}
