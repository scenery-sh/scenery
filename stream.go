package scenery

import (
	"io"

	"scenery.sh/internal/runtimeapp"
)

// ByteStream is an exact-length HTTP response body. A successful streaming
// handler transfers ownership of Reader to Scenery, which always closes it.
type ByteStream = runtimeapp.ByteStream

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return runtimeapp.NewByteStream(reader, size)
}
