package host

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

func TestRuntimeResponseIdentityUsesLinkedServedCodeOnlyInDevelopment(t *testing.T) {
	previous := CurrentLinkedContractBundle()
	t.Cleanup(func() {
		linkedContractRevision = previous.ContractRevision
		linkedImplementationRevision = previous.ImplementationRevision
		linkedBuildInputDigest = previous.BuildInputDigest
		linkedGoTarget = previous.GoTarget
	})
	linkedContractRevision = "sha256:" + strings.Repeat("a", 64)
	linkedImplementationRevision = "sha256:" + strings.Repeat("b", 64)
	linkedBuildInputDigest = "sha256:" + strings.Repeat("c", 64)
	linkedGoTarget = "development"
	for _, enabled := range []string{"0", "1"} {
		t.Setenv("SCENERY_DEV_ENDPOINTS", enabled)
		t.Setenv("SCENERY_DEV_SUPERVISOR", "0")
		handler := withRuntimeIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, httptest.NewRequest("GET", "/denied", nil))
		if enabled == "0" {
			if response.Header().Get("X-Scenery-Implementation-Revision") != "" {
				t.Fatal("production response exposed development identity")
			}
			continue
		}
		for name, want := range map[string]string{
			"X-Scenery-Contract-Revision":       linkedContractRevision,
			"X-Scenery-Implementation-Revision": linkedImplementationRevision,
			"X-Scenery-Build-Input-Digest":      linkedBuildInputDigest,
			"X-Scenery-Go-Target":               linkedGoTarget,
			"X-Scenery-Process-ID":              strconv.Itoa(os.Getpid()),
		} {
			if response.Header().Get(name) != want || !strings.Contains(strings.ToLower(response.Header().Get("Access-Control-Expose-Headers")), strings.ToLower(name)) {
				t.Fatalf("missing served identity %s: %v", name, response.Header())
			}
		}
		if response.Code != http.StatusForbidden {
			t.Fatal("identity middleware changed the result")
		}
	}
	t.Setenv("SCENERY_DEV_ENDPOINTS", "1")
	linkedBuildInputDigest = ""
	response := httptest.NewRecorder()
	withRuntimeIdentity(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })).ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	if response.Header().Get("X-Scenery-Implementation-Revision") != "" {
		t.Fatal("unbound binary claimed complete served identity")
	}
}
