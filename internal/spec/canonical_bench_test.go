package spec

import (
	"fmt"
	"strings"
	"testing"
)

func BenchmarkMarshalCanonicalObjectKeys(b *testing.B) {
	for _, size := range []int{8, 128, 1024} {
		for _, alphabet := range []struct {
			name     string
			prefixes []string
		}{
			{"ascii", []string{"alpha", "beta"}},
			{"unicode", []string{"číslo", "jméno"}},
			{"supplementary", []string{"key", "\U00010000", "\uE000", "🙂"}},
		} {
			b.Run(fmt.Sprintf("members=%d/%s", size, alphabet.name), func(b *testing.B) {
				value := make(map[string]string, size)
				for index := range size {
					key := fmt.Sprintf("%s%04d", alphabet.prefixes[index%len(alphabet.prefixes)], index)
					value[key] = "representative value"
				}
				b.ReportAllocs()
				for b.Loop() {
					if _, err := MarshalCanonical(value); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}

func BenchmarkMarshalCanonicalStrings(b *testing.B) {
	for _, test := range []struct {
		name  string
		value string
	}{
		{"ascii", strings.Repeat("representative value ", 16)},
		{"unicode", strings.Repeat("žluťoučký kůň 🙂 ", 16)},
		{"separators", strings.Repeat("line\u2028paragraph\u2029", 16)},
		{"escaped", strings.Repeat("<tag>\\value\n", 16)},
	} {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := MarshalCanonical(test.value); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
