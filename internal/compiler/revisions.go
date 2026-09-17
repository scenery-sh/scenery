package compiler

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strings"
	"sync"

	graphmodel "scenery.sh/internal/graph"
	"scenery.sh/internal/spec"
)

func ComputeImplementationRevisions(result *Result, buildInputManifestDigests map[string]string) (map[string]string, []Diagnostic) {
	revisions := map[string]string{}
	if result == nil || result.Manifest == nil {
		return revisions, nil
	}
	targets := goTargetsByName(result.Manifest.Resources)
	byAddress := resourcesByAddress(result.Manifest)
	var diagnostics []Diagnostic
	var adapterDigest string
	for _, name := range sortedResourceNames(targets) {
		inputDigest := buildInputManifestDigests[name]
		if inputDigest == "" {
			continue
		}
		if adapterDigest == "" {
			adapterDigest = generatedApplicationAdapterDigest(result)
		}
		projection, targetDiagnostics := implementationRevisionProjection(result, byAddress, targets, targets[name], adapterDigest)
		diagnostics = append(diagnostics, targetDiagnostics...)
		if projection == nil {
			continue
		}
		if !isCanonicalSHA256Digest(inputDigest) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "build input manifest digest must be canonical sha256", Address: targets[name].Address})
			continue
		}
		projection["build_input_manifest_digest"] = inputDigest
		revisions[name] = revisionHash("scenery.implementation-revision\x00", projection)
	}
	for name := range buildInputManifestDigests {
		if targets[name].Address == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "build input manifest names unknown Go target " + name})
		}
	}
	return revisions, diagnostics
}

// ImplementationRevisionsForInputs computes one Go target's implementation
// revision for each of several build input manifest digests, such as the
// entrypoints of development processes. The contract projection that every
// revision shares is computed and encoded once; each result equals
// ComputeImplementationRevisions for that digest alone.
func ImplementationRevisionsForInputs(result *Result, targetName string, inputDigests []string) (map[string]string, []Diagnostic) {
	revisions := map[string]string{}
	if result == nil || result.Manifest == nil || len(inputDigests) == 0 {
		return revisions, nil
	}
	targets := goTargetsByName(result.Manifest.Resources)
	target := targets[targetName]
	if target.Address == "" {
		return revisions, []Diagnostic{{Code: "SCN6122", Severity: "error", Message: "build input manifest names unknown Go target " + targetName}}
	}
	projection, diagnostics := implementationRevisionProjection(result, resourcesByAddress(result.Manifest), targets, target, generatedApplicationAdapterDigest(result))
	if projection == nil {
		return revisions, diagnostics
	}
	revision := implementationRevisionForDigest(projection)
	for _, inputDigest := range inputDigests {
		if !isCanonicalSHA256Digest(inputDigest) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "build input manifest digest must be canonical sha256", Address: target.Address})
			continue
		}
		revisions[inputDigest] = revision(inputDigest)
	}
	return revisions, diagnostics
}

// ServiceProcess identifies one service process of a Go target for its
// implementation revision.
type ServiceProcess struct {
	Service          Resource
	Covered          []string
	ContractRevision string
	InputDigest      string
}

// ServiceProcessImplementationRevisions computes the implementation revision of
// each service process of a Go target, keyed by service address. A process's
// projection is the target's, bound to its service contract revision instead
// of the application's and to the implementation bindings of its service's
// resources and the application's providers instead of every resource, with
// the process's build input digest. A contract or implementation binding change
// of another service leaves it unchanged.
func ServiceProcessImplementationRevisions(result *Result, targetName string, processes []ServiceProcess) (map[string]string, []Diagnostic) {
	revisions := map[string]string{}
	if result == nil || result.Manifest == nil || len(processes) == 0 {
		return revisions, nil
	}
	targets := goTargetsByName(result.Manifest.Resources)
	target := targets[targetName]
	if target.Address == "" {
		return revisions, []Diagnostic{{Code: "SCN6122", Severity: "error", Message: "build input manifest names unknown Go target " + targetName}}
	}
	base, diagnostics := implementationRevisionTargetProjection(result, resourcesByAddress(result.Manifest), targets, target)
	if base == nil {
		return revisions, diagnostics
	}
	// Every process shares the target's projection, which is hashed once.
	targetProjection := revisionHash("scenery.service-process-target-projection\x00", base)
	var providers []Resource
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.provider" {
			providers = append(providers, resource)
		}
	}
	for _, process := range processes {
		if !isCanonicalSHA256Digest(process.InputDigest) || !isCanonicalSHA256Digest(process.ContractRevision) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6122", Severity: "error", Message: "service process revisions require canonical sha256 digests", Address: process.Service.Address})
			continue
		}
		resources := append(graphmodel.ServiceResources(result.Manifest.Resources, process.Service, process.Covered), providers...)
		sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
		resources = slices.CompactFunc(resources, func(a, b Resource) bool { return a.Address == b.Address })
		revisions[process.Service.Address] = revisionHash("scenery.service-process-implementation-revision\x00", map[string]any{
			"target_projection":           targetProjection,
			"service_contract_revision":   process.ContractRevision,
			"implementation_bindings":     implementationBindings(resources),
			"build_input_manifest_digest": process.InputDigest,
		})
	}
	return revisions, diagnostics
}

