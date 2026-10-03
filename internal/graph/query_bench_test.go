package graph

import (
	"fmt"
	"testing"
	"time"
)

func BenchmarkGraphQuery(b *testing.B) {
	for _, size := range []int{8, 128, 1024} {
		for _, width := range []int{1, 8} {
			b.Run(fmt.Sprintf("resources=%d/width=%d", size, width), func(b *testing.B) {
				manifest := benchmarkQueryManifest(size, width)
				focus := manifest.Resources[size/2].Address
				options := GraphOptions{Depth: 3, MaxResources: AgentMaxResources}
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := Graph(manifest, focus, options); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkContextQuery(b *testing.B) {
	for _, size := range []int{8, 128, 1024} {
		for _, focuses := range []int{1, 8} {
			b.Run(fmt.Sprintf("resources=%d/focuses=%d", size, focuses), func(b *testing.B) {
				manifest := benchmarkQueryManifest(size, 3)
				options := ContextOptions{Depth: 3, MaxResources: AgentMaxResources, MaxBytes: AgentMaxBytes}
				for i := 0; i < focuses; i++ {
					options.Focus = append(options.Focus, manifest.Resources[i*size/focuses].Address)
				}
				now := time.Unix(1_800_000_000, 0)
				b.ReportAllocs()
				b.ResetTimer()
				for b.Loop() {
					if _, err := ContextAt(manifest, "workspace", nil, options, now); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkContextIncludes(b *testing.B) {
	for _, size := range []int{8, 128, 1024} {
		b.Run(fmt.Sprintf("resources=%d", size), func(b *testing.B) {
			manifest := benchmarkQueryManifest(size, 3)
			options := ContextOptions{
				Include:      []string{"schemas", "diagnostics", "provenance"},
				MaxResources: AgentMaxResources, MaxBytes: AgentMaxBytes,
			}
			for _, resource := range manifest.Resources {
				options.Focus = append(options.Focus, resource.Address)
			}
			now := time.Unix(1_800_000_000, 0)
			b.ReportAllocs()
			for b.Loop() {
				if _, err := ContextAt(manifest, "workspace", nil, options, now); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func benchmarkQueryManifest(size, width int) *Manifest {
	manifest := &Manifest{ContractRevision: "sha256:benchmark", Resources: make([]Resource, size)}
	for i := range manifest.Resources {
		refs := make([]any, 0, width)
		for j := 1; j <= width; j++ {
			refs = append(refs, map[string]any{"$ref": fmt.Sprintf("record.item%04d", (i+j*j)%size)})
		}
		manifest.Resources[i] = Resource{
			Address: fmt.Sprintf("bench/record/item%04d", i), Module: "bench", Kind: "scenery.record",
			Spec: map[string]any{"references": refs, "description": "representative resource"},
		}
	}
	return manifest
}
