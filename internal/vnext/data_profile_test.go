package vnext

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestFixtureSeedPlanIsEnvironmentScopedAndDeterministic(t *testing.T) {
	resources := dataProfileFixtureResources()
	resources = append(resources, Resource{Address: "house/fixture/demo_scenes", Module: "house", Name: "demo_scenes", Kind: "scenery.fixture/v1", Spec: map[string]any{
		"entity": map[string]any{"$ref": "entity.scene"}, "environments": []any{"development"}, "mode": "upsert",
		"values": []any{map[string]any{"id": map[string]any{"$scalar": "uuid", "value": "01900000-0000-7000-8000-000000000001"}, "tenant_id": "demo", "name": "O'Brien"}},
	}})
	result := &Result{ContractStatus: "valid", Manifest: &Manifest{Resources: resources}}
	plans, err := BuildFixtureSeedPlans(result, "development")
	if err != nil {
		t.Fatal(err)
	}
	if len(plans) != 1 || plans[0].Database != "database" || plans[0].SHA256 == "" {
		t.Fatalf("plans = %#v", plans)
	}
	for _, fragment := range []string{`INSERT INTO "scenes"`, `'O''Brien'`, `ON CONFLICT ("id") DO UPDATE SET`, `"tenant_id" = EXCLUDED."tenant_id"`} {
		if !strings.Contains(plans[0].SQL, fragment) {
			t.Errorf("fixture SQL missing %q:\n%s", fragment, plans[0].SQL)
		}
	}
	if preview, err := BuildFixtureSeedPlans(result, "preview"); err != nil || len(preview) != 0 {
		t.Fatalf("preview plans = %#v, %v", preview, err)
	}
}

