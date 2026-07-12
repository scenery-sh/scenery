package vnext

import "sort"

const (
	openAPIGeneratorProfile = "scenery.openapi/v1"
	openAPIVersion          = "3.1.1"
)

func computeHTTPProjectionRevisions(manifest *Manifest) (map[string]string, map[string]string) {
	httpRevisions, openAPIRevisions := map[string]string{}, map[string]string{}
	if manifest == nil {
		return httpRevisions, openAPIRevisions
	}
	for _, gateway := range manifest.Resources {
		if gateway.Kind != "scenery.http-gateway/v1" {
			continue
		}
		projection := httpGatewayProjection(manifest.Resources, gateway)
		httpRevision := revisionHash("scenery.http-surface-revision.v1\x00", projection)
		httpRevisions[gateway.Address] = httpRevision
		openAPIRevisions[gateway.Address] = revisionHash("scenery.openapi-revision.v1\x00", map[string]any{
			"http_surface_revision": httpRevision,
			"generator_profile":     openAPIGeneratorProfile,
			"openapi_version":       openAPIVersion,
			"projection_options":    map[string]any{"exact_scalars": true, "include_standard_failures": true},
		})
	}
	return httpRevisions, openAPIRevisions
}

func httpGatewayProjection(resources []Resource, gateway Resource) map[string]any {
	bindings := httpBindingsForGateway(resources, gateway)
	reachable := reachableResources(resources, bindings)
	selected := map[string]Resource{gateway.Address: gateway}
	for _, resource := range append(bindings, reachable...) {
		selected[resource.Address] = resource
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, binding := range bindings {
		for _, reference := range []struct{ field, kind string }{{"authentication", "authentication"}, {"authorization", "authorization"}, {"pipeline", "pipeline"}} {
			address := resolveResourceRef(binding, refString(binding.Spec[reference.field]), reference.kind)
			if resource, ok := byAddress[address]; ok {
				selected[address] = resource
			}
		}
	}
	projected := make([]Resource, 0, len(selected))
	for _, resource := range selected {
		value, include := contractResourceProjection(resource)
		if include {
			projected = append(projected, value)
		}
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Address < projected[j].Address })
	projection := map[string]any{"profile": "scenery.http-codec/v1", "gateway": gateway.Address, "resources": projected}
	if bindingsUseHTTPPathTail(bindings) {
		projection["extension_profiles"] = []string{HTTPPathTailProfile}
	}
	return projection
}

func httpBindingsForGateway(resources []Resource, gateway Resource) []Resource {
	var bindings []Resource
	for _, binding := range resources {
		if binding.Kind != "scenery.binding/v1" || stringValue(binding.Spec["protocol"]) != "http" {
			continue
		}
		if resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway") == gateway.Address {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func projectionRevisionsForGateways(result *Result, references []string, revisions map[string]string) map[string]string {
	selected := map[string]string{}
	if result == nil || result.Manifest == nil {
		return selected
	}
	for _, reference := range references {
		address := resolveResourceRef(Resource{Module: "app"}, reference, "http_gateway")
		if revision := revisions[address]; revision != "" {
			selected[address] = revision
		}
	}
	return selected
}
