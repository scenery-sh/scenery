package compiler

import (
	"fmt"
	"sort"
)

func ComputeImplementationRevisions(result *Result, buildInputManifestDigests map[string]string) (map[string]string, []Diagnostic) {
	revisions := map[string]string{}
	if result == nil || result.Manifest == nil {
		return revisions, nil
	}
	resources := result.Manifest.Resources
	byAddress := resourcesByAddress(result.Manifest)
	targets := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind == "scenery.go-target" {
			targets[resource.Name] = resource
		}
	}
	var diagnostics []Diagnostic
	for _, name := range sortedResourceNames(targets) {
		inputDigest := buildInputManifestDigests[name]
		if inputDigest == "" {
			continue
		}
		target := targets[name]
		effective, err := effectiveGoTarget(target, targets, nil)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6150", Severity: "error", Message: err.Error(), Address: target.Address})
			continue
		}
		if stringValue(effective["role"]) == "contract" {
			continue
		}
		moduleRef := resolveResourceRef(target, refString(effective["module"]), "go_module")
		module := byAddress[moduleRef]
		if module.Address == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6151", Severity: "error", Message: "Go target has no resolved module", Address: target.Address})
			continue
		}
		if !isCanonicalSHA256Digest(inputDigest) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "build input manifest digest must be canonical sha256", Address: target.Address})
			continue
		}
		toolchainRef := resolveResourceRef(target, refString(effective["toolchain"]), "go_toolchain")
		toolchain := byAddress[toolchainRef]
		resolvedTarget, err := resolveGoVerificationTarget(result, targets, target)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: err.Error(), Address: target.Address})
			continue
		}
		effective = resolvedGoTargetContext(effective, toolchain, &resolvedTarget.Context)
		adapterDigest, err := generatedApplicationAdapterDigest(result)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "generated adapter digest: " + err.Error(), Address: target.Address})
			continue
		}
		projection := map[string]any{
			"spec_revision":               result.Manifest.SpecRevision,
			"contract_revision":           result.Manifest.ContractRevision,
			"implementation_bindings":     implementationBindings(resources),
			"build_input_manifest_digest": inputDigest,
			"generated_adapter_digest":    adapterDigest,
			"target":                      effective,
			"module":                      module.Spec,
			"toolchain":                   toolchain.Spec,
			"runtime_abi":                 "scenery.go-runtime/v1",
		}
		revisions[name] = revisionHash("scenery.implementation-revision\x00", projection)
	}
	for name := range buildInputManifestDigests {
		if targets[name].Address == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "build input manifest names unknown Go target " + name})
		}
	}
	return revisions, diagnostics
}

func generatedApplicationAdapterDigest(result *Result) (string, error) {
	projected := make([]Resource, 0, len(result.Manifest.Resources))
	for _, resource := range result.Manifest.Resources {
		if projection, include := contractResourceProjection(resource); include {
			projected = append(projected, projection)
		}
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Address < projected[j].Address })
	return revisionHash("scenery.generated-adapter\x00", projected), nil
}

func effectiveGoTarget(target Resource, targets map[string]Resource, stack map[string]bool) (map[string]any, error) {
	if stack == nil {
		stack = map[string]bool{}
	}
	if stack[target.Name] {
		return nil, fmt.Errorf("Go target inheritance cycle at %s", target.Name)
	}
	stack[target.Name] = true
	defer delete(stack, target.Name)
	effective := map[string]any{}
	parentRef := refString(target.Spec["extends"])
	if parentRef == "" {
		parentRef = refString(target.Spec["inherits"])
	}
	if parentRef != "" {
		parent := targets[lastRef(parentRef)]
		if parent.Address == "" {
			return nil, fmt.Errorf("Go target %s extends unknown target %s", target.Name, parentRef)
		}
		inherited, err := effectiveGoTarget(parent, targets, stack)
		if err != nil {
			return nil, err
		}
		for key, value := range inherited {
			effective[key] = value
		}
	}
	for key, value := range target.Spec {
		if key != "extends" && key != "inherits" {
			effective[key] = value
		}
	}
	return effective, nil
}

