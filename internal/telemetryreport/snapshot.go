package telemetryreport

import (
	"errors"
	"io"
	"math"
	"os"
)

// snapshotReader reads only the captured extent of an opened source. An early
// EOF is an error so a logical parser cannot join later segments across a gap.
type snapshotReader struct {
	limited   io.LimitedReader
	size      int64
	readBytes int64
}

func captureSnapshot(file *os.File) (*snapshotReader, os.FileInfo, error) {
	info, err := file.Stat()
	if err != nil {
		return nil, nil, err
	}
	if !info.Mode().IsRegular() || info.Size() < 0 {
		return nil, info, errors.New("report source has no finite regular-file extent")
	}
	return newSnapshotReader(file, info.Size()), info, nil
}

func newSnapshotReader(reader io.Reader, size int64) *snapshotReader {
	return &snapshotReader{limited: io.LimitedReader{R: reader, N: size}, size: size}
}

func (s *snapshotReader) Read(data []byte) (int, error) {
	n, err := s.limited.Read(data)
	s.readBytes += int64(n) // The limited reader cannot return more than size.
	if errors.Is(err, io.EOF) && s.limited.N > 0 {
		err = io.ErrUnexpectedEOF
	}
	return n, err
}

func addReadBytes(total, count int64) (int64, bool) {
	if total < 0 || count < 0 || count > math.MaxInt64-total {
		return total, false
	}
	return total + count, true
}
