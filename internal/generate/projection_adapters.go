package generate

import (
	"cmp"
	"fmt"
	"slices"
	"sync"

	"scenery.sh/internal/compiler"
)

// Adapters depend on declarations, not the build-specific identities embedded
// in composition and its descriptor. Keep their pure value cache separate so
// ordinary implementation edits still rebuild those identity-bearing outputs.
var adapterProjections = struct {
	sync.Mutex
	entries map[[32]byte][]applicationAdapter
	bytes   int
}{entries: make(map[[32]byte][]applicationAdapter)}

func cachedApplicationAdapters(input projectionInput, generatedImport string, render func() ([]applicationAdapter, error)) ([]applicationAdapter, error) {
	key, err := input.key("go-application-adapters", generatedImport)
	if err != nil {
		return render()
	}
	adapterProjections.Lock()
	adapters, found := adapterProjections.entries[key]
	if found {
		adapters = cloneApplicationAdapters(adapters)
	}
	adapterProjections.Unlock()
	if found {
		return adapters, nil
	}
	adapters, err = render()
	if err != nil {
		return nil, err
	}
	size := 0
	for _, adapter := range adapters {
		size += len(adapter.Source) + len(adapter.Address) + len(adapter.ImportPath) + len(adapter.PackageName) + len(adapter.RelativeDir) + len(adapter.PackageABI) + len(adapter.Implementation) + len(adapter.Contract)
		for _, address := range adapter.Covered {
			size += len(address)
		}
	}
	if size <= projectionCacheLimit {
		adapterProjections.Lock()
		if _, exists := adapterProjections.entries[key]; !exists {
			if adapterProjections.bytes+size > projectionCacheLimit || len(adapterProjections.entries) >= 8 {
				clear(adapterProjections.entries)
				adapterProjections.bytes = 0
			}
			adapterProjections.entries[key] = cloneApplicationAdapters(adapters)
			adapterProjections.bytes += size
		}
		adapterProjections.Unlock()
	}
	return adapters, nil
}

func cloneApplicationAdapters(adapters []applicationAdapter) []applicationAdapter {
	cloned := slices.Clone(adapters)
	for i := range cloned {
		cloned[i].Source = slices.Clone(adapters[i].Source)
		cloned[i].Covered = slices.Clone(adapters[i].Covered)
	}
	return cloned
}

func renderApplicationAdapters(result *Result, idx *resourceIndex, generatedImport string) ([]applicationAdapter, error) {
	var adapters []applicationAdapter
	err := forEachNativeServiceModule(result, func(module, service Resource) error {
		adapter, err := renderApplicationAdapter(result, idx, module, service, generatedImport)
		if err == nil {
			adapters = append(adapters, adapter)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(adapters, func(a, b applicationAdapter) int {
		return cmp.Compare(a.Address, b.Address)
	})
	return adapters, nil
}

// planApplicationAdapters returns adapter identities without rendering sources,
// so build preparation can list service processes on every rebuild cheaply.
func planApplicationAdapters(result *Result, generatedImport string) ([]applicationAdapterPlan, error) {
	var plans []applicationAdapterPlan
	byAddress := resourcesByAddress(result.Manifest)
	err := forEachNativeServiceModule(result, func(module, service Resource) error {
		plan, err := planApplicationAdapter(result, byAddress, module, service, generatedImport)
		if err == nil {
			plans = append(plans, plan)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(plans, func(a, b applicationAdapterPlan) int {
		return cmp.Compare(a.Address, b.Address)
	})
	return plans, nil
}

func forEachNativeServiceModule(result *Result, visit func(module, service Resource) error) error {
	modules := map[string]Resource{}
	for _, module := range localModuleInstances(result.Manifest.Resources) {
		modules[moduleInstancePath(module)] = module
	}
	for _, service := range compiler.RuntimeServices(result.Manifest.Resources) {
		module, ok := modules[service.Module]
		if !ok {
			return fmt.Errorf("native service %s is not owned by a local module", service.Address)
		}
		if err := visit(module, service); err != nil {
			return err
		}
	}
	return nil
}
