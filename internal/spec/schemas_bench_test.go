package spec

import "testing"

func BenchmarkSourceSchemaCatalogIndex(b *testing.B) {
	b.ReportAllocs()
	for b.Loop() {
		index := buildSourceSchemaCatalogIndex()
		if len(index.revisions) != len(sourceSchemaIndex.revisions) {
			b.Fatal("source schema catalog changed")
		}
	}
}

func BenchmarkCoreSchema(b *testing.B) {
	for _, kind := range []string{"scenery.record", "scenery.operation", "scenery.http-gateway"} {
		b.Run(kind, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, ok := CoreSchema(kind); !ok {
					b.Fatal("missing core schema", kind)
				}
			}
		})
	}
}

func BenchmarkSchemaRevision(b *testing.B) {
	record, _ := CoreSchema("scenery.record")
	for _, test := range []struct {
		name  string
		value any
	}{
		{"null", nil},
		{"small", map[string]any{"type": "string"}},
		{"record", record},
		{"catalog", Current()},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if SchemaRevision(test.value) == "" {
					b.Fatal("empty schema revision")
				}
			}
		})
	}
}
