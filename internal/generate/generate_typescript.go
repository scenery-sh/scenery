package generate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/tscheck"
)

func GenerateTypeScriptClients(root, selector string, check bool) (GenerateResult, error) {
	return generateTypeScriptClients(root, selector, check, false)
}

func generateTypeScriptClients(root, selector string, check, allowActiveChangeTransaction bool) (GenerateResult, error) {
	result, err := compiler.Compile(root)
	if err != nil {
		return GenerateResult{}, err
	}
	return generateTypeScriptClientsFromResult(result, selector, check)
}

func generateTypeScriptClientsFromResult(result *Result, selector string, check bool) (GenerateResult, error) {
	if result.ContractStatus != "valid" || result.Manifest == nil {
		return GenerateResult{}, fmt.Errorf("cannot generate from invalid contract: %s", firstError(result.Diagnostics))
	}
	files, err := renderTypeScriptClientFiles(result, selector)
	if err != nil {
		return GenerateResult{}, err
	}
	if err := verifyRenderedTypeScriptReact(result, typescriptTargets(result.Manifest.Resources, selector), files); err != nil {
		return GenerateResult{}, err
	}
	return finishGeneratedFiles(result.Root, files, check, "generated TypeScript clients are stale")
}

// GenerateTypeScriptClientsFromResult renders one immutable compiler snapshot.
func GenerateTypeScriptClientsFromResult(result *compiler.Result, selector string, check bool) (GenerateResult, error) {
	return generateTypeScriptClientsFromResult(result, selector, check)
}

// SyncCachedTypeScriptClients refreshes only cache-materialized clients. It is
// safe for ordinary build/test/up orchestration because every write remains
// beneath app-local .scenery state.
func SyncCachedTypeScriptClients(result *compiler.Result) (GenerateResult, error) {
	if result == nil || !result.Valid() || result.Manifest == nil {
		return GenerateResult{}, nil
	}
	var files []generatedFile
	targets := cacheTypeScriptTargets(typescriptTargets(result.Manifest.Resources, ""))
	for _, target := range targets {
		targetFiles, err := renderTypeScriptTarget(result, target)
		if err != nil {
			return GenerateResult{}, err
		}
		outputRoot, err := typeScriptOutputRoot(result, target)
		if err != nil {
			return GenerateResult{}, err
		}
		if pathExists(outputRoot) {
			targetFiles, err = includeStaleGeneratedFiles(outputRoot, targetFiles, map[string]bool{"scenery.typescript-client-generated.json": true}, nil)
			if err != nil {
				return GenerateResult{}, err
			}
		}
		files = append(files, targetFiles...)
	}
	if err := verifyRenderedTypeScriptReact(result, targets, files); err != nil {
		return GenerateResult{}, err
	}
	return finishGeneratedFiles(result.Root, files, false, "generated TypeScript cache is stale")
}

