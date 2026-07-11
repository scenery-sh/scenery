package vnext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type GenerateResult struct {
	Changed []string `json:"changed"`
	Checked []string `json:"checked"`
}

type generatedFile struct {
	Path   string
	Bytes  []byte
	Remove bool
}

func GenerateGoContracts(root string, check bool) (GenerateResult, error) {
	return generateGoContracts(root, check, false)
}

// GenerateAll renders every generated family before committing any artifact,
// so one invalid target cannot leave the workspace partially updated.
func GenerateAll(root string, check bool) (GenerateResult, error) {
	result, err := compile(root, false)
	if err != nil {
		return GenerateResult{}, err
	}
	if result.ContractStatus != "valid" || result.Manifest == nil {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid vNext contract: %s", firstError(result.Diagnostics))
	}
	goFiles, err := renderGoContractFiles(result)
	if err != nil {
		return GenerateResult{}, err
	}
	typeScriptFiles, err := renderTypeScriptClientFiles(result, "")
	if err != nil {
		return GenerateResult{}, err
	}
	return finishGeneratedFiles(result.Root, append(goFiles, typeScriptFiles...), check, "generated vNext artifacts are stale")
}

func generateGoContracts(root string, check, allowActiveChangeTransaction bool) (GenerateResult, error) {
	result, err := compile(root, allowActiveChangeTransaction)
	if err != nil {
		return GenerateResult{}, err
	}
	return generateGoContractsFromResult(result, check)
}

func generateGoContractsFromResult(result *Result, check bool) (GenerateResult, error) {
	if result.ContractStatus != "valid" || result.Manifest == nil {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid vNext contract: %s", firstError(result.Diagnostics))
	}
	files, err := renderGoContractFiles(result)
	if err != nil {
		return GenerateResult{}, err
	}
	return finishGeneratedFiles(result.Root, files, check, "generated vNext contracts are stale")
}

func renderGoContractFiles(result *Result) ([]generatedFile, error) {
	if result != nil && result.hasVerifiedGoFiles {
		return append([]generatedFile(nil), result.verifiedGoFiles...), nil
	}
	if err := validateInvariantPackageABIs(result); err != nil {
		return nil, err
	}
	modules := localModules(result.Manifest.Resources)
	var files []generatedFile
	for _, module := range modules {
		moduleFiles, err := generateModuleContract(result, module)
		if err != nil {
			return nil, err
		}
		files = append(files, moduleFiles...)
	}
	bootstrapOverlay, err := goGenerationBootstrapOverlay(result, files)
	if err != nil {
		return nil, err
	}
	bridgeFiles, err := generateLegacyBridgeArtifacts(result, nativeApplicationServices(result), bootstrapOverlay)
	if err != nil {
		return nil, err
	}
	files = append(files, bridgeFiles...)
	applicationFiles, err := generateApplicationArtifacts(result)
	if err != nil {
		return nil, err
	}
	files = append(files, applicationFiles...)
	files, err = includeStaleGeneratedFiles(result.Root, files, goGeneratedDescriptorNames(), protectedGoGeneratedDescriptors(result))
	if err != nil {
		return nil, err
	}
	return files, nil
}

func finishGeneratedFiles(root string, files []generatedFile, check bool, staleMessage string) (GenerateResult, error) {
	generated, err := inspectGeneratedFiles(root, files)
	if err != nil {
		return generated, err
	}
	if !check && len(generated.Changed) > 0 {
		if err := atomicWriteSet(root, files); err != nil {
			return generated, err
		}
	}
	if check && len(generated.Changed) > 0 {
		return generated, fmt.Errorf("%s: %s", staleMessage, strings.Join(generated.Changed, ", "))
	}
	return generated, nil
}

func validateInvariantPackageABIs(result *Result) error {
	if result == nil || result.Manifest == nil {
		return nil
	}
	seen := map[string]string{}
	for _, module := range localModuleInstances(result.Manifest.Resources) {
		instance := moduleInstancePath(module)
		contractImport, ok := moduleContractImportPath(result.Manifest.Resources, instance)
		if !ok {
			continue
		}
		implementationImport := strings.TrimSuffix(contractImport, "/scenerycontract")
		revision, err := packageABIRevision(implementationImport, moduleResources(result.Manifest.Resources, instance), result.Manifest.Resources)
		if err != nil {
			return err
		}
		if current := seen[contractImport]; current != "" && current != revision {
			return fmt.Errorf("module inputs change exported Go ABI for %s; every instance of one package must share package_contract_abi_revision", contractImport)
		}
		seen[contractImport] = revision
	}
	return nil
}

func inspectGeneratedFiles(root string, files []generatedFile) (GenerateResult, error) {
	result := GenerateResult{Changed: []string{}, Checked: []string{}}
	seen := map[string]bool{}
	for _, file := range files {
		path := filepath.Clean(file.Path)
		if seen[path] {
			return result, fmt.Errorf("generated artifact path collision: %s", path)
		}
		seen[path] = true
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return result, fmt.Errorf("generated artifact escapes app root: %s", path)
		}
		rel = filepath.ToSlash(rel)
		result.Checked = append(result.Checked, rel)
		if err := rejectGeneratedPathSymlinks(root, path); err != nil {
			return result, err
		}
		if file.Remove {
			if _, statErr := os.Lstat(path); statErr == nil {
				result.Changed = append(result.Changed, rel)
			} else if !os.IsNotExist(statErr) {
				return result, statErr
			}
			continue
		}
		current, readErr := os.ReadFile(path)
		if readErr == nil && bytes.Equal(current, file.Bytes) {
			continue
		}
		if readErr != nil && !os.IsNotExist(readErr) {
			return result, readErr
		}
		result.Changed = append(result.Changed, rel)
	}
	sort.Strings(result.Changed)
	sort.Strings(result.Checked)
	return result, nil
}

