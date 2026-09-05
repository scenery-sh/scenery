package generate

import "strings"

type tsRuntimeCapabilities struct {
	query          bool
	requestHeader  bool
	requestCookie  bool
	responseHeader bool
	responseCookie bool
	multipart      bool
	retry          bool
	validation     bool
}

func allTSRuntimeCapabilities() tsRuntimeCapabilities {
	return tsRuntimeCapabilities{
		query:          true,
		requestHeader:  true,
		requestCookie:  true,
		responseHeader: true,
		responseCookie: true,
		multipart:      true,
		retry:          true,
		validation:     true,
	}
}

func tsRuntimeCapabilitiesFor(target Resource, bindings, resources []Resource) tsRuntimeCapabilities {
	capabilities := tsRuntimeCapabilities{retry: tsRetryTarget(target).Enabled}
	for _, resource := range reachableResources(resources, bindings) {
		if resource.Kind == "scenery.record" && len(namedChildren(resource.Spec, "validation")) > 0 {
			capabilities.validation = true
			break
		}
	}
	for _, method := range tsClientMethods(bindings, resources) {
		descriptor := method.descriptor
		if _, ok := descriptor["query"]; ok {
			capabilities.query = true
		}
		if _, ok := descriptor["headers"]; ok {
			capabilities.requestHeader = true
		}
		if _, ok := descriptor["cookies"]; ok {
			capabilities.requestCookie = true
		}
		if body, ok := descriptor["body"].(map[string]any); ok && body["codec"] == "multipart" {
			capabilities.multipart = true
		}
		for _, raw := range tsBindingResponseList(descriptor) {
			response, _ := raw.(map[string]any)
			if _, ok := response["headers"]; ok {
				capabilities.responseHeader = true
			}
			if _, ok := response["cookies"]; ok {
				capabilities.responseCookie = true
			}
		}
	}
	return capabilities
}

func renderTSRuntime(capabilities tsRuntimeCapabilities) string {
	source := renderTSRuntimePublic() + renderTSRuntimeInternals() + renderTSRuntimeInvoke()
	if capabilities.validation {
		source += renderTSRuntimeValidation()
	}
	features := []struct {
		name string
		keep bool
	}{
		{"query", capabilities.query},
		{"request_header", capabilities.requestHeader},
		{"request_cookie", capabilities.requestCookie},
		{"response_header", capabilities.responseHeader},
		{"response_cookie", capabilities.responseCookie},
		{"response_metadata", capabilities.responseHeader || capabilities.responseCookie},
		{"multipart", capabilities.multipart},
		{"retry", capabilities.retry},
		{"no_retry", !capabilities.retry},
		{"validation", capabilities.validation},
	}
	for _, feature := range features {
		source = selectTSRuntimeSections(source, feature.name, feature.keep)
	}
	if strings.Contains(source, "/*__scenery_runtime_") {
		panic("unresolved TypeScript runtime capability section")
	}
	return strings.TrimRight(strings.ReplaceAll(source, "§", "`"), "\n") + "\n"
}

func selectTSRuntimeSections(source, name string, keep bool) string {
	startMarker := "/*__scenery_runtime_" + name + "_start__*/"
	endMarker := "/*__scenery_runtime_" + name + "_end__*/"
	for {
		start := strings.Index(source, startMarker)
		if start < 0 {
			return source
		}
		relativeEnd := strings.Index(source[start+len(startMarker):], endMarker)
		if relativeEnd < 0 {
			panic("unclosed TypeScript runtime capability section " + name)
		}
		end := start + len(startMarker) + relativeEnd
		if keep {
			source = source[:start] + source[start+len(startMarker):end] + source[end+len(endMarker):]
			continue
		}
		source = source[:start] + source[end+len(endMarker):]
	}
}
