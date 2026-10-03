package runtime

import (
	"compress/gzip"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Retain at most two idle compressors; bursts cannot leave an unbounded pool.
// Reset detaches response buffers and reuses the deflate workspace.
var idleGzipWriters = make(chan *gzip.Writer, 2)

var responseGzipCopyBuffers = make(chan []byte, 1)

// gzipStreamWriter lets io.CopyN retain exact-length and short-read handling
// while reusing one bounded staging buffer for compressed response streams.
type gzipStreamWriter struct{ *gzip.Writer }

func (writer gzipStreamWriter) ReadFrom(reader io.Reader) (int64, error) {
	size := 32 << 10
	if limited, ok := reader.(*io.LimitedReader); ok {
		size = int(min(int64(size), max(limited.N, 1)))
	}
	var buffer []byte
	select {
	case buffer = <-responseGzipCopyBuffers:
	default:
	}
	if cap(buffer) < size {
		buffer = make([]byte, size)
	}
	buffer = buffer[:size]
	defer func() {
		select {
		case responseGzipCopyBuffers <- buffer:
		default:
		}
	}()
	return io.CopyBuffer(writer.Writer, reader, buffer)
}

func acquireGzipWriter(dst io.Writer) *gzip.Writer {
	select {
	case writer := <-idleGzipWriters:
		writer.Reset(dst)
		return writer
	default:
		return gzip.NewWriter(dst)
	}
}

// Detach the response without flushing; callers close successful responses.
// Failed streams must remain incomplete rather than gaining a valid trailer.
func releaseGzipWriter(writer *gzip.Writer) {
	writer.Reset(io.Discard)
	select {
	case idleGzipWriters <- writer:
	default:
	}
}

func withGzip(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if !requestAcceptsGzip(req) || isHTTPUpgrade(req) {
			next.ServeHTTP(w, req)
			return
		}
		gw := &gzipResponseWriter{
			ResponseWriter: w,
			req:            req,
			status:         http.StatusOK,
		}
		defer gw.close()
		next.ServeHTTP(gw, req)
	})
}

type gzipResponseWriter struct {
	http.ResponseWriter
	req       *http.Request
	status    int
	wroteHead bool
	gzip      *gzip.Writer
}

func (w *gzipResponseWriter) Header() http.Header {
	return w.ResponseWriter.Header()
}

func (w *gzipResponseWriter) WriteHeader(status int) {
	if w.wroteHead {
		return
	}
	w.status = status
}

func (w *gzipResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHead {
		w.start()
	}
	if w.gzip != nil {
		return w.gzip.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *gzipResponseWriter) Flush() {
	if !w.wroteHead {
		w.start()
	}
	if w.gzip != nil {
		_ = w.gzip.Flush()
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *gzipResponseWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *gzipResponseWriter) close() {
	if w.gzip != nil {
		_ = w.gzip.Close()
		releaseGzipWriter(w.gzip)
		w.gzip = nil
	}
	if !w.wroteHead {
		w.Header().Del("X-Scenery-Contract-Compression")
		w.ResponseWriter.WriteHeader(w.status)
		w.wroteHead = true
	}
}

func (w *gzipResponseWriter) start() {
	if w.wroteHead {
		return
	}
	headers := w.Header()
	if shouldGzipResponse(w.req, w.status, headers) {
		addVary(headers, "Accept-Encoding")
		headers.Del("Content-Length")
		headers.Set("Content-Encoding", "gzip")
		w.gzip = acquireGzipWriter(w.ResponseWriter)
	}
	w.ResponseWriter.WriteHeader(w.status)
	w.wroteHead = true
}

func shouldGzipResponse(req *http.Request, status int, headers http.Header) bool {
	if headers.Get("X-Scenery-Contract-Compression") != "" {
		headers.Del("X-Scenery-Contract-Compression")
		return false
	}
	if req == nil || req.Method == http.MethodHead {
		return false
	}
	if req.Header.Get("Range") != "" {
		return false
	}
	if status < 200 || status == http.StatusNoContent || status == http.StatusNotModified {
		return false
	}
	if headers.Get("Content-Encoding") != "" {
		return false
	}
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(headers.Get("Content-Type"))), "text/event-stream") {
		return false
	}
	addVary(headers, "Accept-Encoding")
	return true
}

func requestAcceptsGzip(req *http.Request) bool {
	if req == nil {
		return false
	}
	header := req.Header.Get("Accept-Encoding")
	if header == "" {
		return false
	}
	gzipMentioned := false
	gzipAllowed := false
	starAllowed := false
	for item := range strings.SplitSeq(header, ",") {
		parts := strings.Split(item, ";")
		coding := strings.ToLower(strings.TrimSpace(parts[0]))
		if coding == "" {
			continue
		}
		quality := 1.0
		for _, param := range parts[1:] {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if !ok || !strings.EqualFold(strings.TrimSpace(key), "q") {
				continue
			}
			parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err == nil {
				quality = parsed
			}
		}
		switch coding {
		case "gzip":
			gzipMentioned = true
			gzipAllowed = quality > 0
		case "*":
			starAllowed = quality > 0
		}
	}
	return gzipAllowed || (!gzipMentioned && starAllowed)
}

func isHTTPUpgrade(req *http.Request) bool {
	if req == nil || req.Header.Get("Upgrade") == "" {
		return false
	}
	for _, value := range req.Header.Values("Connection") {
		for part := range strings.SplitSeq(value, ",") {
			if strings.EqualFold(strings.TrimSpace(part), "upgrade") {
				return true
			}
		}
	}
	return false
}
