package runtime

import (
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

type trackingContractReader struct {
	reader *bytes.Reader
	reads  int
	closes int
}

func newTrackingContractReader(data []byte) *trackingContractReader {
	return &trackingContractReader{reader: bytes.NewReader(data)}
}

func (reader *trackingContractReader) Read(buffer []byte) (int, error) {
	reader.reads++
	return reader.reader.Read(buffer)
}

func (reader *trackingContractReader) Close() error {
	reader.closes++
	return nil
}

func TestEncodeContractByteStreamRejectsLimitBeforeReadingAndCloses(t *testing.T) {
	reader := newTrackingContractReader([]byte("oversized"))
	_, err := EncodeContractByteStreamWithOptions(nil, http.StatusOK, NewContractByteStream(reader, 9), []string{"application/octet-stream"}, ContractResponseOptions{MaxBytes: 8})
	if err == nil {
		t.Fatal("oversized stream was accepted")
	}
	if reader.reads != 0 || reader.closes != 1 {
		t.Fatalf("reader lifecycle = %d reads, %d closes; want 0 reads, 1 close", reader.reads, reader.closes)
	}
}

func TestBufferContractByteStreamReadsExactlyAndCloses(t *testing.T) {
	reader := newTrackingContractReader([]byte("mcp-bytes"))
	content, err := BufferContractByteStream(NewContractByteStream(reader, 9), 9)
	if err != nil || string(content) != "mcp-bytes" {
		t.Fatalf("content = %q, err = %v", content, err)
	}
	if reader.closes != 1 {
		t.Fatalf("reader closes = %d, want 1", reader.closes)
	}

	reader = newTrackingContractReader([]byte("too-large"))
	if _, err := BufferContractByteStream(NewContractByteStream(reader, 9), 8); err == nil {
		t.Fatal("oversized compatibility buffer was accepted")
	}
	if reader.reads != 0 || reader.closes != 1 {
		t.Fatalf("rejected reader lifecycle = %d reads, %d closes", reader.reads, reader.closes)
	}
}

func TestTypedContractByteStreamIdentityGzipAndHead(t *testing.T) {
	payload := bytes.Repeat([]byte("maximum-quality-glb"), 1024)
	for _, test := range []struct {
		name           string
		method         string
		acceptEncoding string
		wantEncoding   string
	}{
		{name: "identity", method: http.MethodGet},
		{name: "gzip", method: http.MethodGet, acceptEncoding: "gzip", wantEncoding: "gzip"},
		{name: "head", method: http.MethodHead},
	} {
		t.Run(test.name, func(t *testing.T) {
			restore := replaceGlobalRegistryForTest()
			defer restore()
			reader := newTrackingContractReader(payload)
			if err := RegisterEndpointChecked(&Endpoint{
				Service: "contract", Name: "Download", Access: Public, Path: "/download", Methods: []string{http.MethodGet},
				DecodeContractRequest: func(*http.Request, map[string]string) (ContractDecodedRequest, error) {
					return ContractDecodedRequest{}, nil
				},
				Invoke: func(context.Context, []any, any) (any, error) {
					return NewContractStreamOutcome("metadata", NewContractByteStream(reader, int64(len(payload)))), nil
				},
				EncodeContractOutcome: func(request *http.Request, outcome any) (ContractHTTPResponse, error) {
					streamed := outcome.(*ContractStreamOutcome)
					stream, err := streamed.TakeStream()
					if err != nil {
						return ContractHTTPResponse{}, err
					}
					return EncodeContractByteStreamWithOptions(request, http.StatusOK, stream, []string{"model/gltf-binary"}, ContractResponseOptions{MaxBytes: int64(len(payload)), CompressionAlgorithms: []string{"gzip"}, CompressionThreshold: 1})
				},
			}); err != nil {
				t.Fatal(err)
			}
			server, err := newServer("127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(test.method, "/download", nil)
			request.Header.Set("Accept", "model/gltf-binary")
			if test.acceptEncoding != "" {
				request.Header.Set("Accept-Encoding", test.acceptEncoding)
			}
			recorder := httptest.NewRecorder()
			server.Handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status = %d, body = %q", recorder.Code, recorder.Body.String())
			}
			if got := recorder.Header().Get("Content-Type"); got != "model/gltf-binary" {
				t.Fatalf("content type = %q", got)
			}
			if got := recorder.Header().Get("Content-Encoding"); got != test.wantEncoding {
				t.Fatalf("content encoding = %q, want %q", got, test.wantEncoding)
			}
			if reader.closes != 1 {
				t.Fatalf("reader closes = %d, want 1", reader.closes)
			}
			if test.method == http.MethodHead {
				if reader.reads != 0 || recorder.Body.Len() != 0 {
					t.Fatalf("HEAD read %d times and wrote %d bytes", reader.reads, recorder.Body.Len())
				}
				if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(payload)) {
					t.Fatalf("HEAD content length = %q", got)
				}
				return
			}
			if reader.reads == 0 {
				t.Fatal("GET did not read the stream")
			}
			body := recorder.Body.Bytes()
			if test.wantEncoding == "gzip" {
				compressed, err := gzip.NewReader(bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				body, err = io.ReadAll(compressed)
				if err != nil {
					t.Fatal(err)
				}
			}
			if !bytes.Equal(body, payload) {
				t.Fatalf("body length = %d, want %d", len(body), len(payload))
			}
			if test.wantEncoding == "" {
				if got := recorder.Header().Get("Content-Length"); got != strconv.Itoa(len(payload)) {
					t.Fatalf("content length = %q", got)
				}
			}
		})
	}
}

func TestWriteContractByteStreamShortReadClosesAndCorruptsGzip(t *testing.T) {
	reader := newTrackingContractReader([]byte("short"))
	var output bytes.Buffer
	err := writeContractByteStream(&output, ContractHTTPResponse{Stream: &ContractByteStream{Reader: reader, Size: 10}, StreamEncoding: "gzip"})
	if err == nil {
		t.Fatal("short stream was accepted")
	}
	if reader.closes != 1 {
		t.Fatalf("reader closes = %d, want 1", reader.closes)
	}
	compressed, gzipErr := gzip.NewReader(bytes.NewReader(output.Bytes()))
	if gzipErr == nil {
		_, gzipErr = io.ReadAll(compressed)
	}
	if gzipErr == nil {
		t.Fatal("short gzip stream was finalized as a valid response")
	}
}

type failingContractWriter struct{ remaining int }

func (writer *failingContractWriter) Write(data []byte) (int, error) {
	if len(data) > writer.remaining {
		written := writer.remaining
		writer.remaining = 0
		return written, errors.New("client disconnected")
	}
	writer.remaining -= len(data)
	return len(data), nil
}

func TestWriteContractByteStreamClientDisconnectCloses(t *testing.T) {
	reader := newTrackingContractReader(bytes.Repeat([]byte("x"), 64))
	err := writeContractByteStream(&failingContractWriter{remaining: 8}, ContractHTTPResponse{Stream: &ContractByteStream{Reader: reader, Size: 64}})
	if err == nil {
		t.Fatal("client disconnect was ignored")
	}
	if reader.closes != 1 {
		t.Fatalf("reader closes = %d, want 1", reader.closes)
	}
}
