package graph

import (
	"fmt"
	"strings"
	"testing"

	"scenery.sh/internal/spec"
)

func BenchmarkContractRevision(b *testing.B) {
	_ = spec.CurrentRevision()
	for _, size := range []int{8, 128, 1024} {
		b.Run(fmt.Sprintf("resources=%d", size), func(b *testing.B) {
			resources := make([]Resource, size)
			for index := range resources {
				name := fmt.Sprintf("item%04d", index)
				resources[index] = Resource{
					Address: "bench/record/" + name, Kind: "scenery.record", Name: name, Module: "bench",
					Spec: map[string]any{"unknown_fields": "reject", "field": []any{
						map[string]any{"name": "label", "type": "string"},
						map[string]any{"name": "quantity", "type": "int"},
					}},
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ContractRevision(resources, "bench"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkRevisionHash(b *testing.B) {
	for _, size := range []int{16, 4096, 65536} {
		b.Run(fmt.Sprintf("bytes=%d", size), func(b *testing.B) {
			value := map[string]any{"payload": strings.Repeat("x", size)}
			b.ReportAllocs()
			for b.Loop() {
				if RevisionHash("scenery.benchmark\x00", value) == "" {
					b.Fatal("empty revision")
				}
			}
		})
	}
}

func BenchmarkContextToken(b *testing.B) {
	payload := ContextToken{WorkspaceRevision: "sha256:" + strings.Repeat("a", 64), ContractRevision: "sha256:" + strings.Repeat("b", 64), QueryDigest: "query", Offset: 128, ExpiresUnix: 1_800_000_000}
	token := makeContextToken(payload)
	b.Run("encode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			_ = makeContextToken(payload)
		}
	})
	b.Run("decode", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := ParseContextToken(token); err != nil {
				b.Fatal(err)
			}
		}
	})
}