func includeStaleGeneratedFiles(root string, files []generatedFile, descriptorNames, protectedDescriptors map[string]bool) ([]generatedFile, error) {
	expected := make(map[string]bool, len(files))
	expectedDescriptors := map[string]bool{}
	for _, file := range files {
		path := filepath.Clean(file.Path)
		expected[path] = true
		if descriptorNames[filepath.Base(path)] {
			expectedDescriptors[path] = true
		}
	}
	stale := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != root && (entry.Name() == ".git" || entry.Name() == ".scenery" || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if !descriptorNames[entry.Name()] || protectedDescriptors[filepath.Clean(path)] {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("generated descriptor is a symlink: %s", path)
		}
		base := filepath.Dir(path)
		ownedFiles, verified, err := verifiedGeneratedDescriptorFiles(path)
		if err != nil {
			return err
		}
		if !verified {
			if expectedDescriptors[filepath.Clean(path)] {
				return nil
			}
			return fmt.Errorf("cannot retire unverified generated descriptor %s", path)
		}
		for _, relative := range ownedFiles {
			owned := filepath.Clean(filepath.Join(base, filepath.FromSlash(relative)))
			if !expected[owned] {
				stale[owned] = true
			}
		}
		if !expectedDescriptors[filepath.Clean(path)] {
			stale[filepath.Clean(path)] = true
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(stale))
	for path := range stale {
		if !expected[path] {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	for _, path := range paths {
		files = append(files, generatedFile{Path: path, Remove: true})
	}
	return files, nil
}

func verifiedGeneratedDescriptorFiles(path string) ([]string, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, err
	}
	var descriptor struct {
		APIVersion    string   `json:"api_version"`
		ContentDigest string   `json:"content_digest"`
		Files         []string `json:"files"`
	}
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return nil, false, fmt.Errorf("read generated descriptor %s: %w", path, err)
	}
	wantVersions := map[string]string{
		"scenery.package-generated.v1.json":                  "scenery.package-generated.v1",
		"scenery.generated.v1.json":                          "scenery.generated.v1",
		"scenery.legacy-bridge-generated.v1.json":            "scenery.legacy-bridge-generated.v1",
		"scenery.typescript-client-generated.v1.json":        "scenery.typescript-client-generated/v1",
		"scenery.legacy-typescript-client-generated.v1.json": "scenery.legacy-typescript-client-generated/v1",
	}
	if descriptor.APIVersion != wantVersions[filepath.Base(path)] || !isCanonicalSHA256Digest(descriptor.ContentDigest) {
		return nil, false, nil
	}
	base := filepath.Dir(path)
	artifacts := make([]generatedFile, 0, len(descriptor.Files))
	seen := map[string]bool{}
	for _, relative := range descriptor.Files {
		clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(relative)))
		if relative == "" || filepath.IsAbs(relative) || clean == ".." || strings.HasPrefix(clean, "../") || clean != filepath.ToSlash(relative) || seen[clean] {
			return nil, false, fmt.Errorf("generated descriptor %s has an unsafe owned path %q", path, relative)
		}
		seen[clean] = true
		owned := filepath.Join(base, filepath.FromSlash(clean))
		if !pathWithin(base, owned) {
			return nil, false, fmt.Errorf("generated descriptor %s file escapes its output root: %s", path, relative)
		}
		info, err := os.Lstat(owned)
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, false, nil
		}
		contents, err := os.ReadFile(owned)
		if err != nil {
			return nil, false, err
		}
		if !trustedGeneratedArtifact(filepath.Base(path), clean, contents) {
			return nil, false, fmt.Errorf("generated descriptor %s claims unmarked artifact %s", path, relative)
		}
		artifacts = append(artifacts, generatedFile{Path: owned, Bytes: contents})
	}
	if artifactDigest(base, artifacts) != descriptor.ContentDigest && legacyArtifactDigest(base, artifacts) != descriptor.ContentDigest {
		return nil, false, nil
	}
	return append([]string(nil), descriptor.Files...), true, nil
}

func trustedGeneratedArtifact(descriptorName, relative string, contents []byte) bool {
	if bytes.HasPrefix(contents, []byte("// Code generated by Scenery vNext. DO NOT EDIT.")) {
		return true
	}
	if descriptorName == "scenery.legacy-typescript-client-generated.v1.json" && bytes.HasPrefix(contents, []byte("// Code generated by scenery. DO NOT EDIT.")) {
		return true
	}
	if filepath.Base(relative) != "scenery.client-selection.v1.json" {
		return false
	}
	var identity struct {
		APIVersion string `json:"api_version"`
	}
	return json.Unmarshal(contents, &identity) == nil && identity.APIVersion == "scenery.client-selection.v1"
}

func protectedGoGeneratedDescriptors(result *Result) map[string]bool {
	protected := map[string]bool{}
	if result == nil || result.Manifest == nil {
		return protected
	}
	for _, module := range result.Manifest.Resources {
		if module.Kind != "scenery.module/v1" || module.Migration == nil || module.Migration.Active != "legacy" {
			continue
		}
		source := stringValue(module.Spec["workspace_package_root"])
		if source == "" {
			source = stringValue(module.Spec["source"])
		}
		if source == "" {
			continue
		}
		moduleRoot := filepath.Join(result.Root, filepath.FromSlash(source))
		protected[filepath.Clean(filepath.Join(moduleRoot, "scenerycontract", "scenery.package-generated.v1.json"))] = true
		protected[filepath.Clean(filepath.Join(moduleRoot, "scenery.legacy-bridge-generated.v1.json"))] = true
	}
	return protected
}

