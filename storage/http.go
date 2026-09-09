package storage

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// ServeObject resolves an immutable version before writing headers. A ranged
// request reads only its selected bytes; HEAD never calls Get.
func ServeObject(w http.ResponseWriter, req *http.Request, store Store, key string) error {
	if req.Method == http.MethodHead {
		obj, err := store.Head(req.Context(), key)
		if err != nil {
			return err
		}
		setObjectHeaders(w.Header(), obj)
		w.Header().Set("Content-Length", strconv.FormatInt(obj.SizeBytes, 10))
		w.WriteHeader(http.StatusOK)
		return nil
	}
	var body io.ReadCloser
	var obj *Object
	var start, length int64
	ranged := req.Header.Get("Range") != ""
	if !ranged {
		var err error
		body, obj, err = store.Get(req.Context(), key, GetOptions{})
		if err != nil {
			return err
		}
		length = obj.SizeBytes
	} else {
		for range 4 {
			head, err := store.Head(req.Context(), key)
			if err != nil {
				return err
			}
			var ok bool
			start, length, _, ok = parseRange(req.Header.Get("Range"), head.SizeBytes)
			if !ok {
				w.Header().Set("Content-Range", fmt.Sprintf("bytes */%d", head.SizeBytes))
				http.Error(w, "requested range is not satisfiable", http.StatusRequestedRangeNotSatisfiable)
				return nil
			}
			body, obj, err = store.Get(req.Context(), key, GetOptions{Offset: &start, Length: &length})
			if errors.Is(err, ErrInvalidInput) {
				continue
			}
			if err != nil {
				return err
			}
			if obj.ETag == head.ETag {
				break
			}
			_ = body.Close()
			body = nil
		}
		if body == nil {
			return &PreconditionError{Key: key}
		}
	}
	defer func() { _ = body.Close() }()
	setObjectHeaders(w.Header(), obj)
	w.Header().Set("Content-Length", strconv.FormatInt(length, 10))
	status := http.StatusOK
	if ranged {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, obj.SizeBytes))
	}
	w.WriteHeader(status)
	if _, err := io.CopyN(w, body, length); err != nil {
		panic(http.ErrAbortHandler)
	}
	return nil
}
func setObjectHeaders(header http.Header, obj *Object) {
	if obj.ContentType != "" {
		header.Set("Content-Type", obj.ContentType)
	}
	if obj.ETag != "" {
		header.Set("ETag", obj.ETag)
	}
	if !obj.ModifiedAt.IsZero() {
		header.Set("Last-Modified", obj.ModifiedAt.UTC().Format(http.TimeFormat))
	}
	header.Set("Accept-Ranges", "bytes")
	SetMetadataHeaders(header, obj.Metadata)
}

// One encoded map preserves exact user metadata keys and arbitrary UTF-8
// values; HTTP header-name canonicalization must not rewrite those identities.
func SetMetadataHeaders(header http.Header, metadata map[string]string) {
	const name = "X-Scenery-Storage-Metadata"
	if len(metadata) == 0 {
		header.Del(name)
		return
	}
	data, _ := json.Marshal(metadata)
	header.Set(name, base64.RawURLEncoding.EncodeToString(data))
}
func MetadataFromHeaders(header http.Header) (map[string]string, error) {
	raw := header.Get("X-Scenery-Storage-Metadata")
	if raw == "" {
		for name := range header {
			if strings.HasPrefix(strings.ToLower(name), "x-scenery-storage-meta-") {
				return nil, fmt.Errorf("%w: use X-Scenery-Storage-Metadata for the encoded metadata map", ErrInvalidInput)
			}
		}
		return nil, nil
	}
	if len(raw) > 22<<10 {
		return nil, fmt.Errorf("%w: metadata header exceeds its byte budget", ErrInvalidInput)
	}
	data, err := base64.RawURLEncoding.Strict().DecodeString(raw)
	if err != nil || len(data) > 16<<10 {
		return nil, fmt.Errorf("%w: malformed metadata header", ErrInvalidInput)
	}
	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("%w: malformed metadata map", ErrInvalidInput)
	}
	return metadata, nil
}

func parseRange(header string, size int64) (start, length int64, ranged, ok bool) {
	if header == "" {
		return 0, size, false, true
	}
	if size <= 0 || !strings.HasPrefix(header, "bytes=") || strings.Contains(header, ",") {
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
