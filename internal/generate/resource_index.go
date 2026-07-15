package generate

import (
	"sort"
	"strings"
)

// resourceIndex caches per-manifest lookup tables that generation builds once
// and reuses across module, service, and type-normalization loops instead of
// rescanning every resource per call.
type resourceIndex struct {
	byAddress              map[string]Resource
	contractImportByModule map[string]string
	resourcesByModule      map[string][]Resource
	moduleDeclsByInstance  map[string][]Resource
}

func newResourceIndex(resources []Resource) *resourceIndex {
	idx := &resourceIndex{
		byAddress:              make(map[string]Resource, len(resources)),
		contractImportByModule: map[string]string{},
		resourcesByModule:      map[string][]Resource{},
		moduleDeclsByInstance:  map[string][]Resource{},
	}
	for _, resource := range resources {
		idx.byAddress[resource.Address] = resource
		idx.resourcesByModule[resource.Module] = append(idx.resourcesByModule[resource.Module], resource)
		if resource.Kind != "scenery.module" {
			continue
		}
		instance := moduleInstancePath(resource)
		idx.moduleDeclsByInstance[instance] = append(idx.moduleDeclsByInstance[instance], resource)
		if _, exists := idx.contractImportByModule[instance]; exists {
			continue
		}
		metadata, _ := resource.Spec["package"].(map[string]any)
		goContract, _ := metadata["go_contract"].(map[string]any)
		importPath := strings.TrimSpace(stringValue(goContract["import_path"]))
		if importPath != "" {
			idx.contractImportByModule[instance] = strings.TrimSuffix(importPath, "/") + "/scenerycontract"
		}
	}
	for _, moduleResources := range idx.resourcesByModule {
		sort.Slice(moduleResources, func(i, j int) bool { return moduleResources[i].Address < moduleResources[j].Address })
	}
	return idx
}

// contractImport mirrors moduleContractImportPath for a prebuilt index.
func (idx *resourceIndex) contractImport(module string) (string, bool) {
	importPath, ok := idx.contractImportByModule[module]
	return importPath, ok
}

// moduleResources mirrors the moduleResources helper for a prebuilt index:
// every resource owned by the module instance, sorted by address.
func (idx *resourceIndex) moduleResources(module string) []Resource {
	return idx.resourcesByModule[module]
}

// moduleDecls returns the scenery.module resources whose instance path is
// module, in manifest order.
func (idx *resourceIndex) moduleDecls(module string) []Resource {
	return idx.moduleDeclsByInstance[module]
}
