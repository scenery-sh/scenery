package generate

import (
	"sort"
)

func pagesForOperations(resources []Resource, byAddress map[string]Resource, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	var pages []Resource
	for _, page := range resources {
		if page.Kind != "scenery.page" {
			continue
		}
		binding := byAddress[resolveResourceRef(page, refString(page.Spec["load"]), "binding")]
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		if owned[operationAddress] {
			pages = append(pages, page)
		}
	}
	sort.Slice(pages, func(i, j int) bool { return pages[i].Address < pages[j].Address })
	return pages
}

func pageOwnedResourceAddresses(resources []Resource, byAddress map[string]Resource, operations []Resource) []string {
	pages := pagesForOperations(resources, byAddress, operations)
	owned := map[string]bool{}
	for _, page := range pages {
		owned[page.Address] = true
		for _, renderer := range renderersForPage(resources, page) {
			owned[renderer.Address] = true
		}
	}
	return sortedBoolKeys(owned)
}

func renderersForPage(resources []Resource, page Resource) []Resource {
	var renderers []Resource
	for _, renderer := range resources {
		if renderer.Kind == "scenery.renderer" && resolveResourceRef(renderer, refString(renderer.Spec["page"]), "page") == page.Address {
			renderers = append(renderers, renderer)
		}
	}
	sort.Slice(renderers, func(i, j int) bool { return renderers[i].Address < renderers[j].Address })
	return renderers
}
