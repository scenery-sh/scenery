package generate

import (
	"bytes"
	"reflect"
	"testing"

	"scenery.sh/internal/compiler"
)

func TestPresentationMetadataPreservesSQLAndBackendCRUD(t *testing.T) {
	t.Parallel()
	ref := func(address string) any { return map[string]any{"$ref": address} }
	resources := []Resource{
		{Address: "app/data_source/database", Kind: "scenery.data-source", Name: "database", Module: "app", Spec: map[string]any{"provider": ref("app/provider/postgres"), "lifecycle": "managed", "require_capabilities": []any{"sql.query/v1"}, "config": map[string]any{"database": "items"}}},
		{Address: "items/service/api", Kind: "scenery.service", Name: "api", Module: "items", Spec: map[string]any{"implementation": map[string]any{"constructor": "New"}, "dependency": map[string]any{"name": "database", "instance": ref("app/data_source/database")}}},
		{Address: "items/record/item", Kind: "scenery.record", Name: "item", Module: "items", Spec: map[string]any{"field": map[string]any{"name": "id", "type": ref("uuid")}}},
		{Address: "items/entity/item", Kind: "scenery.entity", Name: "item", Module: "items", Spec: map[string]any{"type": ref("items/record/item"), "data_source": ref("app/data_source/database"), "mapping": map[string]any{"relation": "items"}, "field": map[string]any{"name": "id", "column": "id", "primary_key": true}}},
		{Address: "items/operation/get", Kind: "scenery.operation", Name: "get", Module: "items", Spec: map[string]any{"service": ref("items/service/api"), "input": ref("std.type.unit"), "handler": map[string]any{"method": "Get", "entity": ref("items/entity/item"), "action": "read"}, "result": map[string]any{"name": "success", "type": ref("std.type.unit")}}},
		{Address: "items/content_page/summary", Kind: "scenery.content-page", Name: "summary", Module: "items", Spec: map[string]any{"title": "Items"}},
	}
	project := func(resources []Resource) (compiler.SQLRequirements, []byte) {
		t.Helper()
		sql, diagnostics := compiler.ProjectSQLRequirements(resources, false)
		if len(diagnostics) != 0 {
			t.Fatalf("SQL projection: %v", diagnostics)
		}
		// Identity is fixed deliberately: canonical graph revision still includes
		// pages; this assertion compares the backend behavior and contracts.
		adapter, err := renderProviderCRUDAdapterSource("identity", "items", "abi", "example.test/items/contract", "generated", resources[1], resources[4:5], nil, nil, resources, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		return sql, adapter
	}
	beforeSQL, beforeGo := project(resources)
	resources[5].Spec = map[string]any{"title": "Renamed items", "aria_label": "Item summary", "layout": "full"}
	afterSQL, afterGo := project(resources)
	if !reflect.DeepEqual(beforeSQL, afterSQL) || !bytes.Equal(beforeGo, afterGo) {
		t.Fatal("presentation metadata changed SQL or backend CRUD")
	}
	withoutPagesSQL, withoutPagesGo := project(resources[:5])
	if !reflect.DeepEqual(beforeSQL, withoutPagesSQL) || !bytes.Equal(beforeGo, withoutPagesGo) {
		t.Fatal("backend CRUD depends on page presence")
	}
}
