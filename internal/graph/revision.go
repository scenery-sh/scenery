package graph

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"scenery.sh/internal/spec"
)

func CanonicalResources(resources []Resource) ([]byte, error) {
	ordered := append([]Resource(nil), resources...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Address < ordered[j].Address })
	return spec.MarshalCanonical(ordered)
}

func ContractRevision(resources []Resource, appName string) (string, error) {
	projected := make([]Resource, 0, len(resources))
	for _, resource := range resources {
		contractResource, include := ContractResourceProjection(resource)
		if include {
			projected = append(projected, contractResource)
		}
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Address < projected[j].Address })
	value := struct {
		SpecRevision string           `json:"spec_revision"`
		Application  string           `json:"application"`
		Dependencies []map[string]any `json:"compile_dependencies"`
		Resources    []Resource       `json:"resources"`
	}{string(spec.CurrentRevision()), appName, dependencyContractIdentities(resources), projected}
	b, err := spec.MarshalCanonical(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(append([]byte("scenery.contract-revision\x00"), b...))
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ContractProjectionHash identifies the canonical contract projection without
// folding in the current specification revision. It is used only to prove a
// revision-scheme rebind; executable artifacts remain bound to ContractRevision.
func ContractProjectionHash(manifest *Manifest) string {
	if manifest == nil {
		return ""
	}
	projected := make([]Resource, 0, len(manifest.Resources))
	for _, resource := range manifest.Resources {
		if value, include := ContractResourceProjection(resource); include {
			projected = append(projected, value)
		}
	}
	sort.Slice(projected, func(i, j int) bool { return projected[i].Address < projected[j].Address })
	return RevisionHash("scenery.contract-projection\x00", struct {
		Application  string           `json:"application"`
		Dependencies []map[string]any `json:"compile_dependencies"`
		Resources    []Resource       `json:"resources"`
	}{manifest.Application.Name, dependencyContractIdentities(manifest.Resources), projected})
}

func ContractResourceProjection(resource Resource) (Resource, bool) {
	schema, ok := spec.ResourceSchemas()[resource.Kind]
	if !ok {
		return Resource{}, false
	}
	projected := Resource{Address: resource.Address, Kind: resource.Kind, Name: resource.Name, Module: resource.Module, Spec: make(map[string]any, len(resource.Spec))}
	for key, value := range resource.Spec {
		if rule, dynamic := spec.DynamicResourceRevisionDomains()[resource.Kind][key]; dynamic {
			if contractValue, include := dynamicContractFieldProjection(resource, key, value, rule); include {
				projected.Spec[key] = contractValue
			}
			continue
		}
		if domain, exists := spec.ResourceFieldRevisionDomain(resource.Kind, key); exists && domain == "contract" {
			projected.Spec[key] = value
		}
	}
	if schema.RevisionDomain != "contract" && len(projected.Spec) == 0 {
		return Resource{}, false
	}
	return projected, true
}

func dynamicContractFieldProjection(resource Resource, field string, value any, rule spec.DynamicRevisionDomain) (any, bool) {
	domains := map[string]string{}
	for _, descriptor := range namedChildren(resource.Spec, rule.SchemaField) {
		domains[stringValue(descriptor[rule.NameField])] = stringValue(descriptor[rule.DomainField])
	}
	if field == rule.SchemaField {
		items, ok := value.([]any)
		if !ok {
			return nil, false
		}
		projected := make([]any, 0, len(items))
		for _, item := range items {
			descriptor, ok := item.(map[string]any)
			if ok && stringValue(descriptor[rule.DomainField]) == "contract" {
				projected = append(projected, item)
			}
		}
		return projected, len(projected) > 0
	}
	values, ok := value.(map[string]any)
	if !ok {
		return nil, false
	}
	projected := map[string]any{}
	for name, item := range values {
		if domains[name] == "contract" {
			projected[name] = item
		}
	}
	return projected, len(projected) > 0
}

func dependencyContractIdentities(resources []Resource) []map[string]any {
	var identities []map[string]any
	for _, resource := range resources {
		if resource.Kind != "scenery.provider" && resource.Kind != "scenery.module" || resource.Spec["compile_descriptor_digest"] == nil {
			continue
		}
		identities = append(identities, map[string]any{
			"kind": strings.TrimPrefix(resource.Kind, "scenery."), "source": resource.Spec["source"],
			"integrity": resource.Spec["locked_integrity"], "compile_descriptor_digest": resource.Spec["compile_descriptor_digest"],
		})
	}
	sort.Slice(identities, func(i, j int) bool {
		if stringValue(identities[i]["kind"]) != stringValue(identities[j]["kind"]) {
			return stringValue(identities[i]["kind"]) < stringValue(identities[j]["kind"])
		}
		return stringValue(identities[i]["source"]) < stringValue(identities[j]["source"])
	})
	return identities
}

func RevisionHash(prefix string, value any) string {
	b, _ := spec.MarshalCanonical(value)
	sum := sha256.Sum256(append([]byte(prefix), b...))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func IsCanonicalSHA256Digest(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 || value != strings.ToLower(value) {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}

func namedChildren(parent map[string]any, name string) []map[string]any {
	switch value := parent[name].(type) {
	case map[string]any:
		return []map[string]any{value}
	case []any:
		result := make([]map[string]any, 0, len(value))
		for _, item := range value {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
