package compiler

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandardAuthConnectionProjectionFollowsRuntimeConfig(t *testing.T) {
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
	writeProjectionConfig(t, root, `{"name":"test","envs":{"local":{"default":true}},"auth":{"enabled":true,"google_oauth":{"enabled":false},"connections":[{"id":"google","preset":"google"}]}}`)

	cfg, err := frameworkAuthConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	projected := standardAuthProjectionResources(cfg, resources)
	for _, address := range []string{
		"scenery_auth/operation/connection_start",
		"scenery_auth/operation/connection_status",
		"scenery_auth/operation/connection_disconnect",
		"scenery_auth/operation/connection_callback_google",
		"scenery_auth/binding/connection_callback_google_public_api_http",
		"scenery_auth/binding/connection_start_public_api_http",
		"scenery_auth/record/connection_response",
	} {
		if !hasProjectionResource(projected, address) {
			t.Fatalf("projection missing %s: %#v", address, projected)
		}
	}

	for _, resource := range projected {
		if resource.Name == "connection_callback_google" {
			continue
		}
		if resource.Name == "connection_callback_google_public_api_http" {
			httpSpec := resource.Spec["http"].(map[string]any)
			if httpSpec["method"] != "GET" || httpSpec["path"] != "/auth/connections/google/callback" || httpSpec["guarantee"] != "opaque" || refString(resource.Spec["authentication"]) != "std.authentication.none" {
				t.Fatal("callback projection differs from the public runtime endpoint")
			}
			continue
		}
		if resource.Kind == "scenery.operation" {
			if len(namedChildren(resource.Spec, "error")) != 5 {
				t.Fatal("missing typed standard-auth domain errors")
			}
		}
		if resource.Kind == "scenery.binding" {
			httpSpec, _ := resource.Spec["http"].(map[string]any)
			found := map[string]bool{}
			for _, response := range namedChildren(httpSpec, "response") {
				when := refString(response["when"])
				if when == "error.invalid_request" || when == "error.unauthenticated" || when == "error.forbidden" || when == "error.internal" || when == "error.unavailable" {
					found[stringValue(response["status"])] = true
				}
			}
			if len(found) != 5 {
				t.Fatal("provider/storage failure cannot be decoded by generated clients")
			}
		}
	}

	writeProjectionConfig(t, root, `{"name":"test","envs":{"local":{"default":true}},"auth":{"enabled":true,"google_oauth":{"enabled":false}}}`)
	cfg, err = frameworkAuthConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	projected = standardAuthProjectionResources(cfg, resources)
	if len(projected) != 0 {
		t.Fatalf("no configured connections projected %#v", projected)
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
