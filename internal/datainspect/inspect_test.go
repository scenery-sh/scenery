package datainspect

import (
	"encoding/json"
	"testing"
)

func TestColumnNames(t *testing.T) {
	t.Parallel()

	got := columnNames([]byte(`[{"name":"first_name","sql_type":"text"},{"name":"last_name","sql_type":"text"}]`))
	want := []string{"first_name", "last_name"}
	if len(got) != len(want) {
		t.Fatalf("columnNames len = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("columnNames[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestResponseJSONShape(t *testing.T) {
	t.Parallel()

	resp := Response{
		SchemaVersion: schemaVersion,
		Schemas:       Schemas{Metadata: "onlava_data", Records: "onlava_data_records"},
		Tenants: []TenantSummary{{
			ID:              "tenant-id",
			Key:             "tenant_key",
			Name:            "Tenant",
			Objects:         1,
			LatestOutboxSeq: 42,
		}},
		Objects: []ObjectSummary{{
			ID:                    "object-id",
			TenantID:              "tenant-id",
			TenantKey:             "tenant_key",
			Name:                  "company",
			PhysicalTable:         "t_hash_company",
			SchemaVersion:         2,
			OutboxTriggersEnabled: true,
			OutboxTriggerName:     "outbox__abcd1234abcd",
			OutboxTriggerPresent:  true,
			Fields: []FieldSummary{{
				Name:    "name",
				Label:   "Name",
				Type:    "text",
				Columns: []string{"name"},
			}},
		}},
		Migrations: MigrationSummary{Latest: []MigrationRecord{}},
		Outbox:     OutboxSummary{LatestSeq: 42, Unpublished: 0},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded struct {
		SchemaVersion string `json:"schema_version"`
		Schemas       struct {
			Metadata string `json:"metadata"`
			Records  string `json:"records"`
		} `json:"schemas"`
		Tenants []TenantSummary `json:"tenants"`
		Objects []ObjectSummary `json:"objects"`
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.SchemaVersion != "onlava.inspect.data.v1" {
		t.Fatalf("schema_version = %q", decoded.SchemaVersion)
	}
	if decoded.Schemas.Metadata != "onlava_data" || decoded.Schemas.Records != "onlava_data_records" {
		t.Fatalf("schemas = %+v", decoded.Schemas)
	}
	if len(decoded.Tenants) != 1 || len(decoded.Objects) != 1 || len(decoded.Objects[0].Fields) != 1 {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestResponseJSONUsesEmptyArrays(t *testing.T) {
	t.Parallel()

	resp := Response{
		SchemaVersion: schemaVersion,
		Schemas:       Schemas{Metadata: "onlava_data", Records: "onlava_data_records"},
		Tenants:       []TenantSummary{},
		Objects:       []ObjectSummary{},
		Migrations:    MigrationSummary{Latest: []MigrationRecord{}},
	}
	data, err := json.Marshal(resp)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if string(data) == "" {
		t.Fatal("empty JSON")
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	for _, key := range []string{"tenants", "objects"} {
		if got, ok := decoded[key].([]any); !ok || len(got) != 0 {
			t.Fatalf("%s = %#v, want empty array", key, decoded[key])
		}
	}
	migrations, _ := decoded["migrations"].(map[string]any)
	if latest, ok := migrations["latest"].([]any); !ok || len(latest) != 0 {
		t.Fatalf("migrations.latest = %#v, want empty array", migrations["latest"])
	}
}
