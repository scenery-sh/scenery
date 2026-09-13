package shared

import "io"

// ByteStream is an exact-length response body whose reader ownership
// transfers to Scenery after a successful streaming handler return.
type ByteStream struct {
	Reader io.ReadCloser
	Size   int64
}

func NewByteStream(reader io.ReadCloser, size int64) ByteStream {
	return ByteStream{Reader: reader, Size: size}
}

func (stream *ByteStream) Close() error {
	if stream == nil || stream.Reader == nil {
		return nil
	}
	reader := stream.Reader
	stream.Reader = nil
	return reader.Close()
}