func localModules(resources []Resource) []Resource {
	byPackage := map[string]Resource{}
	for _, r := range resources {
		if r.Kind == "scenery.module/v1" {
			if r.Migration != nil && r.Migration.Active == "legacy" {
				continue
			}
			root, _ := r.Spec["workspace_package_root"].(string)
			if root == "" {
				source, _ := r.Spec["source"].(string)
				if strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
					root = source
				}
			}
			if root == "" {
				continue
			}
			metadata, _ := r.Spec["package"].(map[string]any)
			goContract, _ := metadata["go_contract"].(map[string]any)
			if stringValue(goContract["import_path"]) == "" {
				continue
			}
			key := filepath.ToSlash(filepath.Clean(root))
			if importPath, ok := moduleContractImportPath(resources, moduleInstancePath(r)); ok {
				key = importPath
			}
			if current, exists := byPackage[key]; !exists || moduleInstancePath(r) < moduleInstancePath(current) {
				byPackage[key] = r
			}
		}
	}
	modules := make([]Resource, 0, len(byPackage))
	for _, module := range byPackage {
		modules = append(modules, module)
	}
	sort.Slice(modules, func(i, j int) bool { return moduleInstancePath(modules[i]) < moduleInstancePath(modules[j]) })
	return modules
}

func localModuleInstances(resources []Resource) []Resource {
	var modules []Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.module/v1" {
			continue
		}
		if resource.Migration != nil && resource.Migration.Active == "legacy" {
			continue
		}
		root, _ := resource.Spec["workspace_package_root"].(string)
		source, _ := resource.Spec["source"].(string)
		if root != "" || strings.HasPrefix(source, "./") || strings.HasPrefix(source, "../") {
			modules = append(modules, resource)
		}
	}
	sort.Slice(modules, func(i, j int) bool { return moduleInstancePath(modules[i]) < moduleInstancePath(modules[j]) })
	return modules
}

func generateModuleContract(result *Result, module Resource) ([]generatedFile, error) {
	source, _ := module.Spec["workspace_package_root"].(string)
	if source == "" {
		source, _ = module.Spec["source"].(string)
	}
	dir := filepath.Join(result.Root, filepath.FromSlash(source))
	contractDir := filepath.Join(dir, "scenerycontract")
	resources := moduleResources(result.Manifest.Resources, moduleInstancePath(module))
	packageBlock := findPackageBlock(result.Sources, source)
	importPath := ""
	if packageBlock != nil {
		for _, child := range packageBlock.Blocks {
			if child.Type == "go_contract" {
				importPath, _ = literalString(child, "import_path")
			}
		}
	}
	if importPath == "" {
		return nil, fmt.Errorf("module %s package has no go_contract.import_path", moduleInstancePath(module))
	}
	abiRevision, err := packageABIRevision(importPath, resources, result.Manifest.Resources)
	if err != nil {
		return nil, err
	}
	contractResources, embeddedTypes, err := goContractTypeClosure(moduleInstancePath(module), resources, result.Manifest.Resources)
	if err != nil {
		return nil, err
	}
	typeResolver := newGoContractTypeResolver(moduleInstancePath(module), result.Manifest.Resources, embeddedTypes)
	typesSource := renderContractTypesResolved(contractResources, typeResolver)
	if err := typeResolver.Err(); err != nil {
		return nil, err
	}
	packageIdentity, packageVersion := module.Name, ""
	if packageBlock != nil {
		if len(packageBlock.Labels) > 0 && strings.TrimSpace(packageBlock.Labels[0]) != "" {
			packageIdentity = packageBlock.Labels[0]
		}
		packageVersion, _ = literalString(packageBlock, "version")
	}
	apiResolver := newGoContractTypeResolver(moduleInstancePath(module), result.Manifest.Resources, embeddedTypes)
	contractSource, err := renderContractAPI(packageIdentity, packageVersion, importPath, abiRevision, resources, result.Manifest.Resources, apiResolver)
	if err != nil {
		return nil, err
	}
	if err := apiResolver.Err(); err != nil {
		return nil, err
	}
	formattedTypes, err := format.Source([]byte(typesSource))
	if err != nil {
		return nil, fmt.Errorf("format %s types: %w\n%s", moduleInstancePath(module), err, typesSource)
	}
	formattedContract, err := format.Source([]byte(contractSource))
	if err != nil {
		return nil, fmt.Errorf("format %s contract: %w\n%s", moduleInstancePath(module), err, contractSource)
	}
	artifactFiles := []generatedFile{{Path: filepath.Join(contractDir, "types.gen.go"), Bytes: formattedTypes}, {Path: filepath.Join(contractDir, "contract.gen.go"), Bytes: formattedContract}}
	descriptor := map[string]any{
		"api_version": "scenery.package-generated.v1", "artifact_kind": "go_package_contract",
		"package": packageIdentity, "package_identity": packageIdentity, "package_version": packageVersion,
		"import_path": importPath + "/scenerycontract", "package_contract_abi_revision": abiRevision,
		"go_implementation_abi_range": ">=1.0.0, <2.0.0", "runtime_abi_range": "scenery.go-runtime/v1",
		"capability_interface_abi_ranges": packageCapabilityABIRanges(resources, result.Manifest.Resources),
		"content_digest":                  artifactDigest(contractDir, artifactFiles), "generator": "scenery.vnext.go", "generator_version": "1",
		"covered": packageDeclarationKeys(resources), "files": generatedFilePaths(contractDir, artifactFiles),
	}
	descriptorBytes, _ := json.MarshalIndent(descriptor, "", "  ")
	descriptorBytes = append(descriptorBytes, '\n')
	return append(artifactFiles, generatedFile{Path: filepath.Join(contractDir, "scenery.package-generated.v1.json"), Bytes: descriptorBytes}), nil
}

