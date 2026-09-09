package compiler

import (
	"fmt"
	"path"
	"strings"
)

// applyHTTPDirectoryGroups runs once after patches, before CRUD expansion.
// Downstream consumers use the effective path without inspecting source folders.
func applyHTTPDirectoryGroups(resources []Resource) []Diagnostic {
	modules := make(map[string]Resource)
	for _, resource := range resources {
		if resource.Kind == "scenery.module" {
			modules[resource.Address] = resource
		}
	}
	var diagnostics []Diagnostic
	for index := range resources {
		resource := &resources[index]
		if resource.Kind != "scenery.crud" && (resource.Kind != "scenery.binding" || resource.Spec["protocol"] != "http") {
			continue
		}
		httpSpec, _ := resource.Spec["http"].(map[string]any)
		bindingPath, ok := httpSpec["path"].(string)
		if !ok {
			continue
		}
		owner, ok := modules[moduleResourceAddress(resource.Module)]
		if !ok || moduleHasRegistryAncestor(resource.Module, modules) {
			continue
		}
		packageRoot := stringValue(owner.Spec["workspace_package_root"])
		if packageRoot == "" || packageRoot == "." {
			continue
		}
		group := path.Dir(packageRoot)
		if group == "." {
			continue
		}
		if packageRoot != path.Clean(packageRoot) || path.IsAbs(packageRoot) || !validHTTPDirectoryGroup(group) {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SCN2102", Severity: "error", Address: resource.Address,
				Message: fmt.Sprintf("HTTP directory group %q must contain only URL-safe literal segments (letters, digits, '.', '_', '~', '-')", group),
				Range:   resource.Origin.DeclarationRange,
			})
			continue
		}
		// Do not repair an invalid authored path by prepending a valid group.
		if _, err := parseHTTPPathTemplate(bindingPath); err != nil {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SCN2102", Severity: "error", Address: resource.Address,
				Message: "invalid HTTP path before directory grouping: " + err.Error(),
				Range:   resource.Origin.DeclarationRange,
			})
			continue
		}
		grouped := "/" + group
		if bindingPath != "/" {
			grouped += bindingPath
		}
		httpSpec["path"] = grouped
		field := nearestFieldProvenance(resource.Origin, "/spec/http/path")
		field.Kind = "derived"
		field.ProvidedBy = owner.Address
		field.SourceAddress = resource.Address
		field.Transformations = appendUniqueString(field.Transformations, "directory_group_prefix")
		setFieldProvenance(&resource.Origin, "/spec/http/path", grouped, field)
	}
	return diagnostics
}

func moduleHasRegistryAncestor(instance string, modules map[string]Resource) bool {
	for instance != "" && instance != "app" && instance != "." {
		if module := modules[moduleResourceAddress(instance)]; module.Spec["locked_integrity"] != nil {
			return true
		}
		instance = path.Dir(instance)
	}
	return false
}

func validHTTPDirectoryGroup(group string) bool {
	if strings.HasPrefix(group, "/") || path.Clean(group) != group {
		return false
	}
	for _, segment := range strings.Split(group, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		for _, char := range segment {
			if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || strings.ContainsRune("._~-", char) {
				continue
			}
			return false
		}
	}
	return true
}