func verifyRenderedTypeScriptReact(result *Result, targets []Resource, files []generatedFile) error {
	var binary string
	for _, target := range targets {
		react, ok := target.Spec["react"].(map[string]any)
		if !ok {
			continue
		}
		outputRoot, err := typeScriptOutputRoot(result, target)
		if err != nil {
			return err
		}
		var staged []tscheck.File
		for _, file := range files {
			if file.Remove {
				continue
			}
			relative, relErr := filepath.Rel(outputRoot, file.Path)
			if relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				staged = append(staged, tscheck.File{Path: file.Path, Bytes: file.Bytes})
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		if binary == "" {
			binary, err = tscheck.ManagedBinary(ctx, result.Root)
			if err != nil {
				cancel()
				return err
			}
		}
		err = tscheck.Check(ctx, binary, result.Root, outputRoot, stringValue(react["tsconfig"]), staged)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}

func renderTypeScriptClientFiles(result *Result, selector string) ([]generatedFile, error) {
	return renderTypeScriptClientFilesByMode(result, selector, false)
}

func renderTypeScriptClientFilesByMode(result *Result, selector string, sourceOnly bool) ([]generatedFile, error) {
	for _, target := range typescriptTargets(result.Manifest.Resources, "") {
		if sourceOnly && typeScriptMaterialization(target) != "source" {
			continue
		}
		if _, err := typeScriptOutputRoot(result, target); err != nil {
			return nil, fmt.Errorf("TypeScript client %s: %w", target.Name, err)
		}
	}
	targets := typescriptTargets(result.Manifest.Resources, selector)
	if sourceOnly {
		targets = sourceTypeScriptTargets(targets)
	}
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
			outputRoot, rootErr := typeScriptOutputRoot(result, target)
			if rootErr != nil {
				return nil, rootErr
			}
			protectedDescriptors[filepath.Clean(filepath.Join(outputRoot, "scenery.typescript-client-generated.json"))] = true
		}
	}
	var err error
	files, err = includeStaleGeneratedFiles(result.Root, files, map[string]bool{"scenery.typescript-client-generated.json": true}, protectedDescriptors)
	if err != nil {
		return nil, err
	}
	files, err = includeStaleUICatalogFiles(result, targets, files)
	if err != nil {
		return nil, err
	}
	return files, nil
}

func sourceTypeScriptTargets(targets []Resource) []Resource {
	result := targets[:0]
	for _, target := range targets {
		if typeScriptMaterialization(target) == "source" {
			result = append(result, target)
		}
	}
	return result
}

func cacheTypeScriptTargets(targets []Resource) []Resource {
	result := targets[:0]
	for _, target := range targets {
		if typeScriptMaterialization(target) == "cache" {
			result = append(result, target)
		}
	}
	return result
}

func typeScriptMaterialization(target Resource) string {
	return defaultString(stringValue(target.Spec["materialization"]), "source")
}

