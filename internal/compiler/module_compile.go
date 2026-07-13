package compiler

import (
	"path/filepath"
	"sort"
	"strings"
)

type moduleInstanceCompilation struct {
	Resources       []Resource
	SourceResources []Resource
	Sources         []*Source
	ModuleResource  Resource
	Diagnostics     []Diagnostic
}

func compileModuleInstanceWithSource(root, callerDirectory, callerModule string, module, sourceModule *Block, callerResources []Resource, lockfile *Lockfile, stack map[string]bool) moduleInstanceCompilation {
	var result moduleInstanceCompilation
	if module == nil || len(module.Labels) != 1 {
		if module != nil {
			result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3001", "module requires one label", module))
		}
		return result
	}
	name := module.Labels[0]
	instancePath := name
	if callerModule != "app" && callerModule != "" {
		instancePath = callerModule + "/" + name
	}
	sourcePath, err := requireLiteralString(module, "source")
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3002", err.Error(), module))
		return result
	}
	location, err := resolveModuleLocation(root, callerDirectory, sourcePath, lockfile)
	if err != nil {
		result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3101", err.Error(), module))
		return result
	}
	identity := location.Directory
	if location.LockEntry != nil {
		identity = location.LockEntry.Source + "#" + location.LockEntry.Integrity
	}
	if stack[identity] {
		result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3009", "module dependency cycle through "+identity, module))
		return result
	}
	if stack == nil {
		stack = map[string]bool{}
	}
	stack[identity] = true
	defer delete(stack, identity)

	packageResources, packageSources, diagnostics := compilePackageLogical(root, location.Directory, instancePath, location.LogicalBase)
	authoredResources := make([]Resource, len(packageResources))
	for index, resource := range packageResources {
		authoredResources[index] = authoredResourceView(resource)
	}
	result.Diagnostics = append(result.Diagnostics, diagnostics...)
	if location.LockEntry != nil {
		digest := packageCompileDescriptorDigest(packageResources, packageSources)
		if location.LockEntry.CompileDescriptorDigest == "" || digest != location.LockEntry.CompileDescriptorDigest {
			result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3103", "locked module compile descriptor identity does not match cached package", module))
		}
	}
	inputValues, inputProvenance, inputDiagnostics := resolveModuleInputValuesWithSourceProvenance(callerResources, packageResources, packageSources, module, sourceModule, callerModule)
	result.Diagnostics = append(result.Diagnostics, inputDiagnostics...)
	resolvedResources, substitutionDiagnostics := substituteResolvedModuleInputsWithProvenance(packageResources, inputValues, inputProvenance)
	result.Diagnostics = append(result.Diagnostics, substitutionDiagnostics...)

	nestedBlocks := packageModuleBlocks(packageSources)
	exportsByModule := map[string]map[string]any{}
	pending := append([]*Block(nil), nestedBlocks...)
	for len(pending) > 0 {
		progress := false
		remaining := make([]*Block, 0, len(pending))
		for _, nested := range pending {
			prepared, unresolved := prepareNestedModuleBlock(nested, inputValues, exportsByModule)
			if len(unresolved) > 0 {
				remaining = append(remaining, nested)
				continue
			}
			available := append(append([]Resource(nil), callerResources...), resolvedResources...)
			available = append(available, result.Resources...)
			child := compileModuleInstanceWithSource(root, location.Directory, instancePath, prepared, nested, available, lockfile, stack)
			result.Diagnostics = append(result.Diagnostics, child.Diagnostics...)
			result.Resources = append(result.Resources, child.Resources...)
			result.SourceResources = append(result.SourceResources, child.SourceResources...)
			result.Sources = append(result.Sources, child.Sources...)
			if child.ModuleResource.Address != "" {
				exports, _ := child.ModuleResource.Spec["exports"].(map[string]any)
				exportsByModule[nested.Labels[0]] = cloneMapValue(exports)
			}
			progress = true
		}
		if !progress {
			for _, nested := range remaining {
				result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3009", "nested module dependency cycle or unavailable export", nested))
			}
			break
		}
		pending = remaining
	}

	for index := range resolvedResources {
		before := cloneMapValue(resolvedResources[index].Spec)
		resolved := cloneMapValue(before)
		value, unresolved := substituteModuleExports(resolved, exportsByModule)
		resolvedResources[index].Spec, _ = value.(map[string]any)
		markResolvedReferenceProvenance(&resolvedResources[index], before, resolvedResources[index].Spec, "/spec", instancePath, inputProvenance)
		for _, reference := range unresolved {
			result.Diagnostics = append(result.Diagnostics, Diagnostic{Code: "SCN3010", Severity: "error", Message: "unknown nested module export " + reference, Address: resolvedResources[index].Address})
		}
	}

	moduleResource, moduleDiagnostic := resourceFromBlock(callerModule, module, sourceIDForRange(module.Range))
	var sourceModuleResource Resource
	if moduleDiagnostic != nil {
		result.Diagnostics = append(result.Diagnostics, *moduleDiagnostic)
	} else {
		moduleResource.Origin.ModuleChain = moduleInstantiationChain(instancePath)
		authoredModuleResource, authoredDiagnostic := resourceFromBlock(callerModule, sourceModule, sourceIDForRange(sourceModule.Range))
		if authoredDiagnostic != nil {
			result.Diagnostics = append(result.Diagnostics, *authoredDiagnostic)
		} else {
			authoredModuleResource.Origin.ModuleChain = moduleInstantiationChain(instancePath)
			sourceModuleResource = authoredResourceView(authoredModuleResource)
		}
		packageMetadata, interfaceInputs, exports, exportMetadata := packageInterfaceMetadata(packageSources)
		interfaceInputsValue, _ := substituteModuleInputs(interfaceInputs, inputValues)
		exportsValue, unresolvedInputs := substituteModuleInputs(exports, inputValues)
		exportsValue, unresolvedModules := substituteModuleExports(exportsValue, exportsByModule)
		exportMetadataValue, _ := substituteModuleInputs(exportMetadata, inputValues)
		exportMetadataValue, metadataModules := substituteModuleExports(exportMetadataValue, exportsByModule)
		for _, reference := range canonicalStrings(append(append(unresolvedInputs, unresolvedModules...), metadataModules...)) {
			result.Diagnostics = append(result.Diagnostics, diagnosticForBlock("SCN3010", "module export is unresolved: "+reference, module))
		}
		normalizedExports := map[string]any{}
		if values, ok := exportsValue.(map[string]any); ok {
			for exportName, value := range values {
				normalizedExports[exportName] = normalizeModuleExportValue(value, instancePath)
			}
		}
		if metadata, ok := exportMetadataValue.(map[string]any); ok {
			for exportName, raw := range metadata {
				entry, _ := raw.(map[string]any)
				if normalized, exists := normalizedExports[exportName]; exists {
					entry = cloneMapValue(entry)
					entry["value"] = normalized
					metadata[exportName] = entry
				}
			}
		}
		moduleResource.Spec["package"] = packageMetadata
		moduleResource.Spec["interface_inputs"], _ = interfaceInputsValue.(map[string]any)
		moduleResource.Spec["exports"] = normalizedExports
		moduleResource.Spec["export_metadata"], _ = exportMetadataValue.(map[string]any)
		attachPackageInterfaceProvenance(&moduleResource, packageSources, instancePath)
		markResolvedReferenceProvenance(&moduleResource, interfaceInputs, moduleResource.Spec["interface_inputs"], "/spec/interface_inputs", instancePath, inputProvenance)
		if expression, ok := sourceModule.Attributes["inputs"]; ok {
			markResolvedReferenceProvenance(&moduleResource, expressionValue(expression), moduleResource.Spec["inputs"], "/spec/inputs", callerModule, nil)
		}
		markResolvedReferenceProvenance(&moduleResource, exports, normalizedExports, "/spec/exports", instancePath, inputProvenance)
		markResolvedReferenceProvenance(&moduleResource, exportMetadata, moduleResource.Spec["export_metadata"], "/spec/export_metadata", instancePath, inputProvenance)
		if sourceModuleResource.Address != "" {
			sourceModuleResource.Spec["package"] = cloneMapValue(packageMetadata)
			sourceModuleResource.Spec["interface_inputs"] = cloneMapValue(interfaceInputs)
			sourceModuleResource.Spec["exports"] = cloneMapValue(exports)
			sourceModuleResource.Spec["export_metadata"] = cloneMapValue(exportMetadata)
			attachPackageInterfaceProvenance(&sourceModuleResource, packageSources, instancePath)
		}
		if location.LockEntry != nil {
			moduleResource.Spec["locked_integrity"] = location.LockEntry.Integrity
			moduleResource.Spec["compile_descriptor_digest"] = location.LockEntry.CompileDescriptorDigest
			moduleResource.Spec["package_contract_abi_revision"] = location.LockEntry.PackageContractABIRevision
		} else if relative, relativeErr := filepath.Rel(root, location.Directory); relativeErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			moduleResource.Spec["workspace_package_root"] = filepath.ToSlash(relative)
		}
		result.ModuleResource = moduleResource
	}
	result.Resources = append(resolvedResources, result.Resources...)
	result.SourceResources = append(authoredResources, result.SourceResources...)
	if result.ModuleResource.Address != "" {
		result.Resources = append(result.Resources, result.ModuleResource)
		result.SourceResources = append(result.SourceResources, sourceModuleResource)
	}
	result.Sources = append(packageSources, result.Sources...)
	return result
}

