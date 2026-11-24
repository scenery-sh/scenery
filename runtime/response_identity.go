package runtime

import (
	"net/http"
	"os"
	"strconv"
)

// withRuntimeIdentity attests the linked executable for development inspection
// and for a Host forwarding requests over the private service socket.
func withRuntimeIdentity(next http.Handler) http.Handler {
	if !devEndpointsEnabled() {
		if ListenNetworkFromEnv() != "unix" {
			return next
		}
		link, err := currentProcessLink()
		if err != nil || link == nil {
			return next
		}
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
