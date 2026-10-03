package runtime

import (
	"fmt"
	"testing"
)

func BenchmarkContractEncodingNegotiation(b *testing.B) {
	for _, header := range []string{"", "gzip", "identity", "gzip, identity;q=0.5", "br, zstd, gzip;q=0.8, *;q=0.1", "gzip;q=0, identity;q=0"} {
		b.Run(header, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _ = negotiateContractEncoding(header, []string{"gzip"})
			}
		})
	}
}

func BenchmarkContractMediaNegotiation(b *testing.B) {
	for _, produced := range [][]string{{"application/json"}, {"application/json; charset=utf-8; profile=v1", "text/plain; charset=utf-8"}} {
		b.Run(fmt.Sprintf("types=%d", len(produced)), func(b *testing.B) {
			for _, accept := range []string{"", "application/json", "*/*", "text/html, application/json;q=0.9, */*;q=0.8", "application/json;profile=v1, text/plain;q=0.2"} {
				b.Run(accept, func(b *testing.B) {
					want, wantErr := negotiateContractMedia(accept, produced)
					b.ReportAllocs()
					for b.Loop() {
						got, err := negotiateContractMedia(accept, produced)
						if got != want || (err == nil) != (wantErr == nil) {
							b.Fatalf("negotiation changed: %q %v", got, err)
						}
					}
				})
			}
		})
	}
}
