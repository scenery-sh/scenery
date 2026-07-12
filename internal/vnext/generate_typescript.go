package vnext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/clientgen"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/standardauthmeta"
)

func GenerateTypeScriptClients(root, selector string, check bool) (GenerateResult, error) {
	return generateTypeScriptClients(root, selector, check, false)
}

func generateTypeScriptClients(root, selector string, check, allowActiveChangeTransaction bool) (GenerateResult, error) {
	result, err := compile(root, allowActiveChangeTransaction)
	if err != nil {
		return GenerateResult{}, err
	}
	return generateTypeScriptClientsFromResult(result, selector, check)
}

func generateTypeScriptClientsFromResult(result *Result, selector string, check bool) (GenerateResult, error) {
	if result.ContractStatus != "valid" || result.Manifest == nil {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid vNext contract: %s", firstError(result.Diagnostics))
	}
	files, err := renderTypeScriptClientFiles(result, selector)
	if err != nil {
		return GenerateResult{}, err
	}
	return finishGeneratedFiles(result.Root, files, check, "generated vNext TypeScript clients are stale")
}

func renderTypeScriptClientFiles(result *Result, selector string) ([]generatedFile, error) {
	for _, target := range typescriptTargets(result.Manifest.Resources, "") {
		if _, err := managedTypeScriptOutputRoot(result, stringValue(target.Spec["output_root"])); err != nil {
			return nil, fmt.Errorf("TypeScript client %s: %w", target.Name, err)
		}
	}
	targets := typescriptTargets(result.Manifest.Resources, selector)
	if selector != "" && len(targets) == 0 {
		return nil, fmt.Errorf("TypeScript client target %q not found", selector)
	}
	var files []generatedFile
	for _, target := range targets {
		targetFiles, err := renderTypeScriptTarget(result, target)
		if err != nil {
			return nil, err
		}
		files = append(files, targetFiles...)
	}
	protectedDescriptors := map[string]bool{}
	if selector != "" {
		selected := map[string]bool{}
		for _, target := range targets {
			selected[target.Address] = true
		}
		for _, target := range typescriptTargets(result.Manifest.Resources, "") {
			if selected[target.Address] {
				continue
			}
			outputRoot := filepath.Join(result.Root, filepath.FromSlash(stringValue(target.Spec["output_root"])))
			protectedDescriptors[filepath.Clean(filepath.Join(outputRoot, "scenery.typescript-client-generated.v1.json"))] = true
			protectedDescriptors[filepath.Clean(filepath.Join(outputRoot, "legacy-v0", "scenery.legacy-typescript-client-generated.v1.json"))] = true
		}
	}
	var err error
	files, err = includeStaleGeneratedFiles(result.Root, files, map[string]bool{
		"scenery.typescript-client-generated.v1.json": true, "scenery.legacy-typescript-client-generated.v1.json": true,
	}, protectedDescriptors)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func typescriptTargets(resources []Resource, selector string) []Resource {
	var out []Resource
	selector = strings.TrimPrefix(selector, "typescript_client.")
	for _, r := range resources {
		if r.Kind == "scenery.typescript-client/v1" && (selector == "" || r.Name == selector) {
			out = append(out, r)
		}
	}
	return out
}

func renderTypeScriptTarget(result *Result, target Resource) ([]generatedFile, error) {
	outputRoot, ok := target.Spec["output_root"].(string)
	if !ok || outputRoot == "" {
		return nil, fmt.Errorf("TypeScript client %s requires output_root", target.Name)
	}
	root, err := managedTypeScriptOutputRoot(result, outputRoot)
	if err != nil {
		return nil, err
	}
	bindings := publicHTTPBindings(result.Manifest.Resources, target)
	legacyBindings := legacyHTTPBindings(result.Manifest.Resources, target, bindings)
	legacyFiles, legacyRevision, legacyFixtureDigest, err := renderLegacyTypeScriptFamily(result, target, root, legacyBindings)
	if err != nil {
		return nil, err
	}
	reachable := reachableResources(result.Manifest.Resources, bindings)
	projectionResources := typescriptProjectionResources(result.Manifest.Resources, bindings, reachable, target)
	revision := typescriptRevision(target, projectionResources)
	projection := typescriptCompatibilityProjection(target, projectionResources)
	recommendation := typescriptVersionRecommendation(filepath.Join(root, "scenery.typescript-client-generated.v1.json"), revision, projection)
	selection, err := clientSelectionManifest(bindings, legacyBindings, revision, legacyRevision)
	if err != nil {
		return nil, err
	}
	selectionBytes, _ := json.MarshalIndent(selection, "", "  ")
	selectionBytes = append(selectionBytes, '\n')
	files := []generatedFile{{Path: filepath.Join(root, "types.ts"), Bytes: []byte(renderTSTypes(reachable, bindings))}, {Path: filepath.Join(root, "runtime.ts"), Bytes: []byte(renderTSRuntime())}, {Path: filepath.Join(root, "client.ts"), Bytes: []byte(renderTSClient(target, bindings, result.Manifest.Resources))}, {Path: filepath.Join(root, "metadata.ts"), Bytes: []byte(renderTSMetadata(target, result.Manifest, bindings, reachable, revision, recommendation))}, {Path: filepath.Join(root, "index.ts"), Bytes: []byte("// Code generated by Scenery vNext. DO NOT EDIT.\nexport * from \"./client.js\";\nexport * from \"./types.js\";\nexport { date, dateTime, decimal, duration, jsonNumber, isJsonNumber, relativePath, SceneryClientError, url, uuid } from \"./runtime.js\";\nexport type { AuthenticationOptions, CallOptions, RetryRuntime } from \"./runtime.js\";\nexport * from \"./metadata.js\";\n")}, {Path: filepath.Join(root, "scenery.client-selection.v1.json"), Bytes: selectionBytes}}
	descriptor := map[string]any{
		"api_version": "scenery.typescript-client-generated/v1", "target": target.Address, "package": target.Spec["package"],
		"contract_revision": result.Manifest.ContractRevision, "typescript_client_revision": revision, "content_digest": artifactDigest(root, files),
		"profile": "scenery.typescript-client/v1", "profile_digest": profileIdentityDigest("scenery.typescript-client/v1"),
		"codec_profiles":        map[string]string{"scenery.http-codec/v1": profileIdentityDigest("scenery.http-codec/v1")},
		"compatibility_catalog": "scenery.compatibility-core/v1", "package_version_recommendation": recommendation, "compatibility_projection": projection,
		"covered_gateways": literalReferenceList(target.Spec["gateways"]), "covered_bindings": resourceAddresses(bindings),
		"covered_operations": resourceAddressesByKinds(reachable, "scenery.operation/v1"), "covered_types": resourceAddressesByKinds(reachable, "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1"),
		"http_surface_revisions": projectionRevisionsForGateways(result, literalReferenceList(target.Spec["gateways"]), result.HTTPSurfaceRevisions),
		"openapi_revisions":      projectionRevisionsForGateways(result, literalReferenceList(target.Spec["gateways"]), result.OpenAPIRevisions),
		"typescript_version":     target.Spec["typescript_version"], "javascript_target": target.Spec["javascript_target"],
		"generator": "scenery.vnext.typescript/v1", "files": generatedFilePaths(root, files),
		"legacy_client_revision": optionalRevisionValue(legacyRevision), "legacy_fixture_catalog_digest": optionalRevisionValue(legacyFixtureDigest),
	}
	b, _ := json.MarshalIndent(descriptor, "", "  ")
	files = append(files, generatedFile{Path: filepath.Join(root, "scenery.typescript-client-generated.v1.json"), Bytes: append(b, '\n')})
	return append(files, legacyFiles...), nil
}

func managedTypeScriptOutputRoot(result *Result, outputRoot string) (string, error) {
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(outputRoot)))
	if result == nil || outputRoot == "" || filepath.IsAbs(outputRoot) || strings.Contains(outputRoot, "\\") || clean != outputRoot || clean == "." || forbiddenWorkspacePath(clean) {
		return "", fmt.Errorf("output_root must be a normalized safe workspace-relative path")
	}
	for _, source := range result.Sources {
		if source.Relative != "scenery.scn" {
			continue
		}
		for _, block := range source.Blocks {
			if block.Type != "workspace" {
				continue
			}
			for _, declared := range literalStringList(block, "managed_generated_roots") {
				managed := filepath.ToSlash(filepath.Clean(filepath.FromSlash(declared)))
				if clean == managed || strings.HasPrefix(clean, managed+"/") {
					return filepath.Join(result.Root, filepath.FromSlash(clean)), nil
				}
			}
		}
	}
	return "", fmt.Errorf("output_root must be beneath a declared managed generated root")
}

