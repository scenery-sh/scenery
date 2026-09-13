package scenery

import (
	"io"

	"scenery.sh/runtime/shared"
)

// ByteStream is an exact-length HTTP response body. A successful streaming
// handler transfers ownership of Reader to Scenery, which always closes it.
type ByteStream = shared.ByteStream

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return shared.NewByteStream(reader, size)
}
