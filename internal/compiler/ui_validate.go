package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

func validateUISemantics(root string, resources []Resource) []Diagnostic {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	routes := map[string]string{}
	rendererRuntimes := map[string]string{}
	var diagnostics []Diagnostic
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.react-component":
			diagnostics = append(diagnostics, validateReactComponent(root, byAddress, resource)...)
		case "scenery.table-page":
			diagnostics = append(diagnostics, validateTablePage(byAddress, resource)...)
		case "scenery.page":
			path := stringValue(resource.Spec["path"])
			canonical := canonicalRoute(path)
			if !validHTTPPath(path) {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2603", "page path is invalid", resource))
			} else if previous := routes[canonical]; previous != "" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2605", Severity: "error", Message: "page route conflicts with " + previous, Address: resource.Address, Related: []Related{{Address: previous}}})
			} else {
				routes[canonical] = resource.Address
			}
			diagnostics = append(diagnostics, validatePageBindings(byAddress, resource)...)
		case "scenery.renderer":
			if builtinTablePageRenderer(resource) {
				continue
			}
			page := byAddress[resolveResourceRef(resource, refString(resource.Spec["page"]), "page")]
			runtimeName := strings.TrimSpace(stringValue(resource.Spec["runtime"]))
			if page.Kind != "scenery.page" || runtimeName != "web" {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2604", "renderer requires a typed page and supported web runtime", resource))
			}
			identity := page.Address + "\x00" + runtimeName
			if previous := rendererRuntimes[identity]; page.Address != "" && runtimeName != "" && previous != "" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2606", Severity: "error", Message: "renderer runtime conflicts with " + previous, Address: resource.Address, Related: []Related{{Address: previous}}})
			} else {
				rendererRuntimes[identity] = resource.Address
			}
			if root != "" {
				if _, err := rendererModulePath(root, byAddress, resource); err != nil {
					diagnostics = append(diagnostics, uiDiagnostic("SCN2604", err.Error(), resource))
				}
			}
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].Address != diagnostics[j].Address {
			return diagnostics[i].Address < diagnostics[j].Address
		}
		return diagnostics[i].Message < diagnostics[j].Message
	})
	return diagnostics
}

func validatePageBindings(resources map[string]Resource, page Resource) []Diagnostic {
	var diagnostics []Diagnostic
	load := resources[resolveResourceRef(page, refString(page.Spec["load"]), "binding")]
	if !isPageInternalBinding(load) || stringValue(load.Spec["delivery"]) == "enqueue" {
		diagnostics = append(diagnostics, uiDiagnostic("SCN2603", "page load must reference a typed internal binding", page))
	} else {
		operation := resources[resolveResourceRef(load, refString(load.Spec["operation"]), "operation")]
		shape := resolveOperationInputShape(resources, operation)
		for _, match := range httpPathParameterPattern.FindAllStringSubmatch(stringValue(page.Spec["path"]), -1) {
			if len(match) != 2 {
				continue
			}
			if _, exists := shape.Fields[match[1]]; shape.Record == nil || !exists {
				diagnostics = append(diagnostics, uiDiagnostic("SCN2603", "page path parameter "+match[1]+" is not present in the load operation input", page))
			}
		}
	}
	seen := map[string]bool{}
	for _, action := range namedChildren(page.Spec, "action") {
		name := stringValue(action["name"])
		binding := resources[resolveResourceRef(page, refString(action["invoke"]), "binding")]
		if name == "" || seen[name] || !isPageInternalBinding(binding) {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2603", "page actions require unique names and typed internal bindings", page))
		}
		seen[name] = true
	}
	return diagnostics
}

func isPageInternalBinding(binding Resource) bool {
	if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "internal" {
		return false
	}
	delivery := stringValue(binding.Spec["delivery"])
	if delivery != "call" && delivery != "wait" && delivery != "enqueue" {
		return false
	}
	internal, _ := binding.Spec["internal"].(map[string]any)
	return stringValue(internal["principal"]) == "inherit"
}

func enrichUIImplementationDigests(root string, resources []Resource) ([]Resource, []Diagnostic) {
	result := append([]Resource(nil), resources...)
	byAddress := resourcesByAddress(&Manifest{Resources: result})
	var diagnostics []Diagnostic
	for index := range result {
		resource := &result[index]
		if resource.Kind != "scenery.renderer" {
			continue
		}
		if builtinTablePageRenderer(*resource) {
			continue
		}
		path, err := rendererModulePath(root, byAddress, *resource)
		if err != nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2604", err.Error(), *resource))
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			diagnostics = append(diagnostics, uiDiagnostic("SCN2604", "renderer module is unavailable", *resource))
			continue
		}
		sum := sha256.Sum256(data)
		resource.Spec = cloneMapValue(resource.Spec)
		resource.Spec["implementation_digest"] = "sha256:" + hex.EncodeToString(sum[:])
	}
	return result, diagnostics
}

func builtinTablePageRenderer(renderer Resource) bool {
	return renderer.Origin.Kind == "expanded" && stringValue(renderer.Spec["module"]) == tablePageRendererModule
}

func rendererModulePath(root string, resources map[string]Resource, renderer Resource) (string, error) {
	declared := stringValue(renderer.Spec["module"])
	if declared == "" || filepath.IsAbs(declared) || strings.HasPrefix(filepath.Clean(declared), "..") {
		return "", fmt.Errorf("renderer module must be a workspace-relative declared file")
	}
	base := root
	if renderer.Module != "app" {
		module := resources[moduleResourceAddress(renderer.Module)]
		source := stringValue(module.Spec["source"])
		if source == "" {
			return "", fmt.Errorf("renderer module owner is unavailable")
		}
		base = filepath.Join(root, filepath.FromSlash(source))
	}
	path := filepath.Clean(filepath.Join(base, filepath.FromSlash(declared)))
	if !pathWithin(root, path) {
		return "", fmt.Errorf("renderer module escapes the workspace")
	}
	resolved, ok := resolveDeclaredModulePath(path)
	if !ok {
		return "", fmt.Errorf("renderer module is unavailable")
	}
	if err := rejectPathSymlinks(root, resolved); err != nil {
		return "", fmt.Errorf("renderer module is not symlink-safe: %w", err)
	}
	return resolved, nil
}

func resolveDeclaredModulePath(path string) (string, bool) {
	candidates := []string{path}
	if filepath.Ext(path) == "" {
		for _, extension := range []string{".tsx", ".ts", ".jsx", ".js"} {
			candidates = append(candidates, path+extension)
		}
		for _, extension := range []string{".tsx", ".ts", ".jsx", ".js"} {
			candidates = append(candidates, filepath.Join(path, "index"+extension))
		}
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() {
			return candidate, true
		}
	}
	return "", false
}

func uiDiagnostic(code, message string, resource Resource) Diagnostic {
	return Diagnostic{Code: code, Severity: "error", Message: message, Address: resource.Address}
}
