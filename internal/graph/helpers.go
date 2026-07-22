package graph

import (
	"path/filepath"
	"sort"
	"strings"
)

var rootResourceKinds = map[string]bool{
	"go_module": true, "go_toolchain": true, "go_target": true,
	"http_gateway": true, "authentication": true, "authorization": true,
	"workload_identity": true, "pipeline": true, "provider": true,
	"data_source": true, "execution_engine": true, "event_bus": true,
	"secret_store": true, "secret": true, "deployment": true,
	"typescript_client": true, "patch": true,
}

func resourcesByAddress(manifest *Manifest) map[string]Resource {
	resources := map[string]Resource{}
	if manifest == nil {
		return resources
	}
	for _, resource := range manifest.Resources {
		resources[resource.Address] = resource
	}
	return resources
}

func ResourceAddress(module, blockType, name string) string {
	if module == "" {
		module = "app"
	}
	return filepath.ToSlash(module + "/" + blockType + "/" + name)
}

func ModuleResourceAddress(instance string) string {
	parts := strings.Split(strings.Trim(instance, "/"), "/")
	if len(parts) <= 1 {
		return ResourceAddress("app", "module", instance)
	}
	return ResourceAddress(strings.Join(parts[:len(parts)-1], "/"), "module", parts[len(parts)-1])
}

func KindForBlock(blockType string) string {
	return "scenery." + strings.ReplaceAll(blockType, "_", "-")
}

func escapeJSONPointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}

func canonicalStrings(values []string) []string {
	set := map[string]bool{}
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	result := make([]string, 0, len(set))
	for value := range set {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func oneOf[T comparable](value T, candidates ...T) bool {
	for _, candidate := range candidates {
		if value == candidate {
			return true
		}
	}
	return false
}
