package compiler

import (
	"sort"
	"strings"
)

func validateGoContractOwnership(application *Block, resources []Resource) []Diagnostic {
	type owner struct {
		key, address, importPath string
		hasServices, hasContract bool
	}
	owners := map[string]owner{}
	for _, module := range resources {
		if module.Kind != "scenery.module" {
			continue
		}
		instance := moduleInstancePath(module)
		metadata, _ := module.Spec["package"].(map[string]any)
		goContract, hasContract := metadata["go_contract"].(map[string]any)
		importPath := strings.TrimSpace(stringValue(goContract["import_path"]))
		sourceRoot := strings.TrimSpace(stringValue(module.Spec["workspace_package_root"]))
		if sourceRoot == "" {
			sourceRoot = strings.TrimSpace(stringValue(module.Spec["source"]))
		}
		key := sourceRoot + "\x00" + strings.TrimSpace(stringValue(metadata["name"]))
		if key == "\x00" {
			key = module.Address
		}
		entry := owners[key]
		entry.key, entry.address = key, module.Address
		entry.hasContract = entry.hasContract || hasContract
		if entry.importPath == "" {
			entry.importPath = importPath
		}
		for _, resource := range resources {
			if resource.Module == instance && resource.Kind == "scenery.service" && stringValue(resource.Spec["runtime"]) == "go" {
				entry.hasServices = true
				entry.address = resource.Address
			}
		}
		owners[key] = entry
	}
	rootHasContract := false
	if application != nil {
		for _, child := range application.Blocks {
			rootHasContract = rootHasContract || child.Type == "go_contract"
		}
	}
	rootHasServices := false
	for _, resource := range resources {
		rootHasServices = rootHasServices || resource.Module == "app" && resource.Kind == "scenery.service" && stringValue(resource.Spec["runtime"]) == "go"
	}
	if rootHasContract || rootHasServices {
		owners["application"] = owner{key: "application", address: "app", hasContract: rootHasContract, hasServices: rootHasServices}
	}
	keys := make([]string, 0, len(owners))
	for key := range owners {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	imports := map[string]owner{}
	var diagnostics []Diagnostic
	for _, key := range keys {
		entry := owners[key]
		switch {
		case entry.hasServices && !entry.hasContract:
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6120", Severity: "error", Message: "source unit with Go services requires exactly one go_contract", Address: entry.address})
		case !entry.hasServices && entry.hasContract:
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6120", Severity: "error", Message: "source unit without Go services must not declare go_contract", Address: entry.address})
		}
		if entry.importPath == "" {
			continue
		}
		if previous, exists := imports[entry.importPath]; exists && previous.key != entry.key {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6121", Severity: "error", Message: "go_contract import path is owned by multiple source units", Address: entry.address, Related: []Related{{Address: previous.address}}})
		} else {
			imports[entry.importPath] = entry
		}
	}
	return diagnostics
}
