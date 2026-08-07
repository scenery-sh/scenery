package evolution

import (
	"sort"
	"strings"
)

func semanticRisk(change SemanticChange) map[string]any {
	security, hasSecurity := change.Classifications["security"]
	kind := ""
	if hasSecurity && security.Applicable && oneOf(security.Relation, SecurityWeaker, SecurityIncomparable, SecurityUnknown) {
		kind = "security_" + security.Relation
	} else if change.ExpectedKind == "scenery.entity" && strongestClassification(change.Classifications) != CompatibilityCompatible {
		kind = "storage_change"
	} else if change.ExpectedKind == "scenery.execution" && strongestClassification(change.Classifications) == CompatibilityMigrationRequired {
		kind = "durable_migration"
	}
	if kind == "" {
		return nil
	}
	return map[string]any{"risk_id": "risk_" + strings.TrimPrefix(change.ChangeID, "chg_"), "kind": kind, "address": change.Address, "path": change.Path, "requires_approval": true, "comparison_change_id": change.ChangeID}
}

func affectedArtifacts(before, after *Resource, path string) []string {
	resource := after
	if resource == nil {
		resource = before
	}
	if resource == nil {
		return nil
	}
	set := map[string]bool{}
	kind := canonicalResourceKind(resource.Kind)
	if kind == "binding" && isMCPBinding(*resource) {
		set["mcp_capability_revision[*]"] = true
	}
	if kind == "mcp-server" {
		set["mcp_capability_revision[*]"] = true
		if strings.HasPrefix(path, "/spec/capability/") || strings.HasPrefix(path, "/spec/connection/") || strings.HasPrefix(path, "/spec/max_") {
			set["mcp_capability_revision[*]"] = true
		}
	}
	if kind == "mcp-connection" {
		set["implementation_revision[*]"] = true
		set["assistant_readiness[*]"] = true
		if isMCPConnectionImplementationPath(path) || strings.HasPrefix(path, "/spec/tools/") {
			set["implementation_revision[*]"] = true
			set["assistant_readiness[*]"] = true
		}
	}
	if kind == "assistant" {
		if path == "" || strings.HasPrefix(path, "/spec/surface/") || path == "/spec/mcp_server" {
			set["assistant_public_revision["+resource.Name+"]"] = true
		}
		if isAssistantImplementationPath(path) {
			set["implementation_revision[*]"] = true
		}
		if strings.HasPrefix(path, "/spec/surface/") || path == "/spec/mcp_server" {
			set["assistant_public_revision["+resource.Name+"]"] = true
		}
	}
	switch resource.Kind {
	case "scenery.binding":
		if isMCPBinding(*resource) {
			break
		}
		gateway := "*"
		if ref := refString(resource.Spec["gateway"]); ref != "" {
			parts := strings.Split(ref, ".")
			gateway = parts[len(parts)-1]
		}
		set["typescript_client_revision["+gateway+"]"] = true
		set["openapi_revision["+gateway+"]"] = true
		set["http_surface_revision["+gateway+"]"] = true
	case "scenery.http-gateway", "scenery.record", "scenery.enum", "scenery.union", "scenery.operation":
		gateway := "*"
		if ref := refString(resource.Spec["gateway"]); ref != "" {
			parts := strings.Split(ref, ".")
			gateway = parts[len(parts)-1]
		}
		set["typescript_client_revision["+gateway+"]"] = true
		set["openapi_revision["+gateway+"]"] = true
		set["http_surface_revision["+gateway+"]"] = true
	}
	if kind == "assistant" && (strings.HasPrefix(path, "/spec/surface/") || path == "/spec/mcp_server") {
		gateway := assistantSurfaceGateway(*resource)
		set["typescript_client_revision["+gateway+"]"] = true
		set["openapi_revision["+gateway+"]"] = true
		set["http_surface_revision["+gateway+"]"] = true
	}
	if resource.Kind == "scenery.service" || resource.Kind == "scenery.operation" || strings.Contains(path, "/handler") {
		set["implementation_revision[*]"] = true
	}
	if resource.Kind == "scenery.deployment" || resource.Kind == "scenery.go-target" {
		set["deployment_revision[*]"] = true
	}
	result := make([]string, 0, len(set))
	for item := range set {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func assistantSurfaceGateway(resource Resource) string {
	surface, _ := resource.Spec["surface"].(map[string]any)
	if ref := refString(surface["gateway"]); ref != "" {
		parts := strings.Split(ref, ".")
		return parts[len(parts)-1]
	}
	if name := stringValue(surface["gateway"]); name != "" {
		parts := strings.Split(name, ".")
		return parts[len(parts)-1]
	}
	return "*"
}
