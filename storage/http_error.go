package storage

import (
	"encoding/json"
	"net/http"

	"scenery.sh/internal/storagefs"
)

// HTTPError preserves storage failure identity and bounded partial-progress
// details while keeping internal errors opaque on both HTTP adapters.
func HTTPError(w http.ResponseWriter, err error) {
	failure, ok := storagefs.DescribeError(err)
	if !ok {
		failure = storagefs.InternalFailure()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(failure.HTTPStatus)
	_ = json.NewEncoder(w).Encode(failure)
}
