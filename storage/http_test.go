package storage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type rangeStore struct {
	recordingStore
	payload     string
	gets, heads int
	readBytes   int
}

func (s *rangeStore) Head(context.Context, string) (*Object, error) {
	s.heads++
	return &Object{Key: "a", SizeBytes: int64(len(s.payload)), ETag: "version", ContentType: "text/plain"}, nil
}
func (s *rangeStore) Get(_ context.Context, _ string, opts GetOptions) (io.ReadCloser, *Object, error) {
	s.gets++
	start, end := 0, len(s.payload)
	if opts.Offset != nil {
		start = int(*opts.Offset)
	}
	if opts.Length != nil && start+int(*opts.Length) < end {
		end = start + int(*opts.Length)
	}
	return io.NopCloser(countRead{reader: strings.NewReader(s.payload[start:end]), count: &s.readBytes}), &Object{Key: "a", SizeBytes: int64(len(s.payload)), ETag: "version", ContentType: "text/plain"}, nil
}

type countRead struct {
	reader io.Reader
	count  *int
}

func (r countRead) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	*r.count += n
	return n, err
}
func TestServeObjectReadsOnlyRequestedRange(t *testing.T) {
	for _, test := range []struct {
		header, body, contentRange string
		status                     int
	}{
		{"", "hello world", "", 200},
		{"bytes=6-10", "world", "bytes 6-10/11", 206},
		{"bytes=-5", "world", "bytes 6-10/11", 206},
		{"bytes=8-99", "rld", "bytes 8-10/11", 206},
		{"bytes=-99", "hello world", "bytes 0-10/11", 206},
		{"bytes=11-", "", "bytes */11", 416},
		{"bytes=-0", "", "bytes */11", 416},
		{"bytes=0-1,4-5", "", "bytes */11", 416},
	} {
		store := &rangeStore{payload: "hello world"}
		request := httptest.NewRequest(http.MethodGet, "/a", nil)
		request.Header.Set("Range", test.header)
		recorder := httptest.NewRecorder()
		if err := ServeObject(recorder, request, store, "a"); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != test.status || recorder.Header().Get("Content-Range") != test.contentRange {
			t.Fatalf("%q: %d, %v", test.header, recorder.Code, recorder.Header())
		}
		if test.status < 300 && (recorder.Body.String() != test.body || store.readBytes != len(test.body)) {
			t.Fatalf("%q: body=%q read=%d", test.header, recorder.Body.String(), store.readBytes)
		}
		if test.status == 416 && store.gets != 0 {
			t.Fatal("unsatisfiable range opened payload")
		}
	}
}
func TestServeObjectHEADAndZeroSize(t *testing.T) {
	for _, method := range []string{http.MethodHead, http.MethodGet} {
		store := &rangeStore{}
		request := httptest.NewRequest(method, "/a", nil)
		recorder := httptest.NewRecorder()
		if err := ServeObject(recorder, request, store, "a"); err != nil {
			t.Fatal(err)
		}
		if recorder.Code != 200 || recorder.Body.Len() != 0 || recorder.Header().Get("Content-Length") != "0" {
			t.Fatalf("zero-size %s: %+v", method, recorder)
		}
		if method == http.MethodHead && store.gets != 0 {
			t.Fatal("HEAD opened payload")
		}
	}
	store := &rangeStore{}
	request := httptest.NewRequest(http.MethodGet, "/a", nil)
	request.Header.Set("Range", "bytes=-1")
	recorder := httptest.NewRecorder()
	if err := ServeObject(recorder, request, store, "a"); err != nil {
		t.Fatal(err)
	}
	if recorder.Code != 416 {
		t.Fatalf("empty suffix range: %d", recorder.Code)
	}
}
func TestMetadataHeadersPreserveExactKeysAndUTF8(t *testing.T) {
	metadata := map[string]string{"lowerCase": "žluťoučký", "UPPER": "\nvalue", "Ü": "<html>"}
	header := http.Header{}
	SetMetadataHeaders(header, metadata)
	got, err := MetadataFromHeaders(header)
	if err != nil || !reflect.DeepEqual(got, metadata) {
		t.Fatalf("metadata: %+v, %v", got, err)
	}
	header.Set("X-Scenery-Storage-Metadata", "not base64")
	if _, err := MetadataFromHeaders(header); err == nil {
		t.Fatal("invalid metadata accepted")
	}
}
