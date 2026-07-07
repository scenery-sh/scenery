package standardauthmeta

import "testing"

func TestEndpointsGateGoogleOAuth(t *testing.T) {
	t.Parallel()

	if hasEndpoint(Endpoints(false), "GoogleStart") || hasEndpoint(Endpoints(false), "GoogleCallback") {
		t.Fatal("disabled Google OAuth endpoints leaked into standard auth metadata")
	}
	if !hasEndpoint(Endpoints(true), "GoogleStart") || !hasEndpoint(Endpoints(true), "GoogleCallback") {
		t.Fatal("enabled Google OAuth endpoints missing from standard auth metadata")
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