func typescriptTargets(resources []Resource, selector string) []Resource {
	var out []Resource
	selector = strings.TrimPrefix(selector, "typescript_client.")
	for _, r := range resources {
		if r.Kind == "scenery.typescript-client" && (selector == "" || r.Name == selector) {
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
	root, err := typeScriptOutputRoot(result, target)
	if err != nil {
		return nil, err
	}
	bindings := publicHTTPBindings(result.Manifest.Resources, target)
	reachable := reachableResources(result.Manifest.Resources, bindings)
	projectionResources := typescriptProjectionResources(result.Manifest.Resources, bindings, reachable, target)
	revision := typescriptRevision(target, projectionResources)
	files := []generatedFile{{Path: filepath.Join(root, "types.ts"), Bytes: []byte(renderTSTypes(reachable, bindings))}, {Path: filepath.Join(root, "runtime.ts"), Bytes: []byte(renderTSRuntime())}, {Path: filepath.Join(root, "client.ts"), Bytes: []byte(renderTSClient(target, bindings, result.Manifest.Resources))}, {Path: filepath.Join(root, "metadata.ts"), Bytes: []byte(renderTSMetadata(target, result.Manifest, bindings, reachable, revision))}, {Path: filepath.Join(root, "index.ts"), Bytes: []byte("// Code generated by Scenery. DO NOT EDIT.\nexport * from \"./client.js\";\nexport * from \"./types.js\";\nexport { date, dateTime, decimal, duration, jsonNumber, isJsonNumber, relativePath, SceneryClientError, url, uuid } from \"./runtime.js\";\nexport type { AuthenticationOptions, CallOptions, RetryRuntime } from \"./runtime.js\";\nexport * from \"./metadata.js\";\n")}}
	reactFiles, catalogRoots, err := renderTypeScriptReact(result, target, root, bindings)
	if err != nil {
		return nil, err
	}
	files = append(files, reactFiles...)
	descriptor := addGeneratedArtifactIdentity(map[string]any{
		"target": target.Address, "package": target.Spec["package"],
		"contract_revision": result.Manifest.ContractRevision, "typescript_client_revision": revision, "content_digest": artifactDigest(root, files),
		"compatibility_catalog": "scenery.compatibility-core",
		"covered_gateways":      literalReferenceList(target.Spec["gateways"]), "covered_bindings": resourceAddresses(bindings),
		"covered_operations": resourceAddressesByKinds(reachable, "scenery.operation"), "covered_types": resourceAddressesByKinds(reachable, "scenery.record", "scenery.enum", "scenery.union"),
		"http_surface_revisions": projectionRevisionsForGateways(result, literalReferenceList(target.Spec["gateways"]), result.HTTPSurfaceRevisions),
		"openapi_revisions":      projectionRevisionsForGateways(result, literalReferenceList(target.Spec["gateways"]), result.OpenAPIRevisions),
		"typescript_version":     target.Spec["typescript_version"], "javascript_target": target.Spec["javascript_target"],
		"ui_catalog_roots": catalogRoots,
		"generator":        "scenery.generate.typescript", "files": generatedFilePaths(root, files),
	}, typeScriptDescriptorKind, typeScriptSchemaDescriptor, result.Manifest.SpecRevision)
	b, _ := json.MarshalIndent(descriptor, "", "  ")
	files = append(files, generatedFile{Path: filepath.Join(root, "scenery.typescript-client-generated.json"), Bytes: append(b, '\n')})
	return files, nil
}

func typeScriptOutputRoot(result *Result, target Resource) (string, error) {
	if typeScriptMaterialization(target) == "cache" {
		if result == nil || target.Name == "" || strings.ContainsAny(target.Name, `/\\`) {
			return "", fmt.Errorf("TypeScript cache target name is invalid")
		}
		return filepath.Join(result.Root, ".scenery", "gen", "typescript", target.Name), nil
	}
	return managedTypeScriptOutputRoot(result, stringValue(target.Spec["output_root"]))
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
		if resource.Kind == "scenery.http-gateway" && gateways[resource.Name] {
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
	reactPageBindings := declaredReactPageBindings(resources, target)
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
		if resource.Kind == "scenery.operation" {
			operations[resource.Address] = resource
			operations[resource.Module+"/operation/"+resource.Name] = resource
		}
	}
	for _, r := range resources {
		if r.Kind != "scenery.binding" {
			continue
		}
		if r.Origin.Kind != "authored" && !reactPageBindings[r.Address] {
			continue
		}
		opRef := refString(r.Spec["operation"])
		op := operations[opRef]
		if op.Address == "" {
			op = operations[r.Module+"/operation/"+lastRef(opRef)]
		}
		if protocol, _ := r.Spec["protocol"].(string); protocol != "http" {
			continue
		}
		if exportDeclared[r.Module] && !exported[op.Address] && !reactPageBindings[r.Address] {
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

func declaredReactPageBindings(resources []Resource, target Resource) map[string]bool {
	result := map[string]bool{}
	if _, ok := target.Spec["react"].(map[string]any); !ok {
		return result
	}
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, table := range resources {
		if table.Kind != "scenery.table-page" || table.Origin.Kind == "expanded" {
			continue
		}
		crud := byAddress[resolveResourceRef(table, refString(table.Spec["source"]), "crud")]
		if crud.Kind == "scenery.crud" {
			result[resourceAddress(crud.Module, "binding", crud.Name+"_list_http")] = true
		}
	}
	for _, split := range resources {
		if split.Kind == "scenery.split-page" && split.Origin.Kind != "expanded" {
			result[resolveResourceRef(split, refString(split.Spec["source"]), "binding")] = true
		}
	}
	return result
}

func exportedOperations(resources []Resource) (map[string]bool, map[string]bool) {
	exported := map[string]bool{}
	declared := map[string]bool{}
	for _, resource := range resources {
		if resource.Kind != "scenery.module" {
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
			case "scenery.record":
				for _, field := range namedChildren(resource.Spec, "field") {
					addType(resource.Module, field["type"])
				}
			case "scenery.union":
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
	revisionInput := map[string]any{"target": targetProjection, "resources": projected}
	b, _ := MarshalCanonical(revisionInput)
	sum := sha256.Sum256(append([]byte("scenery.typescript-client-revision\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func renderTSTypes(resources []Resource, bindingSets ...[]Resource) string {
	var bindings []Resource
	for _, set := range bindingSets {
		bindings = append(bindings, set...)
	}
	var b strings.Builder
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n\ndeclare const jsonNumberBrand: unique symbol;\nexport type JsonValue = null | boolean | string | JsonNumber | readonly JsonValue[] | { readonly [key: string]: JsonValue };\nexport interface JsonNumber { readonly coefficient: string; readonly scale: number; readonly [jsonNumberBrand]: true }\nexport type Unit = Readonly<Record<never, never>>;\nexport type UUIDString = string & { readonly __uuid: unique symbol };\nexport type DateString = string & { readonly __date: unique symbol };\nexport type DateTimeString = string & { readonly __datetime: unique symbol };\nexport type DurationString = string & { readonly __duration: unique symbol };\nexport type DecimalString = string & { readonly __decimal: unique symbol };\nexport type URLString = string & { readonly __url: unique symbol };\nexport type RelativePathString = string & { readonly __relativePath: unique symbol };\nexport interface Problem { readonly code: string; readonly message: string; readonly path?: string }\nexport interface EnqueueReceipt { readonly durableIdentity: string; readonly executionId: string; readonly acceptedRevision: string; readonly statusUrl?: URLString }\n\n")
	for _, r := range resources {
		switch r.Kind {
		case "scenery.record":
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
		case "scenery.enum":
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
		case "scenery.union":
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
		if r.Kind != "scenery.operation" {
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
	b.WriteString("// Code generated by Scenery. DO NOT EDIT.\n")
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
	b.WriteString("    this.#baseUrl = options.baseUrl.replace(/\\/$/, \"\");\n    this.#fetch = options.fetch ?? globalThis.fetch.bind(globalThis);\n    this.#headers = Object.freeze({ ...(options.defaultHeaders ?? {}) });\n    this.#authentication = options.authentication === undefined ? undefined : Object.freeze({ ...options.authentication });\n")
	if retry.Enabled {
		b.WriteString("    this.#retryRuntime = options.retryRuntime;\n")
	}
	b.WriteString("  }\n")
	ops := map[string]Resource{}
	for _, r := range resources {
		if r.Kind == "scenery.operation" {
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
		pathTails := namedChildren(httpSpec, "path_tail")
		if len(pathTails) == 1 {
			suffix := "/{" + stringValue(pathTails[0]["name"]) + "...}"
			fmt.Fprintf(&b, "    let path = %q;\n", strings.TrimSuffix(path, suffix))
		} else {
			fmt.Fprintf(&b, "    let path = %q;\n", path)
		}
		for _, mapping := range namedChildren(httpSpec, "path_parameter") {
			fmt.Fprintf(&b, "    path = path.replace(%q, Runtime.encodeRFC3986(Runtime.encodeHTTPValue(input.%s, %s, typeRegistry)));\n", "{"+stringValue(mapping["name"])+"}", tsInputTargetProperty(mapping["to"]), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
		}
		if len(pathTails) == 1 {
			mapping := pathTails[0]
			fmt.Fprintf(&b, "    path = Runtime.appendPathTail(path, input.%s, %s, typeRegistry);\n", tsInputTargetProperty(mapping["to"]), tsDescriptorLiteral(tsOperationFieldType(op, resources, mapping["to"]), op.Module))
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
		if resource.Kind == "scenery.http-gateway" && resource.Name == gatewayName {
			base = stringValue(resource.Spec["base_path"])
			break
		}
	}
	if base == "" || base == "/" {
		return path
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}