// implementationRevisionForDigest returns the implementation revision of a
// projection for a canonical build input manifest digest. A canonical digest
// encodes as an unescaped string of fixed length, so the projection is encoded
// once and each revision hashes that encoding with the digest in place of a
// placeholder. The placeholder's position is where two encodings with
// different placeholders differ, which cannot match any other value.
func implementationRevisionForDigest(projection map[string]any) func(string) string {
	const prefix = "scenery.implementation-revision\x00"
	zeros, ones := "sha256:"+strings.Repeat("0", 64), "sha256:"+strings.Repeat("1", 64)
	projection["build_input_manifest_digest"] = zeros
	first, firstErr := spec.MarshalCanonical(projection)
	projection["build_input_manifest_digest"] = ones
	second, secondErr := spec.MarshalCanonical(projection)
	start, end := 0, len(first)
	if len(first) == len(second) {
		for start < end && first[start] == second[start] {
			start++
		}
		for end > start && first[end-1] == second[end-1] {
			end--
		}
	}
	start -= len("sha256:")
	if firstErr != nil || secondErr != nil || len(first) != len(second) || start < 0 || end-start != len(zeros) ||
		string(first[start:end]) != zeros || string(second[start:end]) != ones {
		return func(inputDigest string) string {
			projection["build_input_manifest_digest"] = inputDigest
			return revisionHash(prefix, projection)
		}
	}
	return func(inputDigest string) string {
		hash := sha256.New()
		_, _ = hash.Write([]byte(prefix))
		_, _ = hash.Write(first[:start])
		_, _ = hash.Write([]byte(inputDigest))
		_, _ = hash.Write(first[end:])
		return "sha256:" + hex.EncodeToString(hash.Sum(nil))
	}
}

func goTargetsByName(resources []Resource) map[string]Resource {
	targets := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind == "scenery.go-target" {
			targets[resource.Name] = resource
		}
	}
	return targets
}

// implementationRevisionProjection returns a target's revision projection
// without its build input digest, or nil for a contract-role target or an
// unresolvable one.
func implementationRevisionProjection(result *Result, byAddress map[string]Resource, targets map[string]Resource, target Resource, adapterDigest string) (map[string]any, []Diagnostic) {
	projection, diagnostics := implementationRevisionTargetProjection(result, byAddress, targets, target)
	if projection == nil {
		return nil, diagnostics
	}
	projection["contract_revision"] = result.Manifest.ContractRevision
	projection["implementation_bindings"] = implementationBindings(result.Manifest.Resources)
	projection["generated_adapter_digest"] = adapterDigest
	return projection, diagnostics
}

// implementationRevisionTargetProjection returns the part of a target's
// revision projection that does not depend on the application contract: the
// specification revision, resolved target, module, toolchain and runtime ABI.
func implementationRevisionTargetProjection(result *Result, byAddress map[string]Resource, targets map[string]Resource, target Resource) (map[string]any, []Diagnostic) {
	effective, err := effectiveGoTarget(target, targets, nil)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN6150", Severity: "error", Message: err.Error(), Address: target.Address}}
	}
	if stringValue(effective["role"]) == "contract" {
		return nil, nil
	}
	moduleRef := resolveResourceRef(target, refString(effective["module"]), "go_module")
	module := byAddress[moduleRef]
	if module.Address == "" {
		return nil, []Diagnostic{{Code: "SCN6151", Severity: "error", Message: "Go target has no resolved module", Address: target.Address}}
	}
	toolchainRef := resolveResourceRef(target, refString(effective["toolchain"]), "go_toolchain")
	toolchain := byAddress[toolchainRef]
	resolvedTarget, err := resolveGoVerificationTarget(result, targets, target)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN6122", Severity: "error", Message: err.Error(), Address: target.Address}}
	}
	effective = resolvedGoTargetContext(effective, toolchain, &resolvedTarget.Context)
	return map[string]any{
		"spec_revision": result.Manifest.SpecRevision,
		"target":        effective,
		"module":        module.Spec,
		"toolchain":     toolchain.Spec,
		"runtime_abi":   "scenery.go-runtime/v1",
	}, nil
}

