package vnext

import (
	"sort"
	"strings"
)

func semanticRisk(change SemanticChange) map[string]any {
	security, hasSecurity := change.Classifications["security"]
	kind := ""
	if hasSecurity && security.Applicable && oneOf(security.Relation, SecurityWeaker, SecurityIncomparable, SecurityUnknown) {
		kind = "security_" + security.Relation
	} else if change.ExpectedKind == "scenery.entity/v1" && strongestClassification(change.Classifications) != CompatibilityCompatible {
		kind = "storage_change"
	} else if change.ExpectedKind == "scenery.execution/v1" && strongestClassification(change.Classifications) == CompatibilityMigrationRequired {
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
	case "scenery.binding/v1", "scenery.http-gateway/v1", "scenery.record/v1", "scenery.enum/v1", "scenery.union/v1", "scenery.operation/v1":
		gateway := "*"
		if ref := refString(resource.Spec["gateway"]); ref != "" {
			parts := strings.Split(ref, ".")
			gateway = parts[len(parts)-1]
		}
		set["typescript_client_revision["+gateway+"]"] = true
		set["openapi_revision["+gateway+"]"] = true
		set["http_surface_revision["+gateway+"]"] = true
	}
	if resource.Kind == "scenery.service/v1" || resource.Kind == "scenery.operation/v1" || strings.Contains(path, "/handler") {
		set["implementation_revision[*]"] = true
	}
	if resource.Kind == "scenery.deployment/v1" || resource.Kind == "scenery.go-target/v1" {
		set["deployment_revision[*]"] = true
	}
	result := make([]string, 0, len(set))
	for item := range set {
		result = append(result, item)
	}
	sort.Strings(result)
	return result
}

func recommendSemanticVersion(changes []SemanticChange) VersionRecommendation {
	recommendation := VersionRecommendation{Level: "patch"}
	public := map[string]bool{"source": true, "request_wire": true, "response_wire": true, "generated_client": true, "security": true}
	for _, change := range changes {
		for dimension, classification := range change.Classifications {
			if !classification.Applicable {
				continue
			}
			if classification.Result == CompatibilityMigrationRequired && oneOf(dimension, "runtime", "storage", "deployment") {
				recommendation.Migration = true
			}
			if public[dimension] && (classification.Result == CompatibilityBreaking || classification.Result == CompatibilityUnknown) {
				recommendation.Level = "major"
			}
		}
		if recommendation.Level == "patch" && change.Operation == "add" {
			for dimension, classification := range change.Classifications {
				if public[dimension] && classification.Applicable && classification.Result == CompatibilityCompatible {
					recommendation.Level = "minor"
				}
			}
		}
	}
	return recommendation
}
