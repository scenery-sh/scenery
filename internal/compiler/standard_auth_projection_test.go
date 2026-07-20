package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandardAuthGoogleProjectionFollowsRuntimeConfig(t *testing.T) {
	root := t.TempDir()
	resources := []Resource{
		{
			Address: "app/authentication/standard",
			Kind:    "scenery.authentication",
			Name:    "standard",
			Module:  "app",
			Spec:    map[string]any{"provider": map[string]any{"$ref": "std.provider.standard_auth"}},
		},
		{
			Address: "app/http_gateway/public_api",
			Kind:    "scenery.http-gateway",
			Name:    "public_api",
			Module:  "app",
			Spec:    map[string]any{"base_path": "/"},
		},
	}
	writeProjectionConfig(t, root, `{"name":"test","envs":{"local":{"default":true}},"auth":{"enabled":true,"google_oauth":{"enabled":true}}}`)

	projected, err := standardAuthProjectionResources(root, resources)
	if err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{
		"scenery_auth/operation/google_connect_start",
		"scenery_auth/operation/get_google_connection",
		"scenery_auth/operation/disconnect_google_connection",
		"scenery_auth/binding/google_connect_start_public_api_http",
		"scenery_auth/record/google_connection_response",
	} {
		if !hasProjectionResource(projected, address) {
			t.Fatalf("projection missing %s: %#v", address, projected)
		}
	}

	writeProjectionConfig(t, root, `{"name":"test","envs":{"local":{"default":true}},"auth":{"enabled":true,"google_oauth":{"enabled":false}}}`)
	projected, err = standardAuthProjectionResources(root, resources)
	if err != nil {
		t.Fatal(err)
	}
	if len(projected) != 0 {
		t.Fatalf("disabled Google OAuth projected %#v", projected)
	}
}

func writeProjectionConfig(t *testing.T, root, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, ".scenery.json"), []byte(contents), 0o644); err != nil {
		t.Fatal(err)
	}
}

func hasProjectionResource(resources []Resource, address string) bool {
	for _, resource := range resources {
		if resource.Address == address {
			return true
		}
	}
	return false
}
