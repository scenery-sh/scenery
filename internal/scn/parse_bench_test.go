package scn

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func BenchmarkParseSource(b *testing.B) {
	for _, blocks := range []int{8, 128, 1024} {
		for _, comments := range []bool{false, true} {
			b.Run(fmt.Sprintf("blocks=%d/comments=%t", blocks, comments), func(b *testing.B) {
				var text strings.Builder
				for i := range blocks {
					if comments {
						fmt.Fprintf(&text, "# Description of item %d\n", i)
					}
					fmt.Fprintf(&text, "record \"item_%04d\" {\n  field \"value\" { type = string }\n}\n", i)
				}
				path := filepath.Join(b.TempDir(), AppFilename)
				if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
					b.Fatal(err)
				}
				b.SetBytes(int64(text.Len()))
				b.ReportAllocs()
				for b.Loop() {
					source, diagnostics := ParseLogical(path, AppFilename)
					if source == nil || hasErrors(diagnostics) {
						b.Fatalf("source parse failed: %v", diagnostics)
					}
				}
			})
		}
	}
}

func BenchmarkParseLongLine(b *testing.B) {
	for _, items := range []int{128, 1024} {
		b.Run(fmt.Sprintf("items=%d", items), func(b *testing.B) {
			text := "enum \"options\" { values = [" + strings.Repeat("\"value\",", items) + "] }\n"
			path := filepath.Join(b.TempDir(), AppFilename)
			if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
				b.Fatal(err)
			}
			b.SetBytes(int64(len(text)))
			b.ReportAllocs()
			for b.Loop() {
				_, diagnostics := ParseLogical(path, AppFilename)
				if hasErrors(diagnostics) {
					b.Fatalf("source parse failed: %v", diagnostics)
				}
			}
		})
	}
}

func BenchmarkParseUnicodeSource(b *testing.B) {
	var text strings.Builder
	for i := range 128 {
		fmt.Fprintf(&text, "# Čeština 🙂 %d\nrecord \"item_%04d\" {\n  field \"value\" { type = string }\n}\n", i, i)
	}
	path := filepath.Join(b.TempDir(), AppFilename)
	if err := os.WriteFile(path, []byte(text.String()), 0o600); err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(text.Len()))
	b.ReportAllocs()
	for b.Loop() {
		_, diagnostics := ParseLogical(path, AppFilename)
		if hasErrors(diagnostics) {
			b.Fatalf("source parse failed: %v", diagnostics)
		}
	}
}