func moduleResources(resources []Resource, module string) []Resource {
	var out []Resource
	for _, r := range resources {
		if r.Module == module {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}
func resourceAddresses(resources []Resource) []string {
	out := make([]string, len(resources))
	for i, r := range resources {
		out[i] = r.Address
	}
	return out
}

func packageDeclarationKeys(resources []Resource) []string {
	keys := make([]string, 0, len(resources))
	for _, resource := range resources {
		kind := strings.TrimSuffix(strings.TrimPrefix(resource.Kind, "scenery."), "/v1")
		keys = append(keys, kind+"."+resource.Name)
	}
	return canonicalStrings(keys)
}

func packageCapabilityABIRanges(resources, allResources []Resource) map[string]string {
	ranges := map[string]string{}
	for _, service := range resources {
		if service.Kind != "scenery.service/v1" {
			continue
		}
		dependencies, err := serviceGoDependencies(allResources, service)
		if err != nil {
			continue
		}
		for _, dependency := range dependencies {
			ranges[dependency.ImportPath] = dependency.CapabilityABI
		}
	}
	return ranges
}

func findPackageBlock(sources []*Source, moduleSource string) *Block {
	prefix := strings.TrimPrefix(filepath.ToSlash(moduleSource), "./") + "/"
	for _, source := range sources {
		if !strings.HasPrefix(source.Relative, prefix) {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type == "package" {
				return block
			}
		}
	}
	return nil
}

func packageABIRevision(importPath string, resources, allResources []Resource) (string, error) {
	abiResources, err := packageABIResources(resources, allResources)
	if err != nil {
		return "", err
	}
	projection := map[string]any{"import_path": importPath + "/scenerycontract", "profile": "scenery.go-implementation/v1", "abi_major": 1, "resources": abiResources}
	b, err := MarshalCanonical(projection)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("scenery.package-contract-abi-revision.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func packageABIResources(resources, allResources []Resource) ([]map[string]any, error) {
	byAddress := resourcesByAddress(&Manifest{Resources: allResources})
	neededTypes := map[string]bool{}
	var projected []map[string]any
	module := ""
	if len(resources) > 0 {
		module = resources[0].Module
	}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.service/v1":
			dependencies, err := serviceGoDependencies(allResources, resource)
			if err != nil {
				return nil, err
			}
			dependencyProjection := make([]map[string]any, 0, len(dependencies))
			for _, dependency := range dependencies {
				dependencyProjection = append(dependencyProjection, map[string]any{"name": dependency.Name, "go_type": dependency.GoType, "capability_abi": dependency.CapabilityABI})
			}
			clients, err := serviceGoClients(allResources, resource)
			if err != nil {
				return nil, err
			}
			clientProjection := make([]map[string]any, 0, len(clients))
			for _, client := range clients {
				clientProjection = append(clientProjection, map[string]any{"name": client.Name, "target_contract_import_path": client.ContractImport, "operation": client.Operation.Name, "input": normalizePackageABIValue(client.Operation.Spec["input"], client.Operation.Module, allResources), "result": normalizePackageABIValue(client.Operation.Spec["result"], client.Operation.Module, allResources), "error": normalizePackageABIValue(client.Operation.Spec["error"], client.Operation.Module, allResources), "delivery": client.Delivery})
			}
			projected = append(projected, map[string]any{
				"kind": "service", "name": resource.Name, "implementation": resource.Spec["implementation"],
				"lifecycle": resource.Spec["lifecycle"], "dependencies": dependencyProjection,
				"config_shape": normalizePackageABIValue(serviceConfigShape(resource), resource.Module, allResources), "clients": clientProjection,
			})
			for _, field := range namedChildren(resource.Spec, "config_schema") {
				collectABITypeReferences(field["type"], resource.Module, byAddress, neededTypes)
			}
			for _, client := range clients {
				collectABITypeReferences(client.Operation.Spec["input"], client.Operation.Module, byAddress, neededTypes)
				for _, childKind := range []string{"result", "error"} {
					for _, child := range namedChildren(client.Operation.Spec, childKind) {
						collectABITypeReferences(child["type"], client.Operation.Module, byAddress, neededTypes)
					}
				}
			}
		case "scenery.operation/v1":
			collectABITypeReferences(resource.Spec["input"], resource.Module, byAddress, neededTypes)
			for _, childKind := range []string{"result", "error"} {
				for _, child := range namedChildren(resource.Spec, childKind) {
					collectABITypeReferences(child["type"], resource.Module, byAddress, neededTypes)
				}
			}
			projected = append(projected, map[string]any{
				"kind": "operation", "name": resource.Name, "input": normalizePackageABIValue(resource.Spec["input"], resource.Module, allResources), "handler": resource.Spec["handler"],
				"result": normalizePackageABIValue(resource.Spec["result"], resource.Module, allResources), "error": normalizePackageABIValue(resource.Spec["error"], resource.Module, allResources),
			})
		}
	}
	for _, resource := range allResources {
		if resource.Kind != "scenery.module/v1" || moduleInstancePath(resource) != module {
			continue
		}
		if exports, ok := resource.Spec["exports"].(map[string]any); ok {
			for _, value := range exports {
				collectABITypeReferences(value, module, byAddress, neededTypes)
			}
		}
	}
	for changed := true; changed; {
		changed = false
		for address := range neededTypes {
			resource, ok := byAddress[address]
			if !ok {
				continue
			}
			for _, childKind := range []string{"field", "variant"} {
				for _, child := range namedChildren(resource.Spec, childKind) {
					before := len(neededTypes)
					collectABITypeReferences(child["type"], resource.Module, byAddress, neededTypes)
					changed = changed || len(neededTypes) != before
				}
			}
		}
	}
	for address := range neededTypes {
		resource, ok := byAddress[address]
		if !ok {
			continue
		}
		projection := map[string]any{"kind": strings.TrimSuffix(strings.TrimPrefix(resource.Kind, "scenery."), "/v1"), "name": resource.Name, "spec": normalizePackageABIValue(resource.Spec, resource.Module, allResources)}
		if resource.Module != module {
			projection["package_type_identity"] = packageABITypeIdentity(resource, allResources)
		}
		projected = append(projected, projection)
	}
	sort.Slice(projected, func(i, j int) bool {
		left := fmt.Sprint(projected[i]["kind"]) + "/" + fmt.Sprint(projected[i]["name"]) + "/" + fmt.Sprint(projected[i]["package_type_identity"])
		right := fmt.Sprint(projected[j]["kind"]) + "/" + fmt.Sprint(projected[j]["name"]) + "/" + fmt.Sprint(projected[j]["package_type_identity"])
		return left < right
	})
	return projected, nil
}

func collectABITypeReferences(value any, module string, resources map[string]Resource, result map[string]bool) {
	for _, reference := range typeReferences(value) {
		address := reference
		if !strings.Contains(reference, "/") {
			parts := strings.Split(reference, ".")
			if len(parts) != 2 || parts[0] != "record" && parts[0] != "enum" && parts[0] != "union" {
				continue
			}
			address = resourceAddress(module, parts[0], parts[1])
		}
		if resource, ok := resources[address]; ok && isNamedContractType(resource) {
			result[address] = true
		}
	}
}

func typeReferences(value any) []string {
	if reference := refString(value); reference != "" {
		return []string{reference}
	}
	expression, _ := value.(map[string]any)
	raw, _ := expression["$expression"].(string)
	if raw == "" {
		return nil
	}
	return typeExpressionNames(raw)
}

func serviceConfigShape(service Resource) any {
	if schema := namedChildren(service.Spec, "config_schema"); len(schema) > 0 {
		return schema
	}
	config, _ := service.Spec["config"].(map[string]any)
	if len(config) == 0 {
		return nil
	}
	keys := make([]string, 0, len(config))
	for key := range config {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func renderContractTypes(resources []Resource) string {
	return renderContractTypesResolved(resources, nil)
}

func renderContractTypesResolved(resources []Resource, resolver *goContractTypeResolver) string {
	var body strings.Builder
	typeName := goType
	typeExpression := goTypeExpression
	if resolver != nil {
		typeName = resolver.Type
		typeExpression = resolver.Expression
	}
	renderTupleTypes(&body, resources, typeExpression)
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record/v1":
			renderRecord(&body, resource, typeName)
		case "scenery.enum/v1":
			renderEnum(&body, resource)
		case "scenery.union/v1":
			renderUnion(&body, resource, typeName)
		}
	}
	renderNamedUnionDecoder(&body, resources, resolver)

	var b strings.Builder
	b.WriteString("// Code generated by Scenery vNext. DO NOT EDIT.\npackage scenerycontract\n\n")
	if hasContractWireTypes(resources) {
		b.WriteString("import (\n\t\"encoding/json\"\n\t\"fmt\"\n\tscenery \"scenery.sh\"\n")
		if resolver != nil {
			imports := resolver.Imports()
			aliases := make([]string, 0, len(imports))
			for alias := range imports {
				aliases = append(aliases, alias)
			}
			sort.Strings(aliases)
			for _, alias := range aliases {
				fmt.Fprintf(&b, "\t%s %q\n", alias, imports[alias])
			}
		}
		b.WriteString(")\n\n")
	} else {
		b.WriteString("import scenery \"scenery.sh\"\n\n")
	}
	b.WriteString("var _ = scenery.Problem{}\n\n")
	b.WriteString(body.String())
	return b.String()
}

func renderTupleTypes(b *strings.Builder, resources []Resource, typeExpression func(string) string) {
	tuples := packageTupleTypes(resources)
	names := make([]string, 0, len(tuples))
	for name := range tuples {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		arguments := splitTypeArguments(tuples[name][len("tuple(") : len(tuples[name])-1])
		fmt.Fprintf(b, "type %s struct {\n", name)
		for index, argument := range arguments {
			fmt.Fprintf(b, "\tItem%d %s\n", index, typeExpression(argument))
		}
		b.WriteString("}\n\n")
	}
}

func packageTupleTypes(resources []Resource) map[string]string {
	result := map[string]string{}
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record/v1":
			for _, field := range namedChildren(resource.Spec, "field") {
				collectTupleTypes(field["type"], result)
			}
		case "scenery.union/v1":
			for _, variant := range namedChildren(resource.Spec, "variant") {
				collectTupleTypes(variant["type"], result)
			}
		case "scenery.operation/v1":
			collectTupleTypes(resource.Spec["input"], result)
			for _, kind := range []string{"result", "error"} {
				for _, variant := range namedChildren(resource.Spec, kind) {
					collectTupleTypes(variant["type"], result)
				}
			}
		case "scenery.service/v1":
			for _, field := range namedChildren(resource.Spec, "config_schema") {
				collectTupleTypes(field["type"], result)
			}
		}
	}
	return result
}

