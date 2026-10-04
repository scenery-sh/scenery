package runtime

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

func TestResponseCompressionConcurrentIsolation(t *testing.T) {
	const workers = 8
	errors := make(chan error, workers)
	var group sync.WaitGroup
	for worker := range workers {
		group.Go(func() {
			errors <- checkResponseCompressionIsolation(worker)
		})
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Error(err)
		}
	}
}

func checkResponseCompressionIsolation(worker int) error {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	type savedResponse struct{ compressed, expected []byte }
	var saved []savedResponse
	for turn := range 3 {
		payload := []byte(strings.Repeat(fmt.Sprintf("worker=%d turn=%d <different content>\n", worker, turn), 20+turn))
		response, err := EncodeContractRepresentationWithOptions(request, http.StatusOK, payload, "bytes", []string{"application/octet-stream"}, ContractResponseOptions{CompressionAlgorithms: []string{"gzip"}})
		if err != nil {
			return err
		}
		saved = append(saved, savedResponse{response.Body, payload})

		stream := NewContractByteStream(io.NopCloser(bytes.NewReader(payload)), int64(len(payload)))
		var streamed bytes.Buffer
		if err := writeContractByteStream(&streamed, ContractHTTPResponse{Stream: &stream, StreamEncoding: "gzip"}); err != nil {
			return err
		}
		saved = append(saved, savedResponse{streamed.Bytes(), payload})

		recorder := httptest.NewRecorder()
		withGzip(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write(payload[:len(payload)/2])
			w.(http.Flusher).Flush()
			_, _ = w.Write(payload[len(payload)/2:])
		})).ServeHTTP(recorder, request)
		saved = append(saved, savedResponse{recorder.Body.Bytes(), payload})
	}
	// Keep previous response buffers alive while later requests reuse compressor
	// state. Each stream must contain only its own bytes and a valid trailer.
	for _, response := range saved {
		reader, err := gzip.NewReader(bytes.NewReader(response.compressed))
		if err != nil {
			return err
		}
		body, err := io.ReadAll(reader)
		_ = reader.Close()
		if err != nil {
			return err
		}
		if !bytes.Equal(body, response.expected) {
			return fmt.Errorf("worker %d received another response's compression state", worker)
		}
	}
	return nil
}
