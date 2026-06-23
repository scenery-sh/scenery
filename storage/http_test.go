package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestServeObject(t *testing.T) {
	t.Parallel()
	obj := &Object{
		Key:         "hello.txt",
		SizeBytes:   int64(len("hello world")),
		ContentType: "text/plain",
		ETag:        `"etag"`,
		ModifiedAt:  time.Date(2026, 6, 22, 9, 0, 0, 0, time.UTC),
		Metadata:    map[string]string{"Author": "scenery"},
	}
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	rr := httptest.NewRecorder()
	ServeObject(rr, req, io.NopCloser(strings.NewReader("hello world")), obj)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.String() != "hello world" {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if rr.Header().Get("Content-Type") != "text/plain" || rr.Header().Get("ETag") != `"etag"` {
		t.Fatalf("headers = %+v", rr.Header())
	}
}

func TestServeObjectRange(t *testing.T) {
	t.Parallel()
	obj := &Object{Key: "hello.txt", SizeBytes: int64(len("hello world"))}
	req := httptest.NewRequest(http.MethodGet, "/hello.txt", nil)
	req.Header.Set("Range", "bytes=6-10")
	rr := httptest.NewRecorder()
	ServeObject(rr, req, io.NopCloser(strings.NewReader("hello world")), obj)
	if rr.Code != http.StatusPartialContent {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.String() != "world" {
		t.Fatalf("body = %q", rr.Body.String())
	}
	if rr.Header().Get("Content-Range") != "bytes 6-10/11" {
		t.Fatalf("content range = %q", rr.Header().Get("Content-Range"))
	}
}