func stringValues(value any) []string {
	items, _ := value.([]any)
	values := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			values = append(values, text)
			continue
		}
		if scalar, ok := item.(map[string]any); ok {
			if text, ok := scalar["value"].(string); ok {
				values = append(values, text)
			}
		}
	}
	return values
}

func implementationBindings(resources []Resource) []map[string]any {
	var projection []map[string]any
	for _, resource := range resources {
		var spec map[string]any
		switch resource.Kind {
		case "scenery.module":
			spec = map[string]any{"locked_integrity": resource.Spec["locked_integrity"], "package_contract_abi_revision": resource.Spec["package_contract_abi_revision"]}
		case "scenery.service":
			spec = map[string]any{"implementation": resource.Spec["implementation"], "dependency": resource.Spec["dependency"], "config": resource.Spec["config"], "client": resource.Spec["client"], "lifecycle": resource.Spec["lifecycle"]}
		case "scenery.operation":
			spec = map[string]any{"handler": resource.Spec["handler"]}
		case "scenery.provider":
			spec = resource.Spec
		case "scenery.view":
			spec = map[string]any{"implementation": resource.Spec["implementation"], "implementation_digest": resource.Spec["implementation_digest"]}
		case "scenery.renderer":
			spec = map[string]any{"runtime": resource.Spec["runtime"], "module": resource.Spec["module"], "config": resource.Spec["config"], "implementation_digest": resource.Spec["implementation_digest"]}
		default:
			continue
		}
		projection = append(projection, map[string]any{"address": resource.Address, "kind": resource.Kind, "spec": spec})
	}
	return projection
}

func sortedResourceNames(resources map[string]Resource) []string {
	names := make([]string, 0, len(resources))
	for name := range resources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func computeDeploymentRevisions(manifest *Manifest, implementationRevisions map[string]string, providerPlanDigests map[string][]string) map[string]string {
	revisions := map[string]string{}
	if manifest == nil || len(implementationRevisions) == 0 {
		return revisions
	}
	for _, resource := range manifest.Resources {
		if resource.Kind != "scenery.deployment" {
			continue
		}
		planDigests := append([]string(nil), providerPlanDigests[resource.Name]...)
		if len(planDigests) == 0 {
			planDigests = append(planDigests, providerPlanDigests[resource.Address]...)
		}
		if len(planDigests) == 0 {
			continue
		}
		validPlans := true
		for _, digest := range planDigests {
			if !isCanonicalSHA256Digest(digest) {
				validPlans = false
				break
			}
		}
		if !validPlans {
			continue
		}
		sort.Strings(planDigests)
		resolved, diagnostics := ResolveDeployment(manifest, resource.Address)
		if hasErrors(diagnostics) {
			continue
		}
		projection := map[string]any{
			"spec_revision":           manifest.SpecRevision,
			"contract_revision":       manifest.ContractRevision,
			"implementation_revision": implementationRevisions,
			"deployment_address":      resource.Address,
			"deployment_values":       resolved,
			"target_platform":         deploymentTargetPlatformIdentity(resource),
			"provider_plan_digests":   planDigests,
		}
		revisions[resource.Name] = revisionHash("scenery.deployment-revision\x00", projection)
	}
	return revisions
}

func ComputeDeploymentRevisions(manifest *Manifest, implementationRevisions map[string]string, providerPlanDigests map[string][]string) map[string]string {
	return computeDeploymentRevisions(manifest, implementationRevisions, providerPlanDigests)
}

func deploymentTargetPlatformIdentity(deployment Resource) map[string]any {
	identity := map[string]any{"environment": deployment.Spec["environment"]}
	for _, field := range []string{"platform", "region", "architecture"} {
		if value := deployment.Spec[field]; value != nil {
			identity[field] = value
		}
	}
	return identity
}
