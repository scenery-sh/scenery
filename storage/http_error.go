package storage

import (
	"encoding/base64"
	"encoding/json"
	"net/http"

	"scenery.sh/internal/storagefs"
)

const (
	// HTTP HEAD responses cannot carry a body, so proxy clients receive the
	// same bounded failure envelope in a safe, encoded header as GET clients.
	storageErrorHeader    = "X-Scenery-Storage-Error"
	maxStorageErrorBytes  = 16 << 10
	maxStorageErrorHeader = 24 << 10
)

// HTTPError preserves storage failure identity and bounded partial-progress
// details while keeping internal errors opaque on both HTTP adapters.
func HTTPError(w http.ResponseWriter, err error) {
	failure, ok := storagefs.DescribeError(err)
	if !ok {
		failure = storagefs.InternalFailure()
	}
	if data, marshalErr := json.Marshal(failure); marshalErr == nil && len(data) <= maxStorageErrorBytes {
		encoded := base64.RawURLEncoding.EncodeToString(data)
		if len(encoded) <= maxStorageErrorHeader {
			w.Header().Set(storageErrorHeader, encoded)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(failure.HTTPStatus)
	_ = json.NewEncoder(w).Encode(failure)
}
