package telemetryreport

import (
	"bufio"
	"bytes"
	"errors"
	"io"
)

// readLines passes each line of r, without its newline, to visit. A line
// longer than limit is skipped and counted instead of ending the read, so one
// oversized record neither hides the records after it nor makes the reader
// hold more than limit bytes. It returns the skipped lines and any read error,
// after visiting every complete line before it.
func readLines(r io.Reader, limit int, visit func([]byte)) (int, error) {
	reader := bufio.NewReaderSize(r, 64<<10)
	var line []byte
	oversized := 0
	skipping := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !skipping {
			if len(line)+len(chunk) > limit+1 {
				skipping = true
				line = line[:0]
			} else {
				line = append(line, chunk...)
			}
		}
		switch {
		case err == nil:
			if skipping {
				oversized++
			} else {
				visit(bytes.TrimRight(line, "\r\n"))
			}
			line, skipping = line[:0], false
		case errors.Is(err, bufio.ErrBufferFull):
		case errors.Is(err, io.EOF):
			if skipping {
				oversized++
			} else if len(line) > 0 {
				visit(bytes.TrimRight(line, "\r\n"))
			}
			return oversized, nil
		default:
			return oversized, err
		}
	}
}
