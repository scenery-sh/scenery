package scenery

import (
	"io"

	"scenery.sh/runtime"
)

// ByteStream is an exact-length HTTP response body. A successful streaming
// handler transfers ownership of Reader to Scenery, which always closes it.
type ByteStream = runtime.ContractByteStream

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return runtime.NewContractByteStream(reader, size)
}
