package vnext

import "testing"

func TestSnakeNamePreservesInitialisms(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"RequestID":     "request_id",
		"tenantID":      "tenant_id",
		"URLValue":      "url_value",
		"HTTPServerURL": "http_server_url",
		"OAuthClientID": "oauth_client_id",
		"already_snake": "already_snake",
	}
	for input, want := range tests {
		if got := snakeName(input); got != want {
			t.Errorf("snakeName(%q) = %q, want %q", input, got, want)
		}
	}
}
