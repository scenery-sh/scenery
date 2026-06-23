package storage

import (
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

func ServeObject(w http.ResponseWriter, req *http.Request, body io.ReadCloser, obj *Object) {
	defer body.Close()
	if obj == nil {
		http.Error(w, "missing storage object metadata", http.StatusInternalServerError)
		return
	}
	if obj.ContentType != "" {
		w.Header().Set("Content-Type", obj.ContentType)
	}
	if obj.ETag != "" {
		w.Header().Set("ETag", obj.ETag)
	}
	if !obj.ModifiedAt.IsZero() {
		w.Header().Set("Last-Modified", obj.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	for k, v := range obj.Metadata {
		if k == "" || strings.ContainsAny(k, "\r\n:") {
			continue
		}
		w.Header().Set("X-Scenery-Storage-Meta-"+k, v)
	}
	w.Header().Set("Accept-Ranges", "bytes")

	start, length, ranged, ok := parseRange(req.Header.Get("Range"), obj.SizeBytes)
	if !ok {
		http.Error(w, "invalid range", http.StatusRequestedRangeNotSatisfiable)
		return
	}
	status := http.StatusOK
	size := obj.SizeBytes
	if ranged {
		status = http.StatusPartialContent
		size = length
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, obj.SizeBytes))
	}
	if size >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(status)
	if req.Method == http.MethodHead {
		return
	}
	if start > 0 {
		if _, err := io.CopyN(io.Discard, body, start); err != nil {
			return
		}
	}
	if ranged {
		_, _ = io.CopyN(w, body, length)
		return
	}
	_, _ = io.Copy(w, body)
}

func parseRange(header string, size int64) (start, length int64, ranged, ok bool) {
	if header == "" {
		return 0, size, false, true
	}
	if size < 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
		return 0, 0, false, false
	}
	spec := strings.TrimPrefix(header, "bytes=")
	parts := strings.Split(spec, "-")
	if len(parts) != 2 {
		return 0, 0, false, false
	}
	if parts[0] == "" {
		suffix, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || suffix <= 0 {
			return 0, 0, false, false
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, suffix, true, true
	}
	first, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || first < 0 || first >= size {
		return 0, 0, false, false
	}
	last := size - 1
	if parts[1] != "" {
		last, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || last < first {
			return 0, 0, false, false
		}
		if last >= size {
			last = size - 1
		}
	}
	return first, last - first + 1, true, true
}
