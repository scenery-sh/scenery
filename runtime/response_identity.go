package runtime

import (
	"net/http"
	"os"
	"strconv"
)

// withRuntimeIdentity binds development HTTP evidence to the linked bytes that
// served the request. It reads no candidate files and is absent in production.
func withRuntimeIdentity(next http.Handler) http.Handler {
	if !devEndpointsEnabled() {
		return next
	}
	bundle := CurrentLinkedContractBundle()
	if VerifyLinkedContractBundle(bundle.ContractRevision) != nil {
		return next
	}
	values := map[string]string{
		"X-Scenery-Contract-Revision":       bundle.ContractRevision,
		"X-Scenery-Implementation-Revision": bundle.ImplementationRevision,
		"X-Scenery-Build-Input-Digest":      bundle.BuildInputDigest,
		"X-Scenery-Go-Target":               bundle.GoTarget,
		"X-Scenery-Process-ID":              strconv.Itoa(os.Getpid()),
	}
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		for name, value := range values {
			w.Header().Set(name, value)
			exposeResponseHeader(w.Header(), name)
		}
		next.ServeHTTP(w, req)
	})
}
