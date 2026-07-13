package generate

import (
	"fmt"
	"sort"
	"strings"
)

func pagesForOperations(resources, operations []Resource) []Resource {
	owned := operationAddressSet(operations)
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
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

func pageOwnedResourceAddresses(resources, operations []Resource) []string {
	pages := pagesForOperations(resources, operations)
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

func renderPageRegistrations(b *strings.Builder, resources, operations []Resource) error {
	for _, page := range pagesForOperations(resources, operations) {
		load := resolveResourceRef(page, refString(page.Spec["load"]), "binding")
		actions := map[string]string{}
		for _, action := range namedChildren(page.Spec, "action") {
			actions[stringValue(action["name"])] = resolveResourceRef(page, refString(action["invoke"]), "binding")
		}
		var renderers []string
		for _, renderer := range renderersForPage(resources, page) {
			configJSON := ""
			if renderer.Spec["config"] != nil {
				encoded, err := MarshalCanonical(renderer.Spec["config"])
				if err != nil {
					return fmt.Errorf("renderer %s config: %w", renderer.Address, err)
				}
				configJSON = string(encoded)
			}
			renderers = append(renderers, fmt.Sprintf("{Address: %q, Runtime: %q, Module: %q, ImplementationDigest: %q, ConfigJSON: %q}", renderer.Address, stringValue(renderer.Spec["runtime"]), stringValue(renderer.Spec["module"]), stringValue(renderer.Spec["implementation_digest"]), configJSON))
		}
		fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterContractPage(sceneryruntime.ContractPageRegistration{Address: %q, Package: %q, Path: %q, LoadBinding: %q, Actions: %s, Renderers: []sceneryruntime.ContractRendererRegistration{%s}}); err != nil { return err }\n", page.Address, page.Module, stringValue(page.Spec["path"]), load, goStringStringMap(actions), strings.Join(renderers, ", "))
	}
	return nil
}
