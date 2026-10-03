package contract

import (
	"fmt"
	"testing"
)

func BenchmarkContractJSONObjectKeys(b *testing.B) {
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
					if _, err := MarshalContractValue(value, "map(string)"); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
