package runtime

import (
	"bytes"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func BenchmarkResponseCompression(b *testing.B) {
	for _, size := range []int{256, 4096, 65536} {
		payload := []byte(strings.Repeat("Scenery response content. ", size/25+1))[:size]
		for _, encoding := range []string{"identity", "gzip"} {
			b.Run(fmt.Sprintf("bytes=%d/encoding=%s", size, encoding), func(b *testing.B) {
				request := httptest.NewRequest(http.MethodGet, "/", nil)
				request.Header.Set("Accept-Encoding", encoding)
				options := ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}}
				b.Run("buffered", func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						if _, err := EncodeContractRepresentationWithOptions(request, http.StatusOK, payload, "bytes", []string{"application/octet-stream"}, options); err != nil {
							b.Fatal(err)
						}
					}
				})
				b.Run("stream", func(b *testing.B) {
					b.ReportAllocs()
					for b.Loop() {
						stream := NewContractByteStream(io.NopCloser(bytes.NewReader(payload)), int64(len(payload)))
						response := ContractHTTPResponse{Stream: &stream, StreamEncoding: encoding}
						var output bytes.Buffer
						if err := writeContractByteStream(&output, response); err != nil {
							b.Fatal(err)
						}
					}
				})
				b.Run("middleware", func(b *testing.B) {
					handler := withGzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
						_, _ = w.Write(payload)
					}))
					b.ReportAllocs()
					for b.Loop() {
						handler.ServeHTTP(httptest.NewRecorder(), request)
					}
				})
			})
		}
	}
}

func BenchmarkConcurrentResponseCompression(b *testing.B) {
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
				request := httptest.NewRequest(http.MethodGet, "/", nil)
				request.Header.Set("Accept-Encoding", "gzip")
				options := ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}}
				b.ReportAllocs()
				b.ResetTimer()
				b.RunParallel(func(pb *testing.PB) {
					for pb.Next() {
						if _, err := EncodeContractRepresentationWithOptions(request, http.StatusOK, payload, "bytes", []string{"application/octet-stream"}, options); err != nil {
							b.Error(err)
							return
						}
					}
				})
			})
		}
	}
}
