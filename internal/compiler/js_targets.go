package compiler

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
)

var jsExportName = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

type JSWorkerTarget struct {
	Directory string
	Workspace string
	Entry     string
	Version   string
}

func validateJSTargets(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	workspaces := make(map[string]string)
	targets := make(map[[2]string]string)
	var diagnostics []Diagnostic
	report := func(resource Resource, code, message string) {
		diagnostics = append(diagnostics, Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address})
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.js-workspace":
			relative := stringValue(resource.Spec["root"])
			directory := filepath.Join(root, filepath.FromSlash(relative))
			info, err := os.Lstat(directory)
			if !filepath.IsLocal(relative) || err != nil || !info.IsDir() || rejectPathSymlinks(root, directory) != nil {
				report(resource, "SCN6164", "js_workspace root must be a workspace-relative directory without symlinks")
				continue
			}
			if previous := workspaces[directory]; previous != "" {
				report(resource, "SCN6164", "js_workspace root is also owned by "+previous)
			}
			workspaces[directory] = resource.Address
		case "scenery.js-toolchain":
			if !semanticVersionExact(stringValue(resource.Spec["version"])) {
				report(resource, "SCN6165", "js_toolchain version requires an exact Bun release such as 1.3.14")
			}
		case "scenery.js-target":
			role := stringValue(resource.Spec["role"])
			workspace := resolveResourceRef(resource, refString(resource.Spec["workspace"]), "js_workspace")
			toolchain := resolveResourceRef(resource, refString(resource.Spec["toolchain"]), "js_toolchain")
			if (role != "development" && role != "artifact") || byAddress[workspace].Kind != "scenery.js-workspace" || byAddress[toolchain].Kind != "scenery.js-toolchain" {
				report(resource, "SCN6166", "js_target requires role development or artifact and resolved workspace and toolchain references")
				continue
			}
			key := [2]string{workspace, role}
			if previous := targets[key]; previous != "" {
				report(resource, "SCN6166", "js_target duplicates the role and workspace of "+previous)
			}
			targets[key] = resource.Address
		}
	}
	for _, resource := range resources {
		if resource.Kind != "scenery.service" || !IsJSRuntime(stringValue(resource.Spec["runtime"])) {
			continue
		}
		if _, err := selectJSWorkerTarget(root, resources, byAddress, resource, "development"); err != nil {
			report(resource, "SCN6167", err.Error())
		}
	}
	return diagnostics
}

func SelectJSWorkerTarget(result *Result, service Resource, role string) (JSWorkerTarget, error) {
	return selectJSWorkerTarget(result.Root, result.Manifest.Resources, resourcesByAddress(result.Manifest), service, role)
}

func selectJSWorkerTarget(root string, resources []Resource, byAddress map[string]Resource, service Resource, role string) (JSWorkerTarget, error) {
	implementation, _ := service.Spec["implementation"].(map[string]any)
	if !jsExportName.MatchString(stringValue(implementation["constructor"])) {
		return JSWorkerTarget{}, fmt.Errorf("JavaScript service constructor must name an exported function")
	}
	moduleIndex := slices.IndexFunc(resources, func(resource Resource) bool {
		return resource.Kind == "scenery.module" && moduleInstancePath(resource) == service.Module
	})
	if moduleIndex < 0 {
		return JSWorkerTarget{}, fmt.Errorf("JavaScript service requires its own module directory")
	}
	module := resources[moduleIndex]
	metadata, _ := module.Spec["package"].(map[string]any)
	declaration, _ := metadata["js_contract"].(map[string]any)
	target := JSWorkerTarget{
		Directory: stringValue(module.Spec["workspace_package_root"]),
		Entry:     stringValue(declaration["entry"]),
	}
	entryPath := filepath.Join(root, target.Directory, filepath.FromSlash(target.Entry))
	info, err := os.Lstat(entryPath)
	extensions := []string{".js", ".mjs"}
	if stringValue(service.Spec["runtime"]) == "ts" {
		extensions = []string{".ts", ".mts"}
	}
	if !filepath.IsLocal(target.Directory) || !filepath.IsLocal(target.Entry) || !slices.Contains(extensions, filepath.Ext(target.Entry)) ||
		err != nil || !info.Mode().IsRegular() || rejectPathSymlinks(root, entryPath) != nil {
		return JSWorkerTarget{}, fmt.Errorf("js_contract entry must be a regular module-relative source file matching the service runtime")
	}

	best := -1
	for _, resource := range resources {
		if resource.Kind != "scenery.js-target" || stringValue(resource.Spec["role"]) != role {
			continue
		}
		workspace := byAddress[resolveResourceRef(resource, refString(resource.Spec["workspace"]), "js_workspace")]
		toolchain := byAddress[resolveResourceRef(resource, refString(resource.Spec["toolchain"]), "js_toolchain")]
		relative := filepath.FromSlash(stringValue(workspace.Spec["root"]))
		clean := filepath.Clean(relative)
		if !filepath.IsLocal(relative) || !pathWithin(filepath.Join(root, clean), filepath.Join(root, target.Directory)) || len(clean) <= best {
			continue
		}
		target.Workspace = clean
		target.Version = stringValue(toolchain.Spec["version"])
		best = len(clean)
	}
	if best < 0 || !semanticVersionExact(target.Version) {
		return JSWorkerTarget{}, fmt.Errorf("JavaScript service requires a covering js_target with role %s", role)
	}
	return target, nil
}
