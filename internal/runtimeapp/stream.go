package runtimeapp

import "io"

// ByteStream keeps the exact-length reader in the native application process.
// Transport code takes ownership after a successful streaming handler return.
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
