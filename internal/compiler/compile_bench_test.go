package compiler

import (
	"path/filepath"
	"testing"
)

func BenchmarkCompileFixtures(b *testing.B) {
	for _, name := range []string{"native", "house"} {
		b.Run(name, func(b *testing.B) {
			root := filepath.Join("testdata", name)
			compile := func() {
				result, err := Compile(root)
				if err != nil {
					b.Fatal(err)
				}
				if result.Manifest == nil || hasErrors(result.Diagnostics) {
					b.Fatalf("fixture compilation failed: %v", result.Diagnostics)
				}
			}
			// Initialize immutable catalogs before measuring fresh compilations.
			compile()
			b.ReportAllocs()
			for b.Loop() {
				compile()
			}
		})
	}
}
