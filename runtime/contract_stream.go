package runtime

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
)

// EncodeContractByteStreamWithOptions consumes stream ownership on every
// return path. Successful responses transfer that ownership to the server.
func EncodeContractByteStreamWithOptions(request *http.Request, status int, stream ContractByteStream, produced []string, options ContractResponseOptions) (response ContractHTTPResponse, err error) {
	owned := stream
	defer func() {
		if err != nil {
			_ = owned.Close()
		}
	}()
	if owned.Reader == nil {
		return ContractHTTPResponse{}, fmt.Errorf("byte stream requires a reader")
	}
	if owned.Size < 0 {
		return ContractHTTPResponse{}, fmt.Errorf("byte stream requires a non-negative exact size")
	}
	if options.MaxBytes > 0 && owned.Size > options.MaxBytes {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "system.internal", Status: http.StatusInternalServerError, Message: "response exceeds binding limit"}
	}
	accept, acceptEncoding := "", ""
	if request != nil {
		accept = request.Header.Get("Accept")
		acceptEncoding = request.Header.Get("Accept-Encoding")
	}
	mediaType, negotiateErr := negotiateContractMedia(accept, produced)
	if negotiateErr != nil {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: negotiateErr.Error(), Cause: negotiateErr}
	}
	encoding, negotiateErr := negotiateContractEncoding(acceptEncoding, options.CompressionAlgorithms)
	if owned.Size < options.CompressionThreshold {
		if identity, identityErr := negotiateContractEncoding(acceptEncoding, nil); identityErr == nil {
			encoding = identity
		}
	}
	if negotiateErr != nil {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: negotiateErr.Error(), Cause: negotiateErr}
	}
	headers := http.Header{
		"Content-Type":                   []string{mediaType},
		"X-Scenery-Contract-Compression": []string{"handled"},
	}
	if len(options.CompressionAlgorithms) > 0 {
		headers.Set("Vary", "Accept-Encoding")
	}
	if encoding == "gzip" {
		headers.Set("Content-Encoding", "gzip")
	} else {
		headers.Set("Content-Length", strconv.FormatInt(owned.Size, 10))
	}
	response = ContractHTTPResponse{Status: status, Headers: headers, Stream: &owned, StreamEncoding: encoding}
	return response, nil
}

// BufferContractByteStream consumes a typed stream for a non-streaming
// transport such as an MCP JSON result. Direct HTTP stream bindings do not use
// this compatibility boundary.
func BufferContractByteStream(stream ContractByteStream, maxBytes int64) (content []byte, err error) {
	owned := stream
	defer func() {
		closeErr := owned.Close()
		if err == nil && closeErr != nil {
			err = closeErr
		}
	}()
	if owned.Reader == nil {
		return nil, fmt.Errorf("byte stream requires a reader")
	}
	if owned.Size < 0 {
		return nil, fmt.Errorf("byte stream requires a non-negative exact size")
	}
	if maxBytes > 0 && owned.Size > maxBytes {
		return nil, fmt.Errorf("byte stream exceeds transport limit")
	}
	if uint64(owned.Size) > uint64(^uint(0)>>1) {
		return nil, fmt.Errorf("byte stream is too large to buffer")
	}
	content = make([]byte, int(owned.Size))
	if _, err := io.ReadFull(owned.Reader, content); err != nil {
		return nil, fmt.Errorf("read byte stream: %w", err)
	}
	return content, nil
}