func optionalRevisionValue(value string) any {
	if value == "" {
		return nil
	}
	return value
}

type typescriptClientProjection struct {
	Target    Resource   `json:"target"`
	Resources []Resource `json:"resources"`
}

func typescriptCompatibilityProjection(target Resource, resources []Resource) typescriptClientProjection {
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		projection, include := contractResourceProjection(resource)
		if include {
			projected = append(projected, projection)
		}
	}
	return typescriptClientProjection{
		Target:    Resource{Address: target.Address, Kind: target.Kind, Name: target.Name, Module: target.Module, Spec: target.Spec},
		Resources: projected,
	}
}

func typescriptVersionRecommendation(path, revision string, projection typescriptClientProjection) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return "initial"
	}
	var previous struct {
		Revision       string                     `json:"typescript_client_revision"`
		Recommendation string                     `json:"package_version_recommendation"`
		Projection     typescriptClientProjection `json:"compatibility_projection"`
	}
	if json.Unmarshal(b, &previous) != nil {
		return "major"
	}
	if previous.Revision == revision {
		if previous.Recommendation != "" {
			return previous.Recommendation
		}
		return "none"
	}
	if previous.Projection.Target.Address == "" || !semanticEqual(previous.Projection.Target, projection.Target) {
		return "major"
	}
	diff := CompareManifests(&Manifest{Resources: previous.Projection.Resources}, &Manifest{Resources: projection.Resources}, CompareOptions{View: "artifact"})
	if diff.Summary.Breaking > 0 || diff.Summary.Unknown > 0 || diff.Summary.MigrationRequired > 0 {
		return "major"
	}
	if len(diff.Changes) > 0 {
		return "minor"
	}
	return "patch"
}