func packageModuleBlocks(sources []*Source) []*Block {
	var blocks []*Block
	for _, source := range sources {
		for _, block := range source.Blocks {
			if block.Type == "module" {
				blocks = append(blocks, block)
			}
		}
	}
	sort.Slice(blocks, func(i, j int) bool {
		left, right := "", ""
		if len(blocks[i].Labels) > 0 {
			left = blocks[i].Labels[0]
		}
		if len(blocks[j].Labels) > 0 {
			right = blocks[j].Labels[0]
		}
		return left < right
	})
	return blocks
}

func prepareNestedModuleBlock(block *Block, parentInputs map[string]any, moduleExports map[string]map[string]any) (*Block, []string) {
	prepared := &Block{Type: block.Type, Labels: append([]string(nil), block.Labels...), Attributes: map[string]Expression{}, AttributeRanges: block.AttributeRanges, Blocks: block.Blocks, Range: block.Range}
	var unresolved []string
	for name, expression := range block.Attributes {
		value := expressionValue(expression)
		value, missingInputs := substituteModuleInputs(value, parentInputs)
		value, missingModules := substituteModuleExports(value, moduleExports)
		unresolved = append(unresolved, missingInputs...)
		unresolved = append(unresolved, missingModules...)
		prepared.Attributes[name] = Expression{Kind: "literal", Value: value, Range: expression.Range, ValueRanges: expression.ValueRanges, Static: true}
	}
	return prepared, canonicalStrings(unresolved)
}