// serviceContractRevisions retains service contract revisions by application
// contract revision, service and covered addresses. The application contract
// revision hashes the canonical projection of every resource a service
// contract revision projects, so together they determine it.
var serviceContractRevisions struct {
	sync.Mutex
	values map[string]string
}

const serviceContractRevisionLimit = 4096

// ServiceContractRevision identifies the contract a native service's adapter
// implements (graph.ServiceContractRevision).
func ServiceContractRevision(manifest *Manifest, service Resource, covered []string) string {
	if manifest == nil || manifest.ContractRevision == "" {
		return graphmodel.ServiceContractRevision(manifest, service, covered)
	}
	key := manifest.ContractRevision + "\x00" + service.Address + "\x00" + service.Module + "\x00" + strings.Join(covered, "\x00")
	serviceContractRevisions.Lock()
	revision, ok := serviceContractRevisions.values[key]
	serviceContractRevisions.Unlock()
	if ok {
		return revision
	}
	revision = graphmodel.ServiceContractRevision(manifest, service, covered)
	serviceContractRevisions.Lock()
	defer serviceContractRevisions.Unlock()
	if serviceContractRevisions.values == nil || len(serviceContractRevisions.values) >= serviceContractRevisionLimit {
		serviceContractRevisions.values = map[string]string{}
	}
	serviceContractRevisions.values[key] = revision
	return revision
}

// adapterDigests retains the generated adapter digests of the most recent
// contract revisions. A contract revision hashes the same canonical projection
// of an immutable result's resources, so it determines the digest.
var adapterDigests struct {
	sync.Mutex
	values map[string]string
	order  []string
}

const adapterDigestLimit = 8

func generatedApplicationAdapterDigest(result *Result) string {
	revision := result.Manifest.ContractRevision
	if revision == "" {
		return computeGeneratedApplicationAdapterDigest(result)
	}
	adapterDigests.Lock()
	digest, ok := adapterDigests.values[revision]
	adapterDigests.Unlock()
	if ok {
		return digest
	}
	digest = computeGeneratedApplicationAdapterDigest(result)
	adapterDigests.Lock()
	defer adapterDigests.Unlock()
	if _, ok := adapterDigests.values[revision]; !ok {
		if adapterDigests.values == nil {
			adapterDigests.values = map[string]string{}
		}
		if len(adapterDigests.order) >= adapterDigestLimit {
			delete(adapterDigests.values, adapterDigests.order[0])
			adapterDigests.order = adapterDigests.order[1:]
		}
		adapterDigests.values[revision] = digest
		adapterDigests.order = append(adapterDigests.order, revision)
	}
	return digest
}

func computeGeneratedApplicationAdapterDigest(result *Result) string {
	projected := make([]Resource, 0, len(result.Manifest.Resources))
	for _, resource := range result.Manifest.Resources {
		if projection, include := contractResourceProjection(resource); include {
			projected = append(projected, projection)
		}
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Address < projected[j].Address })
	return revisionHash("scenery.generated-adapter\x00", projected)
}

func effectiveGoTarget(target Resource, targets map[string]Resource, stack map[string]bool) (map[string]any, error) {
	if stack == nil {
		stack = map[string]bool{}
	}
	if stack[target.Name] {
		return nil, fmt.Errorf("go target inheritance cycle at %s", target.Name)
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
			return nil, fmt.Errorf("go target %s extends unknown target %s", target.Name, parentRef)
		}
		inherited, err := effectiveGoTarget(parent, targets, stack)
		if err != nil {
			return nil, err
		}
		maps.Copy(effective, inherited)
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
