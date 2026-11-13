package generate

import (
	"reflect"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestPresentationMetadataPreservesSQL(t *testing.T) {
	t.Parallel()
	ref := func(address string) any { return map[string]any{"$ref": address} }
	resources := []Resource{
		{Address: "app/data_source/database", Kind: "scenery.data-source", Name: "database", Module: "app", Spec: map[string]any{"provider": ref("app/provider/postgres"), "lifecycle": "managed", "require_capabilities": []any{"sql.query/v1"}, "config": map[string]any{"database": "items"}}},
		{Address: "items/service/api", Kind: "scenery.service", Name: "api", Module: "items", Spec: map[string]any{"implementation": map[string]any{"constructor": "New"}, "dependency": map[string]any{"name": "database", "instance": ref("app/data_source/database")}}},
		{Address: "items/record/item", Kind: "scenery.record", Name: "item", Module: "items", Spec: map[string]any{"field": map[string]any{"name": "id", "type": ref("uuid")}}},
		{Address: "items/operation/get", Kind: "scenery.operation", Name: "get", Module: "items", Spec: map[string]any{"service": ref("items/service/api"), "input": ref("std.type.unit"), "handler": map[string]any{"method": "Get"}, "result": map[string]any{"name": "success", "type": ref("std.type.unit")}}},
		{Address: "items/content_page/summary", Kind: "scenery.content-page", Name: "summary", Module: "items", Spec: map[string]any{"title": "Items"}},
	}
	project := func(resources []Resource) compiler.SQLRequirements {
		t.Helper()
		sql, diagnostics := compiler.ProjectSQLRequirements(resources, false)
		if len(diagnostics) != 0 {
			t.Fatalf("SQL projection: %v", diagnostics)
		}
		return sql
	}
	beforeSQL := project(resources)
	resources[4].Spec = map[string]any{"title": "Renamed items", "aria_label": "Item summary", "layout": "full"}
	if afterSQL := project(resources); !reflect.DeepEqual(beforeSQL, afterSQL) {
		t.Fatal("presentation metadata changed SQL")
	}
	if withoutPagesSQL := project(resources[:4]); !reflect.DeepEqual(beforeSQL, withoutPagesSQL) {
		t.Fatal("backend SQL depends on page presence")
	}
}
