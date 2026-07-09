package standardauthmeta

import "testing"

func TestEndpointsGateGoogleOAuth(t *testing.T) {
	t.Parallel()

	googleEndpoints := []string{
		"DisconnectGoogleConnection",
		"GetGoogleConnection",
		"GoogleCallback",
		"GoogleConnectCallback",
		"GoogleConnectStart",
		"GoogleStart",
	}
	for _, name := range googleEndpoints {
		if hasEndpoint(Endpoints(false), name) {
			t.Fatalf("disabled Google OAuth endpoint %s leaked into standard auth metadata", name)
		}
		if !hasEndpoint(Endpoints(true), name) {
			t.Fatalf("enabled Google OAuth endpoint %s missing from standard auth metadata", name)
		}
	}
}

func hasEndpoint(endpoints []Endpoint, name string) bool {
	for _, endpoint := range endpoints {
		if endpoint.Name == name {
			return true
		}
	}
	return false
}