func typescriptProjectionResources(all, bindings, reachable []Resource, target Resource) []Resource {
	selected := map[string]Resource{}
	for _, resource := range append(append([]Resource(nil), reachable...), bindings...) {
		selected[resource.Address] = resource
	}
	gateways := map[string]bool{}
	for _, value := range anyList(target.Spec["gateways"]) {
		gateways[lastRef(refOrString(value))] = true
	}
	for _, resource := range all {
		if resource.Kind == "scenery.http-gateway/v1" && gateways[resource.Name] {
			selected[resource.Address] = resource
		}
	}
	result := make([]Resource, 0, len(selected))
	for _, resource := range selected {
		result = append(result, resource)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Address < result[j].Address })
	return result
}

func profileIdentityDigest(identity string) string {
	sum := sha256.Sum256([]byte("scenery.profile.v1\x00" + identity))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func literalReferenceList(value any) []string {
	var result []string
	for _, item := range anyList(value) {
		result = append(result, refOrString(item))
	}
	sort.Strings(result)
	return result
}

func resourceAddressesByKinds(resources []Resource, kinds ...string) []string {
	allowed := map[string]bool{}
	for _, kind := range kinds {
		allowed[kind] = true
	}
	var filtered []Resource
	for _, resource := range resources {
		if allowed[resource.Kind] {
			filtered = append(filtered, resource)
		}
	}
	return resourceAddresses(filtered)
}

func publicHTTPBindings(resources []Resource, target Resource) []Resource {
	var out []Resource
	operations := map[string]Resource{}
	exported, exportDeclared := exportedOperations(resources)
	gateways := map[string]bool{}
	for _, gateway := range anyList(target.Spec["gateways"]) {
		gateways[refOrString(gateway)] = true
		gateways[lastRef(refOrString(gateway))] = true
	}
	include := map[string]bool{}
	for _, value := range anyList(target.Spec["include"]) {
		include[refOrString(value)] = true
		include[lastRef(refOrString(value))] = true
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.operation/v1" {
			operations[resource.Address] = resource
			operations[resource.Module+"/operation/"+resource.Name] = resource
		}
	}
	for _, r := range resources {
		if r.Kind != "scenery.binding/v1" {
			continue
		}
		if r.Origin.Kind != "authored" {
			continue
		}
		opRef := refString(r.Spec["operation"])
		op := operations[opRef]
		if op.Address == "" {
			op = operations[r.Module+"/operation/"+lastRef(opRef)]
		}
		handler, _ := op.Spec["handler"].(map[string]any)
		if adapter, _ := handler["adapter"].(string); adapter == "legacy_go_v0" {
			continue
		}
		if protocol, _ := r.Spec["protocol"].(string); protocol != "http" {
			continue
		}
		if exportDeclared[r.Module] && !exported[op.Address] {
			continue
		}
		gateway := refOrString(r.Spec["gateway"])
		if len(gateways) > 0 && !gateways[gateway] && !gateways[lastRef(gateway)] {
			continue
		}
		if len(include) > 0 && !include[r.Address] && !include[r.Name] && !include[op.Address] && !include[op.Name] {
			continue
		}
		httpSpec, _ := r.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		if guarantee, _ := httpSpec["guarantee"].(string); guarantee == "implementation_declared" || guarantee == "opaque" || guarantee == "advisory" {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func exportedOperations(resources []Resource) (map[string]bool, map[string]bool) {
	exported := map[string]bool{}
	declared := map[string]bool{}
	for _, resource := range resources {
		if resource.Kind != "scenery.module/v1" {
			continue
		}
		module := resource.Name
		values, exists := resource.Spec["exports"]
		if !exists {
			continue
		}
		declared[module] = true
		walkRefs(values, "/exports", func(_ string, reference string) {
			if parts := strings.Split(reference, "/"); len(parts) == 3 && parts[0] == module && parts[1] == "operation" {
				exported[reference] = true
				return
			}
			parts := strings.Split(reference, ".")
			if len(parts) >= 2 && parts[0] == "operation" {
				exported[resourceAddress(module, "operation", parts[1])] = true
			}
		})
	}
	return exported, declared
}

func clientSelectionManifest(nativeBindings, legacyBindings []Resource, nativeRevision, legacyRevision string) (map[string]any, error) {
	selection := map[string]any{}
	for _, family := range []struct {
		name     string
		revision string
		bindings []Resource
	}{{"native", nativeRevision, nativeBindings}, {"legacy_v0", legacyRevision, legacyBindings}} {
		for _, binding := range family.bindings {
			operation := refString(binding.Spec["operation"])
			if !strings.Contains(operation, "/") {
				operation = resourceAddress(binding.Module, "operation", lastRef(operation))
			}
			if previous, exists := selection[operation].(map[string]any); exists && (previous["family"] != family.name || previous["revision"] != family.revision) {
				return nil, fmt.Errorf("client operation %s is selected by conflicting artifact families", operation)
			}
			selection[operation] = map[string]any{"family": family.name, "revision": family.revision}
		}
	}
	return map[string]any{"api_version": "scenery.client-selection.v1", "operations": selection}, nil
}

func legacyHTTPBindings(resources []Resource, target Resource, nativeBindings []Resource) []Resource {
	native := make(map[string]bool, len(nativeBindings))
	for _, binding := range nativeBindings {
		native[binding.Address] = true
	}
	gateways := map[string]bool{}
	for _, gateway := range anyList(target.Spec["gateways"]) {
		gateways[refOrString(gateway)] = true
		gateways[lastRef(refOrString(gateway))] = true
	}
	include := map[string]bool{}
	for _, value := range anyList(target.Spec["include"]) {
		include[refOrString(value)] = true
		include[lastRef(refOrString(value))] = true
	}
	var bindings []Resource
	for _, binding := range resources {
		if binding.Kind != "scenery.binding/v1" || binding.Origin.Kind != "legacy_v0" || native[binding.Address] {
			continue
		}
		if httpSpec, _ := binding.Spec["http"].(map[string]any); httpSpec == nil {
			continue
		}
		if access, _ := binding.Origin.LegacyIdentity["access"].(string); access == "private" {
			continue
		}
		gateway := refOrString(binding.Spec["gateway"])
		if len(gateways) > 0 && !gateways[gateway] && !gateways[lastRef(gateway)] {
			continue
		}
		operation := refString(binding.Spec["operation"])
		if len(include) > 0 && !include[binding.Address] && !include[binding.Name] && !include[operation] && !include[lastRef(operation)] {
			continue
		}
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func renderLegacyTypeScriptFamily(result *Result, target Resource, root string, bindings []Resource) ([]generatedFile, string, string, error) {
	if len(bindings) == 0 {
		return nil, "", "", nil
	}
	cfg, err := legacyTypeScriptClientConfig(result)
	if err != nil {
		return nil, "", "", err
	}
	services := map[string]bool{}
	for _, binding := range bindings {
		services[binding.Module] = true
		if name, _ := binding.Origin.LegacyIdentity["service"].(string); name != "" {
			services[name] = true
		}
	}
	legacyApp, err := boundedLegacyClientApp(result, cfg, services)
	if err != nil {
		return nil, "", "", err
	}
	clientBytes, err := clientgen.GenerateTypeScript(legacyApp, clientgen.TypeScriptOptions{
		AppSlug: cfg.AppID(), StandardAuth: cfg.Auth.Enabled, StandardAuthGoogle: cfg.Auth.GoogleOAuth.Enabled,
	})
	if err != nil {
		return nil, "", "", fmt.Errorf("generate legacy TypeScript client: %w", err)
	}
	clientDigest := sha256.Sum256(clientBytes)
	revision := revisionHash("scenery.legacy-typescript-client.v1\x00", map[string]any{
		"target": target.Address, "bindings": resourceAddresses(bindings),
		"client_digest": "sha256:" + hex.EncodeToString(clientDigest[:]), "frontend": "scenery.legacy.v0",
	})
	legacyRoot := filepath.Join(root, "legacy-v0")
	files := []generatedFile{{Path: filepath.Join(legacyRoot, "client.ts"), Bytes: clientBytes}}
	descriptor := map[string]any{
		"api_version": "scenery.legacy-typescript-client-generated/v1", "artifact_family": "legacy_v0",
		"compatibility_frontend": "scenery.legacy.v0", "target": target.Address,
		"package": fmt.Sprint(target.Spec["package"]) + "-legacy-v0", "import_path": "./legacy-v0/client.js",
		"contract_revision": result.Manifest.ContractRevision, "legacy_client_revision": revision,
		"fixture_catalog_digest": nil, "covered_bindings": resourceAddresses(bindings),
		"generator": "scenery.legacy.typescript/v0", "files": generatedFilePaths(legacyRoot, files),
		"content_digest": artifactDigest(legacyRoot, files),
	}
	descriptorBytes, _ := json.MarshalIndent(descriptor, "", "  ")
	files = append(files, generatedFile{Path: filepath.Join(legacyRoot, "scenery.legacy-typescript-client-generated.v1.json"), Bytes: append(descriptorBytes, '\n')})
	return files, revision, "", nil
}

func legacyTypeScriptClientConfig(result *Result) (appcfg.Config, error) {
	if result == nil || result.Manifest == nil || result.Migration == nil {
		return appcfg.Config{}, fmt.Errorf("generate legacy TypeScript client: compiled migration snapshot is unavailable")
	}
	if result.Migration.LegacyConfig != "" {
		_, cfg, err := appcfg.DiscoverRoot(result.Root)
		if err != nil {
			return appcfg.Config{}, fmt.Errorf("discover legacy client app: %w", err)
		}
		return cfg, nil
	}
	cfg := appcfg.Config{Name: result.Manifest.Application.Name, ID: result.Manifest.Application.Name}
	cfg.Auth.Enabled, cfg.Auth.GoogleOAuth.Enabled = compiledLegacyClientAuth(result.Manifest.Resources)
	return cfg, nil
}

func compiledLegacyClientAuth(resources []Resource) (standardAuth, googleOAuth bool) {
	baseMethods := map[string]bool{}
	for _, endpoint := range standardauthmeta.Endpoints(false) {
		baseMethods[endpoint.Name] = true
	}
	googleMethods := map[string]bool{}
	for _, endpoint := range standardauthmeta.Endpoints(true) {
		if !baseMethods[endpoint.Name] {
			googleMethods[endpoint.Name] = true
		}
	}
	for _, resource := range resources {
		if resource.Kind == "scenery.authentication/v1" && refOrString(resource.Spec["provider"]) == "std.provider.standard_auth" {
			standardAuth = true
		}
		if resource.Kind != "scenery.operation/v1" {
			continue
		}
		handler, _ := resource.Spec["handler"].(map[string]any)
		if stringValue(handler["adapter"]) != "legacy_standard_auth_v0" {
			continue
		}
		standardAuth = true
		if googleMethods[stringValue(handler["method"])] {
			googleOAuth = true
		}
	}
	return standardAuth, googleOAuth
}

func boundedLegacyClientApp(result *Result, cfg appcfg.Config, selected map[string]bool) (*model.App, error) {
	if result == nil {
		return &model.App{Name: cfg.Name}, nil
	}
	app := &model.App{Name: cfg.Name, Root: result.Root}
	if result.Migration == nil || result.Manifest == nil {
		return app, nil
	}
	targetServices := map[string][]MigrationService{}
	for _, service := range result.Migration.Services {
		if service.State == "native" || !selected[service.Name] {
			continue
		}
		targetServices[service.LegacyTarget] = append(targetServices[service.LegacyTarget], service)
	}
	targetNames := make([]string, 0, len(targetServices))
	for name := range targetServices {
		targetNames = append(targetNames, name)
	}
	sort.Strings(targetNames)
	found := map[string]bool{}
	for _, targetReference := range targetNames {
		target, err := ResolveGoBuildTarget(result, strings.TrimPrefix(targetReference, "go_target."), "")
		if err != nil {
			return nil, fmt.Errorf("resolve legacy client Go target %s: %w", targetReference, err)
		}
		packageRoots := make([]string, 0, len(targetServices[targetReference]))
		for _, service := range targetServices[targetReference] {
			packageRoots = append(packageRoots, service.Package)
		}
		legacy, err := parse.AppPackagesWithTarget(result.Root, cfg.Name, packageRoots, target.Context)
		if err != nil {
			return nil, fmt.Errorf("parse bounded legacy client app for %s: %w", targetReference, err)
		}
		if app.ModulePath == "" {
			app.ModulePath = legacy.ModulePath
		}
		for _, service := range legacy.Services {
			if selected[service.Name] {
				app.Services = append(app.Services, service)
				found[service.Name] = true
			}
		}
	}
	for _, service := range result.Migration.Services {
		if service.State != "native" && selected[service.Name] && !found[service.Name] {
			return nil, fmt.Errorf("legacy client service %s was not discovered beneath its declared package root and Go target", service.Name)
		}
	}
	sort.Slice(app.Services, func(i, j int) bool { return app.Services[i].Name < app.Services[j].Name })
	return app, nil
}

func reachableResources(resources, bindings []Resource) []Resource {
	byAddress := map[string]Resource{}
	operations := map[string]Resource{}
	for _, b := range bindings {
		address := resolveResourceRef(b, refString(b.Spec["operation"]), "operation")
		operations[address] = Resource{}
	}
	for _, r := range resources {
		byAddress[r.Address] = r
		if _, selected := operations[r.Address]; selected {
			operations[r.Address] = r
		}
	}
	selected := map[string]Resource{}
	var addType func(string, any)
	addType = func(module string, value any) {
		for _, name := range typeValueNames(value) {
			address := ""
			if strings.Contains(name, "/") {
				address = name
			} else {
				parts := strings.Split(name, ".")
				if len(parts) != 2 || (parts[0] != "record" && parts[0] != "enum" && parts[0] != "union") {
					continue
				}
				address = resourceAddress(module, parts[0], parts[1])
			}
			if _, exists := selected[address]; exists {
				continue
			}
			resource, exists := byAddress[address]
			if !exists {
				continue
			}
			selected[address] = resource
			switch resource.Kind {
			case "scenery.record/v1":
				for _, field := range namedChildren(resource.Spec, "field") {
					addType(resource.Module, field["type"])
				}
			case "scenery.union/v1":
				for _, variant := range namedChildren(resource.Spec, "variant") {
					addType(resource.Module, variant["type"])
				}
			}
		}
	}
	for address, operation := range operations {
		if operation.Address == "" {
			continue
		}
		selected[address] = operation
		addType(operation.Module, operation.Spec["input"])
		for _, kind := range []string{"result", "error"} {
			for _, variant := range namedChildren(operation.Spec, kind) {
				addType(operation.Module, variant["type"])
			}
		}
	}
	out := make([]Resource, 0, len(selected))
	for _, resource := range selected {
		out = append(out, resource)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	return out
}

func typeValueNames(value any) []string {
	if reference := refString(value); reference != "" {
		return []string{reference}
	}
	if expression, ok := value.(map[string]any); ok {
		if raw, ok := expression["$expression"].(string); ok {
			return typeExpressionNames(raw)
		}
	}
	return nil
}
func typescriptRevision(target Resource, resources []Resource) string {
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		projection, include := contractResourceProjection(resource)
		if include {
			projected = append(projected, projection)
		}
	}
	targetProjection := Resource{Address: target.Address, Kind: target.Kind, Name: target.Name, Module: target.Module, Spec: target.Spec}
	b, _ := MarshalCanonical(map[string]any{"target": targetProjection, "resources": projected, "profile": "scenery.typescript-client/v1", "codec": "scenery.http-codec/v1"})
	sum := sha256.Sum256(append([]byte("scenery.typescript-client-revision.v1\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func renderTSTypes(resources []Resource, bindingSets ...[]Resource) string {
	var bindings []Resource
	for _, set := range bindingSets {
		bindings = append(bindings, set...)
	}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery vNext. DO NOT EDIT.\n\ndeclare const jsonNumberBrand: unique symbol;\nexport type JsonValue = null | boolean | string | JsonNumber | readonly JsonValue[] | { readonly [key: string]: JsonValue };\nexport interface JsonNumber { readonly coefficient: string; readonly scale: number; readonly [jsonNumberBrand]: true }\nexport type Unit = Readonly<Record<never, never>>;\nexport type UUIDString = string & { readonly __uuid: unique symbol };\nexport type DateString = string & { readonly __date: unique symbol };\nexport type DateTimeString = string & { readonly __datetime: unique symbol };\nexport type DurationString = string & { readonly __duration: unique symbol };\nexport type DecimalString = string & { readonly __decimal: unique symbol };\nexport type URLString = string & { readonly __url: unique symbol };\nexport type RelativePathString = string & { readonly __relativePath: unique symbol };\nexport interface Problem { readonly code: string; readonly message: string; readonly path?: string }\nexport interface EnqueueReceipt { readonly durableIdentity: string; readonly executionId: string; readonly acceptedRevision: string; readonly statusUrl?: URLString }\n\n")
	for _, r := range resources {
		switch r.Kind {
		case "scenery.record/v1":
			fmt.Fprintf(&b, "export interface %s {\n", goName(r.Name))
			for _, field := range namedChildren(r.Spec, "field") {
				name, _ := field["name"].(string)
				optional := tsOptional(field["type"])
				fmt.Fprintf(&b, "  readonly %s%s: %s;\n", tsName(name), optional, tsType(field["type"]))
			}
			if r.Spec["unknown_fields"] == "preserve" {
				b.WriteString("  readonly unknownFields: Readonly<Record<string, JsonValue>>;\n")
			}
			b.WriteString("}\n\n")
		case "scenery.enum/v1":
			fmt.Fprintf(&b, "export type %s = ", goName(r.Name))
			values := namedChildren(r.Spec, "value")
			for i, v := range values {
				if i > 0 {
					b.WriteString(" | ")
				}
				name, _ := v["name"].(string)
				wire := wireName(v, name)
				fmt.Fprintf(&b, "%q", wire)
			}
			if r.Spec["open"] == true {
				if len(values) > 0 {
					b.WriteString(" | ")
				}
				fmt.Fprintf(&b, "(string & { readonly __%sUnknown: unique symbol })", tsName(r.Name))
			}
			b.WriteString(";\n\n")
			fmt.Fprintf(&b, "export const %s = {\n", goName(r.Name))
			for _, value := range values {
				name, _ := value["name"].(string)
				fmt.Fprintf(&b, "  %s: %q,\n", goName(name), wireName(value, name))
			}
			b.WriteString("} as const;\n")
			fmt.Fprintf(&b, "export function isKnown%s(value: %s): boolean { return (Object.values(%s) as readonly string[]).includes(value); }\n\n", goName(r.Name), goName(r.Name), goName(r.Name))
		case "scenery.union/v1":
			fmt.Fprintf(&b, "export type %s =\n", goName(r.Name))
			for _, variant := range namedChildren(r.Spec, "variant") {
				name, _ := variant["name"].(string)
				tag := wireName(variant, name)
				fmt.Fprintf(&b, "  | { readonly kind: %q; readonly value: %s }\n", tag, tsType(variant["type"]))
			}
			if r.Spec["open"] == true {
				b.WriteString("  | { readonly kind: string; readonly value: JsonValue; readonly unknown: true }\n")
			}
			b.WriteString(";\n\n")
		}
	}
	for _, r := range resources {
		if r.Kind != "scenery.operation/v1" {
			continue
		}
		name := goName(r.Name)
		inputType := tsType(r.Spec["input"])
		if inputType != name+"Input" {
			fmt.Fprintf(&b, "export type %sInput = %s;\n\n", name, inputType)
		}
		fmt.Fprintf(&b, "export type %sOutcome =\n", name)
		variants := append(namedChildren(r.Spec, "result"), namedChildren(r.Spec, "error")...)
		transportVariants := tsTransportOutcomeVariants(r, bindings)
		if len(variants) == 0 && len(transportVariants) == 0 {
			b.WriteString("  never\n")
		}
		for i, v := range variants {
			variant, _ := v["name"].(string)
			kind := "result"
			field := "value"
			if i >= len(namedChildren(r.Spec, "result")) {
				kind = "error"
				field = "problem"
			}
			fmt.Fprintf(&b, "  | { readonly kind: %q; readonly name: %q; readonly %s: %s }\n", kind, variant, field, tsType(v["type"]))
		}
		for _, variant := range transportVariants {
			fmt.Fprintf(&b, "  | { readonly kind: %q; readonly name: %q; readonly %s: %s }\n", variant.Kind, variant.Name, variant.Field, variant.Type)
		}
		b.WriteString(";\n\n")
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

type tsOutcomeVariant struct{ Kind, Name, Field, Type string }

func tsTransportOutcomeVariants(operation Resource, bindings []Resource) []tsOutcomeVariant {
	seen := map[string]bool{}
	var variants []tsOutcomeVariant
	for _, binding := range bindings {
		if binding.Module != operation.Module || lastRef(refString(binding.Spec["operation"])) != operation.Name {
			continue
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		for _, response := range namedChildren(httpSpec, "response") {
			when := refString(response["when"])
			if strings.HasPrefix(when, "result.") || strings.HasPrefix(when, "error.") || strings.HasPrefix(when, "system.") {
				continue
			}
			name := stringValue(response["name"])
			if name == "" {
				name = lastRef(when)
			}
			key := when + "\x00" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			if when == "dispatch.enqueued" {
				variants = append(variants, tsOutcomeVariant{Kind: "enqueue", Name: name, Field: "receipt", Type: "EnqueueReceipt"})
			} else {
				variants = append(variants, tsOutcomeVariant{Kind: "failure", Name: name, Field: "problem", Type: "Problem"})
			}
		}
	}
	sort.Slice(variants, func(i, j int) bool {
		if variants[i].Kind != variants[j].Kind {
			return variants[i].Kind < variants[j].Kind
		}
		return variants[i].Name < variants[j].Name
	})
	return variants
}

func renderTSClient(target Resource, bindings, resources []Resource) string {
	className := goName(target.Name) + "Client"
	retry := tsRetryTarget(target)
	var b strings.Builder
	b.WriteString("// Code generated by Scenery vNext. DO NOT EDIT.\n")
	b.WriteString("import * as Runtime from \"./runtime.js\";\n")
	b.WriteString("import type * as Types from \"./types.js\";\n\n")
	fmt.Fprintf(&b, "export interface %sOptions { readonly baseUrl: Types.URLString; readonly fetch?: typeof globalThis.fetch; readonly defaultHeaders?: Readonly<Record<string, string>>; readonly authentication?: Runtime.AuthenticationOptions", className)
	if retry.Enabled {
		b.WriteString("; readonly retryRuntime: Runtime.RetryRuntime")
	}
	b.WriteString(" }\n\n")
	b.WriteString(renderTSRegistry(reachableResources(resources, bindings)))
	fmt.Fprintf(&b, "\nexport class %s {\n", className)
	b.WriteString("  readonly #baseUrl: string;\n  readonly #fetch: typeof globalThis.fetch;\n  readonly #headers: Readonly<Record<string, string>>;\n  readonly #authentication?: Runtime.AuthenticationOptions;\n")
	if retry.Enabled {
		b.WriteString("  readonly #retryRuntime: Runtime.RetryRuntime;\n")
	}
	fmt.Fprintf(&b, "  constructor(options: %sOptions) {\n", className)
	b.WriteString("    if (!/^https?:\\/\\/[^/?#]+(?:\\/[^?#]*)?$/.test(options.baseUrl)) throw new Runtime.SceneryClientError(\"invalid_options\", \"\", \"baseUrl must be an absolute hierarchical HTTP URL without query or fragment\");\n")
	b.WriteString("    this.#baseUrl = options.baseUrl.replace(/\\/$/, \"\");\n    this.#fetch = options.fetch ?? globalThis.fetch;\n    this.#headers = Object.freeze({ ...(options.defaultHeaders ?? {}) });\n    this.#authentication = options.authentication === undefined ? undefined : Object.freeze({ ...options.authentication });\n")
	if retry.Enabled {
		b.WriteString("    this.#retryRuntime = options.retryRuntime;\n")
	}
	b.WriteString("  }\n")
	ops := map[string]Resource{}
	for _, r := range resources {
		if r.Kind == "scenery.operation/v1" {
			ops[r.Name] = r
		}
	}
	operationCounts := map[string]int{}
	for _, binding := range bindings {
		operationCounts[lastRef(refString(binding.Spec["operation"]))]++
	}
	for _, binding := range bindings {
		opRef := refString(binding.Spec["operation"])
		opName := lastRef(opRef)
		op := ops[opName]
		methodName := tsName(opName)
		if operationCounts[opName] > 1 {
			methodName += "Via" + goName(binding.Name)
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		method, _ := httpSpec["method"].(string)
		path, _ := httpSpec["path"].(string)
		path = tsBindingPath(binding, path, resources)
		inputName := "input"
		if refString(op.Spec["input"]) == "std.type.unit" {
			inputName = "_input"
		}
		fmt.Fprintf(&b, "  async %s(%s: Types.%sInput, options: Runtime.CallOptions = {}): Promise<Types.%sOutcome> {\n", methodName, inputName, goName(opName), goName(opName))
		fmt.Fprintf(&b, "    const binding = %q;\n", binding.Address)
		b.WriteString("    if (options.signal?.aborted) throw new Runtime.SceneryClientError(\"cancelled\", binding, \"request cancelled\");\n")
		fmt.Fprintf(&b, "    let path = %q;\n", path)
		for _, mapping := range namedChildren(httpSpec, "path_parameter") {
			fmt.Fprintf(&b, "    path = path.replace(%q, Runtime.encodeRFC3986(Runtime.encodeHTTPValue(input.%s, %s, typeRegistry)));\n", "{"+stringValue(mapping["name"])+"}", tsInputTargetProperty(mapping["to"]), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
		}
		b.WriteString("    const query: string[] = [];\n")
		for _, mapping := range namedChildren(httpSpec, "query_parameter") {
			fmt.Fprintf(&b, "    Runtime.appendQuery(query, %q, input.%s, %q, %s, typeRegistry);\n", stringValue(mapping["name"]), tsInputTargetProperty(mapping["to"]), defaultTSQueryEncoding(stringValue(mapping["encoding"])), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
		}
		b.WriteString("    if (query.length > 0) path += `?${query.join(\"&\")}`;\n")
		b.WriteString("    const headers = Runtime.mergeHeaders(this.#headers, options.headers, binding);\n")
		b.WriteString("    if (this.#authentication?.authorization !== undefined) headers.set(\"authorization\", this.#authentication.authorization);\n")
		for _, mapping := range namedChildren(httpSpec, "header") {
			fmt.Fprintf(&b, "    Runtime.appendHeader(headers, %q, input.%s, %q, %s, typeRegistry);\n", stringValue(mapping["name"]), tsInputTargetProperty(mapping["to"]), defaultTSQueryEncoding(stringValue(mapping["encoding"])), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
		}
		b.WriteString("    const cookies: string[] = [];\n")
		for _, mapping := range namedChildren(httpSpec, "cookie") {
			fmt.Fprintf(&b, "    Runtime.appendCookie(cookies, %q, input.%s, %s, typeRegistry);\n", stringValue(mapping["name"]), tsInputTargetProperty(mapping["to"]), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
		}
		b.WriteString("    if (cookies.length > 0) headers.set(\"cookie\", cookies.join(\"; \"));\n")
		bodyExpression, bodyCodec, bodyDescriptor := tsRequestBody(httpSpec, op, resources)
		if bodyCodec != "" && bodyCodec != "multipart" {
			requestMediaTypes := []string(nil)
			if bodySpec, _ := httpSpec["body"].(map[string]any); bodySpec != nil {
				requestMediaTypes = literalStringListFromValue(bodySpec["accepted_media_types"])
			}
			if len(requestMediaTypes) == 0 {
				requestMediaTypes = []string{defaultHTTPMediaType(bodyCodec)}
			}
			fmt.Fprintf(&b, "    headers.set(\"content-type\", %q);\n", requestMediaTypes[0])
		}
		body := "undefined"
		if bodyCodec == "multipart" {
			fmt.Fprintf(&b, "    const multipartBody = Runtime.encodeMultipartRequestBody(%s, %s, typeRegistry);\n", bodyExpression, tsMultipartBodyDescriptor(httpSpec, op, resources))
			b.WriteString("    headers.set(\"content-type\", multipartBody.contentType);\n")
			body = "multipartBody.body"
		} else if bodyCodec != "" {
			body = fmt.Sprintf("Runtime.encodeRequestBody(%s, %q, %s, typeRegistry)", bodyExpression, bodyCodec, bodyDescriptor)
		}
		fmt.Fprintf(&b, "    let response: Response;\n    try {\n")
		requestInit := fmt.Sprintf("{ method: %q, signal: options.signal, headers, body: %s, credentials: this.#authentication?.credentials }", method, body)
		if retry.Enabled {
			fmt.Fprintf(&b, "      response = await Runtime.fetchWithRetry(this.#fetch, this.#baseUrl + path, %s, options.signal, this.#retryRuntime, { maximumAttempts: %d, statuses: %s, maximumDelayMilliseconds: %d });\n", requestInit, retry.MaximumAttempts, retry.StatusesLiteral, retry.MaximumDelayMilliseconds)
		} else {
			fmt.Fprintf(&b, "      response = await this.#fetch(this.#baseUrl + path, %s);\n", requestInit)
		}
		b.WriteString("    } catch (cause) {\n      throw new Runtime.SceneryClientError(options.signal?.aborted ? \"cancelled\" : \"network\", binding, \"request failed\", cause);\n    }\n")
		renderTSResponseCases(&b, op, httpSpec, resources)
		b.WriteString("    throw new Runtime.SceneryClientError(\"contract_violation\", binding, `unexpected response ${response.status}`);\n  }\n")
	}
	b.WriteString("}\n")
	return b.String()
}

type tsRetryConfiguration struct {
	Enabled                  bool
	MaximumAttempts          int
	StatusesLiteral          string
	MaximumDelayMilliseconds int
}

func tsRetryTarget(target Resource) tsRetryConfiguration {
	retry, _ := target.Spec["retry"].(map[string]any)
	if retry == nil {
		return tsRetryConfiguration{}
	}
	attempts, _ := integerValue(retry["maximum_attempts"])
	maximumDelay, ok := integerValue(retry["maximum_delay_milliseconds"])
	if !ok {
		maximumDelay = 60_000
	}
	statuses := []int{408, 429, 500, 502, 503, 504}
	if configured := anyList(retry["statuses"]); len(configured) > 0 {
		statuses = statuses[:0]
		for _, value := range configured {
			if status, valid := integerValue(value); valid {
				statuses = append(statuses, status)
			}
		}
		sort.Ints(statuses)
	}
	encoded, _ := json.Marshal(statuses)
	return tsRetryConfiguration{Enabled: true, MaximumAttempts: attempts, StatusesLiteral: string(encoded), MaximumDelayMilliseconds: maximumDelay}
}

func tsBindingPath(binding Resource, path string, resources []Resource) string {
	gatewayName := lastRef(refOrString(binding.Spec["gateway"]))
	base := "/"
	for _, resource := range resources {
		if resource.Kind == "scenery.http-gateway/v1" && resource.Name == gatewayName {
			base = stringValue(resource.Spec["base_path"])
			break
		}
	}
	if base == "" || base == "/" {
		return path
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}
