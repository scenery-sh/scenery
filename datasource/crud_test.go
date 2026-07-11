package datasource

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCRUDWhereRequiresTenantAndPrimaryKeys(t *testing.T) {
	spec := CRUDSpec{Address: "house/crud/scenes", Relation: "scenes", Fields: []CRUDField{
		{Name: "id", Column: "id", PrimaryKey: true},
		{Name: "tenant_id", Column: "tenant_id", TenantKey: true, Immutable: true},
		{Name: "name", Column: "name"},
	}}
	input := map[string]json.RawMessage{"id": json.RawMessage(`"scene-1"`), "tenant_id": json.RawMessage(`"tenant-1"`)}
	where, arguments, err := crudWhere(spec, input, func(field CRUDField) bool { return field.PrimaryKey || field.TenantKey })
	if err != nil {
		t.Fatal(err)
	}
	if where != ` WHERE "id" = $1 AND "tenant_id" = $2` || len(arguments) != 2 || arguments[0] != "scene-1" || arguments[1] != "tenant-1" {
		t.Fatalf("where = %q, args = %#v", where, arguments)
	}
	delete(input, "tenant_id")
	if _, _, err := crudWhere(spec, input, func(field CRUDField) bool { return field.PrimaryKey || field.TenantKey }); err == nil || !strings.Contains(err.Error(), "tenant_id") {
		t.Fatalf("missing tenant error = %v", err)
	}
}

func TestCRUDSpecRejectsIdentifierInjectionAndPreservesExactNumber(t *testing.T) {
	spec := CRUDSpec{Address: "house/crud/scenes", Relation: `scenes; DROP TABLE users`, Fields: []CRUDField{{Name: "id", Column: "id", PrimaryKey: true}}}
	if err := validateCRUDSpec(spec); err == nil {
		t.Fatal("identifier injection was accepted")
	}
	value, err := decodeCRUDSQLValue(json.RawMessage(`123456789012345678901234567890.125`))
	if err != nil {
		t.Fatal(err)
	}
	if value != "123456789012345678901234567890.125" {
		t.Fatalf("exact number = %#v", value)
	}
}

func TestCRUDRelationQuotesDeclaredSchema(t *testing.T) {
	spec := CRUDSpec{Address: "house/crud/scenes", Schema: "tenant_data", Relation: "scenes", Fields: []CRUDField{{Name: "id", Column: "id", PrimaryKey: true}}}
	if err := validateCRUDSpec(spec); err != nil {
		t.Fatal(err)
	}
	if got := crudRelation(spec); got != `"tenant_data"."scenes"` {
		t.Fatalf("crudRelation() = %q", got)
	}
	spec.Schema = `bad; DROP SCHEMA public`
	if err := validateCRUDSpec(spec); err == nil {
		t.Fatal("schema identifier injection was accepted")
	}
}