func substituteModuleExports(value any, exports map[string]map[string]any) (any, []string) {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 1 {
			if reference := refString(typed); strings.HasPrefix(reference, "module.") {
				parts := strings.Split(reference, ".")
				if len(parts) < 3 || exports[parts[1]] == nil {
					return typed, []string{reference}
				}
				current, ok := exports[parts[1]][parts[2]]
				if !ok {
					return typed, []string{reference}
				}
				for _, part := range parts[3:] {
					object, objectOK := current.(map[string]any)
					if !objectOK {
						return typed, []string{reference}
					}
					current, ok = object[part]
					if !ok {
						return typed, []string{reference}
					}
				}
				return cloneSemanticValue(current), nil
			}
		}
		result := make(map[string]any, len(typed))
		var unresolved []string
		for key, item := range typed {
			resolved, missing := substituteModuleExports(item, exports)
			result[key] = resolved
			unresolved = append(unresolved, missing...)
		}
		return result, canonicalStrings(unresolved)
	case []any:
		result := make([]any, len(typed))
		var unresolved []string
		for index, item := range typed {
			resolved, missing := substituteModuleExports(item, exports)
			result[index] = resolved
			unresolved = append(unresolved, missing...)
		}
		return result, canonicalStrings(unresolved)
	default:
		return value, nil
	}
}

func normalizeModuleExportValue(value any, module string) any {
	switch typed := value.(type) {
	case map[string]any:
		if len(typed) == 1 {
			reference := refString(typed)
			parts := strings.Split(reference, ".")
			if len(parts) >= 2 && !strings.Contains(reference, "/") && parts[0] != "std" && parts[0] != "var" && parts[0] != "module" && !primitiveTypes[reference] {
				address := resourceAddress(module, parts[0], parts[1])
				if len(parts) > 2 {
					address += "/" + strings.Join(parts[2:], "/")
				}
				return map[string]any{"$ref": filepath.ToSlash(address)}
			}
		}
		result := make(map[string]any, len(typed))
		for key, item := range typed {
			result[key] = normalizeModuleExportValue(item, module)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for index, item := range typed {
			result[index] = normalizeModuleExportValue(item, module)
		}
		return result
	default:
		return value
	}
}