func collectTupleTypes(value any, result map[string]string) {
	raw := goWireTypeExpression(value)
	collectTupleTypeExpression(raw, result)
}

func collectTupleTypeExpression(raw string, result map[string]string) {
	raw = canonicalContractTypeExpression(raw)
	open := strings.IndexByte(raw, '(')
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return
	}
	name := strings.TrimSpace(raw[:open])
	arguments := splitTypeArguments(raw[open+1 : len(raw)-1])
	if name == "tuple" && len(arguments) > 0 {
		result[tupleGoTypeName(raw)] = raw
	}
	for _, argument := range arguments {
		collectTupleTypeExpression(argument, result)
	}
}

func canonicalContractTypeExpression(raw string) string {
	raw = strings.TrimSpace(raw)
	open := strings.IndexByte(raw, '(')
	if open < 0 || !strings.HasSuffix(raw, ")") {
		return raw
	}
	name := strings.TrimSpace(raw[:open])
	arguments := splitTypeArguments(raw[open+1 : len(raw)-1])
	for index := range arguments {
		arguments[index] = canonicalContractTypeExpression(arguments[index])
	}
	return name + "(" + strings.Join(arguments, ",") + ")"
}

func tupleGoTypeName(raw string) string {
	canonical := canonicalContractTypeExpression(raw)
	sum := sha256.Sum256([]byte(canonical))
	return "Tuple" + strings.ToUpper(hex.EncodeToString(sum[:8]))
}