func TestCRUDExpansionProducesOrdinaryStableResourcesWithLineage(t *testing.T) {
	resources := dataProfileFixtureResources()
	expanded, diagnostics := expandDataResources(resources)
	if hasErrors(diagnostics) {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	for _, address := range []string{
		"house/service/scene_api_data", "house/record/scene_api_get_input", "house/record/scene_api_create_input",
		"house/operation/scene_api_get", "house/execution/scene_api_get_direct", "house/binding/scene_api_get_http",
		"house/operation/scene_api_create", "house/execution/scene_api_create_direct", "house/binding/scene_api_create_http",
	} {
		resource, ok := resourcesByAddress(&Manifest{Resources: expanded})[address]
		if !ok {
			t.Errorf("missing expanded resource %s", address)
			continue
		}
		if resource.Origin.Kind != "expanded" || len(resource.Origin.ExpansionLineage) != 1 || resource.Origin.ExpansionLineage[0].Generator != "house/crud/scene_api" {
			t.Errorf("origin for %s = %#v", address, resource.Origin)
		}
		for path, field := range resource.Origin.FieldProvenance {
			if field.Kind != "expansion" || field.ProvidedBy != "house/crud/scene_api" || path == "" {
				t.Errorf("expanded field provenance for %s at %s = %#v", address, path, field)
			}
		}
		if len(resource.Origin.FieldProvenance) == 0 {
			t.Errorf("expanded field provenance for %s is empty", address)
		}
	}
	getInput := resourcesByAddress(&Manifest{Resources: expanded})["house/record/scene_api_get_input"]
	fields := namedChildren(getInput.Spec, "field")
	if len(fields) != 2 || fields[0]["name"] != "id" || fields[1]["name"] != "tenant_id" {
		t.Fatalf("tenant-scoped get input fields = %#v", fields)
	}
	getOperation := resourcesByAddress(&Manifest{Resources: expanded})["house/operation/scene_api_get"]
	resultName := stringValue(namedChildren(getOperation.Spec, "result")[0]["name"])
	if _, ok := getOperation.Origin.FieldProvenance["/spec/result/type"]; !ok {
		t.Fatalf("result provenance paths = %#v", getOperation.Origin.FieldProvenance)
	}
	if _, wrong := getOperation.Origin.FieldProvenance["/spec/result/"+resultName+"/type"]; wrong {
		t.Fatalf("provenance path is not an RFC 6901 pointer: %#v", getOperation.Origin.FieldProvenance)
	}
	getBinding := resourcesByAddress(&Manifest{Resources: expanded})["house/binding/scene_api_get_http"]
	contexts := namedChildren(getBinding.Spec["http"].(map[string]any), "context")
	if len(contexts) != 1 || refOrString(contexts[0]["from"]) != "principal.tenant_id" {
		t.Fatalf("tenant context mapping = %#v", contexts)
	}
}

func TestSQLViewResultColumnsAreVerified(t *testing.T) {
	root := t.TempDir()
	queryDir := filepath.Join(root, "house", "queries")
	if err := os.MkdirAll(queryDir, 0o755); err != nil {
		t.Fatal(err)
	}
	queryPath := filepath.Join(queryDir, "scenes.sql")
	if err := os.WriteFile(queryPath, []byte("-- name: ListScenes :many\nSELECT id, tenant_id, name\nFROM scenes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	resources := dataProfileFixtureResources()
	resources = append(resources,
		Resource{Address: "app/module/house", Module: "app", Name: "house", Kind: "scenery.module/v1", Spec: map[string]any{"source": "./house", "workspace_package_root": "house"}},
		Resource{Address: "house/view/scenes", Module: "house", Name: "scenes", Kind: "scenery.view/v1", Spec: map[string]any{"data_source": map[string]any{"$ref": "app/data_source/database"}, "input": map[string]any{"$ref": "record.scene_row"}, "result": map[string]any{"$expression": "list(record.scene_row)"}, "implementation": map[string]any{"kind": "sql_query", "file": "queries/scenes.sql", "name": "ListScenes"}}},
	)
	view := resources[len(resources)-1]
	if diagnostics := validateViewSemantics(root, resourcesByAddress(&Manifest{Resources: resources}), view); hasErrors(diagnostics) {
		t.Fatalf("valid SQL projection diagnostics = %#v", diagnostics)
	}
	if err := os.WriteFile(queryPath, []byte("-- name: ListScenes :many\nSELECT id, name\nFROM scenes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if diagnostics := validateViewSemantics(root, resourcesByAddress(&Manifest{Resources: resources}), view); !hasDiagnostic(diagnostics, "SCN2509") {
		t.Fatalf("mismatched SQL projection diagnostics = %#v", diagnostics)
	}
}

func TestCRUDExpansionIsIndependentOfActionOrder(t *testing.T) {
	left := dataProfileFixtureResources()
	right := dataProfileFixtureResources()
	left[len(left)-1].Spec["actions"] = []any{"get", "create"}
	right[len(right)-1].Spec["actions"] = []any{"create", "get"}
	leftExpanded, leftDiagnostics := expandDataResources(left)
	rightExpanded, rightDiagnostics := expandDataResources(right)
	if hasErrors(leftDiagnostics) || hasErrors(rightDiagnostics) {
		t.Fatalf("left=%#v right=%#v", leftDiagnostics, rightDiagnostics)
	}
	leftBytes, _ := canonicalResources(leftExpanded)
	rightBytes, _ := canonicalResources(rightExpanded)
	if !reflect.DeepEqual(leftBytes, rightBytes) {
		t.Fatalf("CRUD action permutation changed expansion\nleft=%s\nright=%s", leftBytes, rightBytes)
	}
}

func TestDataValidationRejectsInvalidEntityMappingAndProductionFixture(t *testing.T) {
	resources := dataProfileFixtureResources()
	for index := range resources {
		if resources[index].Kind == "scenery.entity/v1" {
			resources[index].Spec["field"] = map[string]any{"name": "missing", "column": "missing", "primary_key": true}
		}
	}
	resources = append(resources, Resource{Address: "house/fixture/scenes", Module: "house", Name: "scenes", Kind: "scenery.fixture/v1", Spec: map[string]any{
		"entity": map[string]any{"$ref": "entity.scene"}, "environments": []any{"production"}, "mode": "upsert", "values": []any{map[string]any{"id": "one"}},
	}})
	diagnostics := validateDataSemantics("", resources)
	for _, code := range []string{"SCN2506", "SCN2508"} {
		if !hasDiagnostic(diagnostics, code) {
			t.Errorf("missing %s in %#v", code, diagnostics)
		}
	}
}

func TestCRUDExpansionRejectsDerivedAddressCollision(t *testing.T) {
	resources := dataProfileFixtureResources()
	resources = append(resources, Resource{Address: "house/operation/scene_api_get", Module: "house", Name: "scene_api_get", Kind: "scenery.operation/v1", Origin: Origin{Kind: "authored"}})
	_, diagnostics := expandDataResources(resources)
	if !hasDiagnostic(diagnostics, "SCN2510") {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
}

func dataProfileFixtureResources() []Resource {
	return []Resource{
		{Address: "app/provider/postgres", Module: "app", Name: "postgres", Kind: "scenery.provider/v1", Spec: map[string]any{"source": "registry.scenery.dev/core/postgres", "version": ">= 2.1.0, < 3.0.0"}},
		{Address: "app/data_source/database", Module: "app", Name: "database", Kind: "scenery.data-source/v1", Spec: map[string]any{"provider": map[string]any{"$ref": "provider.postgres"}, "lifecycle": "managed", "require_capabilities": []any{"sql.query/v1"}}},
		{Address: "house/record/scene_row", Module: "house", Name: "scene_row", Kind: "scenery.record/v1", Spec: map[string]any{"field": []any{
			map[string]any{"name": "id", "type": map[string]any{"$ref": "uuid"}},
			map[string]any{"name": "tenant_id", "type": map[string]any{"$ref": "string"}},
			map[string]any{"name": "name", "type": map[string]any{"$ref": "string"}},
		}}},
		{Address: "house/entity/scene", Module: "house", Name: "scene", Kind: "scenery.entity/v1", Spec: map[string]any{
			"type": map[string]any{"$ref": "record.scene_row"}, "data_source": map[string]any{"$ref": "app/data_source/database"}, "mapping": map[string]any{"relation": "scenes"},
			"field": []any{
				map[string]any{"name": "id", "column": "id", "primary_key": true, "default": map[string]any{"strategy": "uuid_v7"}},
				map[string]any{"name": "tenant_id", "column": "tenant_id", "tenant_key": true, "immutable": true},
				map[string]any{"name": "name", "column": "name"},
			},
		}},
		{Address: "house/crud/scene_api", Module: "house", Name: "scene_api", Kind: "scenery.crud/v1", Origin: Origin{Kind: "authored", SourceID: "src_house"}, Spec: map[string]any{
			"entity": map[string]any{"$ref": "entity.scene"}, "implementation": map[string]any{"$ref": "std.crud.entity"}, "actions": []any{"get", "create"}, "execution": map[string]any{"mode": "direct", "timeout": "15s"},
			"http": map[string]any{"path": "/house/scenes", "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"}, "gateway": map[string]any{"$ref": "app/http_gateway/public"}, "authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"}},
		}},
	}
}
