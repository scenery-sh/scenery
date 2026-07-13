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
	switch resource.Kind {
	case "scenery.binding", "scenery.http-gateway", "scenery.record", "scenery.enum", "scenery.union", "scenery.operation":
		gateway := "*"
		if ref := refString(resource.Spec["gateway"]); ref != "" {
			parts := strings.Split(ref, ".")
			gateway = parts[len(parts)-1]
		}
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