func renderRecord(b *strings.Builder, resource Resource, typeName func(any) string) {
	name := goName(resource.Name)
	fmt.Fprintf(b, "type %s struct {\n", name)
	for _, field := range namedChildren(resource.Spec, "field") {
		name, _ := field["name"].(string)
		typeValue := field["type"]
		fmt.Fprintf(b, "\t%s %s `json:\"%s%s\"`\n", goName(name), typeName(typeValue), wireName(field, name), optionalJSONSuffix(typeValue))
	}
	if resource.Spec["unknown_fields"] == "preserve" {
		b.WriteString("\tUnknownFields map[string]scenery.JSON `json:\"-\"`\n")
	}
	b.WriteString("}\n\n")
	renderRecordMethods(b, resource, name)
}

func hasContractWireTypes(resources []Resource) bool {
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1":
			return true
		}
	}
	return false
}

func renderRecordMethods(b *strings.Builder, resource Resource, name string) {
	fmt.Fprintf(b, "func (value %s) MarshalJSON() ([]byte, error) {\n", name)
	fmt.Fprintf(b, "\tif err := value.Validate(); err != nil { return nil, err }\n")
	b.WriteString("\tobject := map[string]json.RawMessage{}\n")
	for _, field := range namedChildren(resource.Spec, "field") {
		fieldName, _ := field["name"].(string)
		goField := goName(fieldName)
		wire := wireName(field, fieldName)
		typeExpression := goWireTypeExpression(field["type"])
		if _, ok := optionalInner(field["type"]); ok {
			fmt.Fprintf(b, "\tif value.%s.Set { raw, err := scenery.MarshalContractValue(value.%s, %q); if err != nil { return nil, fmt.Errorf(\"encode field %s: %%w\", err) }; object[%q] = raw }\n", goField, goField, typeExpression, wire, wire)
		} else {
			fmt.Fprintf(b, "\traw%s, err := scenery.MarshalContractValue(value.%s, %q); if err != nil { return nil, fmt.Errorf(\"encode field %s: %%w\", err) }; object[%q] = raw%s\n", goField, goField, typeExpression, wire, wire, goField)
		}
	}
	if resource.Spec["unknown_fields"] == "preserve" {
		b.WriteString("\tfor key, raw := range value.UnknownFields {\n\t\tif _, exists := object[key]; exists { return nil, fmt.Errorf(\"unknown field %q collides with a declared wire name\", key) }\n\t\tcanonical, err := scenery.MarshalContractValue(raw, \"json\"); if err != nil { return nil, fmt.Errorf(\"encode unknown field %q: %w\", key, err) }\n\t\tobject[key] = append(json.RawMessage(nil), canonical...)\n\t}\n")
	}
	b.WriteString("\tencoded, err := json.Marshal(object); if err != nil { return nil, err }; return scenery.MarshalContractValue(scenery.JSON(encoded), \"json\")\n}\n\n")
	fmt.Fprintf(b, "func (value *%s) UnmarshalJSON(data []byte) error {\n", name)
	b.WriteString("\tobject, err := scenery.DecodeJSONObject(data); if err != nil { return err }\n")
	fmt.Fprintf(b, "\t*value = %s{}\n", name)
	for _, field := range namedChildren(resource.Spec, "field") {
		fieldName, _ := field["name"].(string)
		goField := goName(fieldName)
		wire := wireName(field, fieldName)
		typeExpression := goWireTypeExpression(field["type"])
		if _, optional := optionalInner(field["type"]); optional {
			fmt.Fprintf(b, "\tif raw, exists := object[%q]; exists {\n", wire)
			fmt.Fprintf(b, "\t\tif err := unmarshalGeneratedContractValue(raw, &value.%s, %q); err != nil { return fmt.Errorf(\"decode field %s: %%w\", err) }; delete(object, %q)\n\t}\n", goField, typeExpression, wire, wire)
		} else {
			fmt.Fprintf(b, "\traw%s, exists := object[%q]; if !exists { return fmt.Errorf(\"missing required field %s\") }\n", goField, wire, wire)
			fmt.Fprintf(b, "\tif err := unmarshalGeneratedContractValue(raw%s, &value.%s, %q); err != nil { return fmt.Errorf(\"decode field %s: %%w\", err) }; delete(object, %q)\n", goField, goField, typeExpression, wire, wire)
		}
	}
	if resource.Spec["unknown_fields"] == "preserve" {
		b.WriteString("\tvalue.UnknownFields = make(map[string]scenery.JSON, len(object))\n\tfor key, raw := range object { value.UnknownFields[key] = append(scenery.JSON(nil), raw...) }\n")
	} else {
		b.WriteString("\tfor key := range object { return fmt.Errorf(\"unknown field %q\", key) }\n")
	}
	b.WriteString("\treturn value.Validate()\n}\n\n")
	fmt.Fprintf(b, "func (value %s) Validate() error {\n", name)
	for _, field := range namedChildren(resource.Spec, "field") {
		fieldName := stringValue(field["name"])
		goField := goName(fieldName)
		fmt.Fprintf(b, "\tif err := scenery.ValidateContractValue(value.%s, %q, %s); err != nil { return fmt.Errorf(\"validate field %s: %%w\", err) }\n", goField, goWireTypeExpression(field["type"]), renderGoContractConstraints(field), wireName(field, fieldName))
	}
	validations := namedChildren(resource.Spec, "validation")
	if len(validations) > 0 {
		b.WriteString("\tfields := map[string]any{")
		for _, field := range namedChildren(resource.Spec, "field") {
			fmt.Fprintf(b, "%q: value.%s,", stringValue(field["name"]), goName(stringValue(field["name"])))
		}
		b.WriteString("}\n\tfieldTypes := map[string]string{")
		for _, field := range namedChildren(resource.Spec, "field") {
			fmt.Fprintf(b, "%q: %q,", stringValue(field["name"]), goWireTypeExpression(field["type"]))
		}
		b.WriteString("}\n")
		for _, validation := range validations {
			fmt.Fprintf(b, "\tif err := scenery.ValidateContractRecord(fields, fieldTypes, %q, %q, %q, %q); err != nil { return err }\n", validationProgramJSON(expressionText(validation["when"])), stringValue(validation["code"]), stringValue(validation["message"]), refString(validation["path"]))
		}
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func renderEnum(b *strings.Builder, resource Resource) {
	name := goName(resource.Name)
	fmt.Fprintf(b, "type %s string\n\nconst (\n", name)
	for _, value := range namedChildren(resource.Spec, "value") {
		label, _ := value["name"].(string)
		wire := label
		if explicit, ok := value["wire_value"].(string); ok {
			wire = explicit
		}
		fmt.Fprintf(b, "\t%s%s %s = %q\n", name, goName(label), name, wire)
	}
	b.WriteString(")\n\n")
	fmt.Fprintf(b, "func (value %s) IsKnown() bool {\n\tswitch value {\n", name)
	for _, value := range namedChildren(resource.Spec, "value") {
		fmt.Fprintf(b, "\tcase %s%s: return true\n", name, goName(stringValue(value["name"])))
	}
	b.WriteString("\tdefault: return false\n\t}\n}\n\n")
	fmt.Fprintf(b, "func (value %s) MarshalJSON() ([]byte, error) {\n", name)
	if resource.Spec["open"] != true {
		b.WriteString("\tif !value.IsKnown() { return nil, fmt.Errorf(\"unknown closed enum value %q\", value) }\n")
	}
	b.WriteString("\treturn scenery.MarshalContractValue(string(value), \"string\")\n}\n\n")
	fmt.Fprintf(b, "func (value *%s) UnmarshalJSON(data []byte) error {\n\tvar decoded string\n\tif err := scenery.UnmarshalContractValue(data, &decoded, \"string\"); err != nil { return err }\n\t*value = %s(decoded)\n", name, name)
	if resource.Spec["open"] != true {
		b.WriteString("\tif !value.IsKnown() { return fmt.Errorf(\"unknown closed enum value %q\", decoded) }\n")
	}
	b.WriteString("\treturn nil\n}\n\n")
}

func renderUnion(b *strings.Builder, resource Resource, typeName func(any) string) {
	name := goName(resource.Name)
	discriminator := stringValue(resource.Spec["discriminator"])
	fmt.Fprintf(b, "type %s interface { is%s() }\n\n", name, name)
	for _, variant := range namedChildren(resource.Spec, "variant") {
		variantName, _ := variant["name"].(string)
		wrapper := name + goName(variantName)
		tag := wireName(variant, variantName)
		fmt.Fprintf(b, "type %s struct { Value %s }\nfunc (%s) is%s() {}\nfunc (value %s) MarshalJSON() ([]byte, error) { return marshal%sVariant(%q, value.Value, %q) }\n\n", wrapper, typeName(variant["type"]), wrapper, name, wrapper, name, tag, goWireTypeExpression(variant["type"]))
	}
	if resource.Spec["open"] == true {
		wrapper := name + "Unknown"
		fmt.Fprintf(b, "type %s struct { Tag string; Payload scenery.JSON }\nfunc (%s) is%s() {}\nfunc (value %s) MarshalJSON() ([]byte, error) { return marshal%sVariant(value.Tag, value.Payload, \"json\") }\n\n", wrapper, wrapper, name, wrapper, name)
	}
	fmt.Fprintf(b, "func marshal%sVariant(tag string, payload any, payloadType string) ([]byte, error) {\n", name)
	b.WriteString("\tif tag == \"\" { return nil, fmt.Errorf(\"union tag is required\") }\n")
	fmt.Fprintf(b, "\tpayloadBytes, err := scenery.MarshalContractValue(payload, payloadType); if err != nil { return nil, err }; object, err := scenery.DecodeJSONObject(payloadBytes); if err != nil { return nil, fmt.Errorf(\"union payload must be a record: %%w\", err) }; if _, exists := object[%q]; exists { return nil, fmt.Errorf(\"union discriminator collision\") }; tagBytes, err := scenery.MarshalContractValue(tag, \"string\"); if err != nil { return nil, err }; object[%q] = tagBytes; encoded, err := json.Marshal(object); if err != nil { return nil, err }; return scenery.MarshalContractValue(scenery.JSON(encoded), \"json\")\n}\n\n", discriminator, discriminator)
	fmt.Fprintf(b, "func Marshal%sJSON(value %s) ([]byte, error) {\n\tswitch typed := value.(type) {\n", name, name)
	for _, variant := range namedChildren(resource.Spec, "variant") {
		wrapper := name + goName(stringValue(variant["name"]))
		fmt.Fprintf(b, "\tcase %s: return typed.MarshalJSON()\n\tcase *%s: if typed == nil { return nil, fmt.Errorf(\"nil union variant\") }; return typed.MarshalJSON()\n", wrapper, wrapper)
	}
	if resource.Spec["open"] == true {
		wrapper := name + "Unknown"
		fmt.Fprintf(b, "\tcase %s: return typed.MarshalJSON()\n\tcase *%s: if typed == nil { return nil, fmt.Errorf(\"nil union variant\") }; return typed.MarshalJSON()\n", wrapper, wrapper)
	}
	b.WriteString("\tdefault: return nil, fmt.Errorf(\"unknown union value %T\", value)\n\t}\n}\n\n")
	fmt.Fprintf(b, "func Unmarshal%sJSON(data []byte) (%s, error) {\n", name, name)
	fmt.Fprintf(b, "\tobject, err := scenery.DecodeJSONObject(data); if err != nil { return nil, err }; tagBytes, exists := object[%q]; if !exists { return nil, fmt.Errorf(\"missing union discriminator %s\") }; var tag string; if err := scenery.UnmarshalContractValue(tagBytes, &tag, \"string\"); err != nil { return nil, fmt.Errorf(\"decode union discriminator: %%w\", err) }; delete(object, %q); encoded, err := json.Marshal(object); if err != nil { return nil, err }; payload, err := scenery.MarshalContractValue(scenery.JSON(encoded), \"json\"); if err != nil { return nil, err }; switch tag {\n", discriminator, discriminator, discriminator)
	for _, variant := range namedChildren(resource.Spec, "variant") {
		variantName := stringValue(variant["name"])
		wrapper := name + goName(variantName)
		fmt.Fprintf(b, "\tcase %q: var value %s; if err := unmarshalGeneratedContractValue(payload, &value, %q); err != nil { return nil, err }; return %s{Value: value}, nil\n", wireName(variant, variantName), typeName(variant["type"]), goWireTypeExpression(variant["type"]), wrapper)
	}
	if resource.Spec["open"] == true {
		fmt.Fprintf(b, "\tdefault: return %sUnknown{Tag: tag, Payload: append(scenery.JSON(nil), payload...)}, nil\n", name)
	} else {
		b.WriteString("\tdefault: return nil, fmt.Errorf(\"unknown closed union tag %q\", tag)\n")
	}
	b.WriteString("\t}\n}\n\n")
}

func renderNamedUnionDecoder(b *strings.Builder, resources []Resource, resolver *goContractTypeResolver) {
	var unions []Resource
	for _, resource := range resources {
		if resource.Kind == "scenery.union/v1" {
			unions = append(unions, resource)
		}
	}
	var external []Resource
	if resolver != nil {
		external = resolver.referencedResources(resources, "scenery.union/v1")
	}
	if len(unions) == 0 && len(external) == 0 {
		b.WriteString("func unmarshalGeneratedContractValue(data []byte, target any, typeExpression string) error { return scenery.UnmarshalContractValue(data, target, typeExpression) }\n")
		return
	}
	b.WriteString("func unmarshalGeneratedContractValue(data []byte, target any, typeExpression string) error {\n\treturn scenery.UnmarshalContractValueWithNamed(data, target, typeExpression, func(typeName string, data []byte) (any, error) {\n\t\tswitch typeName {\n")
	for _, union := range unions {
		fmt.Fprintf(b, "\t\tcase %q, %q: return Unmarshal%sJSON(data)\n", "union."+union.Name, union.Address, goName(union.Name))
	}
	for _, union := range external {
		if union.Module == resolver.module || resolver.IsEmbedded(union.Address) {
			continue
		}
		fmt.Fprintf(b, "\t\tcase %q: return %s.Unmarshal%sJSON(data)\n", union.Address, strings.TrimSuffix(resolver.Qualified(union), "."+goName(union.Name)), goName(union.Name))
	}
	b.WriteString("\t\tdefault: return nil, fmt.Errorf(\"unknown generated named type %s\", typeName)\n\t\t}\n\t})\n}\n")
}
