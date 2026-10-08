package observability

import (
	"encoding/json"
	"errors"
	"io"
)

// Validate the entire finite response without decoding or retaining a trailing
// value. The first Decode can consume bytes and lose their accompanying read
// error, so record that error before the decoder sees the body.
func decodeFiniteJSON(body io.Reader, value any) error {
	reader := &responseErrorReader{Reader: body}
	decoder := json.NewDecoder(reader)
	decoder.UseNumber()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if reader.err != nil {
		return reader.err
	}
	remainder := io.MultiReader(decoder.Buffered(), reader)
	var scratch [4096]byte
	for {
		n, err := remainder.Read(scratch[:])
		if !onlyJSONWhitespace(scratch[:n]) {
			return errors.New("response contains data after JSON value")
		}
		if reader.err != nil {
			return reader.err
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

type responseErrorReader struct {
	io.Reader
	err error
}

func (r *responseErrorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) && r.err == nil {
		r.err = err
	}
	return n, err
}

func onlyJSONWhitespace(data []byte) bool {
	for _, b := range data {
		if b != ' ' && b != '\t' && b != '\r' && b != '\n' {
			return false
		}
	}
	return true
}
