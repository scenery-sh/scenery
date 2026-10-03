package runtime

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"testing"
)

func BenchmarkConcurrentStreamCompression(b *testing.B) {
	for _, size := range []int{4096, 65536} {
		for _, random := range []bool{false, true} {
			b.Run(fmt.Sprintf("bytes=%d/random=%t", size, random), func(b *testing.B) {
				payload := bytes.Repeat([]byte("x"), size)
				if random {
					source := rand.New(rand.NewPCG(20, 8))
					for i := range payload {
						payload[i] = byte(source.Uint32())
					}
				}
				b.ReportAllocs()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						stream := NewContractByteStream(io.NopCloser(bytes.NewReader(payload)), int64(len(payload)))
						if err := writeContractByteStream(io.Discard, ContractHTTPResponse{Stream: &stream, StreamEncoding: "gzip"}); err != nil {
							b.Error(err)
							return
						}
					}
				})
			})
		}
	}
}
