package vnext

import (
	"path/filepath"
	"testing"
)

func TestHTTPEffectiveDefaultsMatchCodecProfile(t *testing.T) {
	result, err := Compile(filepath.Join("testdata", "house"))
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid() {
		t.Fatalf("diagnostics = %#v", result.Diagnostics)
	}
	effective, err := result.ManifestForView("effective")
	if err != nil {
		t.Fatal(err)
	}
	gateway := resourcesByAddress(effective)["app/http_gateway/public_api"]
	request, _ := gateway.Spec["request_limit"].(map[string]any)
	response, _ := gateway.Spec["response_limit"].(map[string]any)
	wantRequest := map[string]int{
		"header_bytes":                  65536,
		"body_bytes":                    8388608,
		"decompressed_body_bytes":       16777216,
		"multipart_body_bytes":          33554432,
		"multipart_file_part_bytes":     16777216,
		"multipart_non_file_part_bytes": 1048576,
		"multipart_parts":               128,
	}
	for name, want := range wantRequest {
		got, ok := integerValue(request[name])
		if !ok || got != want {
			t.Errorf("request_limit.%s = %#v; want %d", name, request[name], want)
		}
	}
	if got, ok := integerValue(response["body_bytes"]); !ok || got != 16777216 {
		t.Errorf("response_limit.body_bytes = %#v; want 16777216", response["body_bytes"])
	}
}

func TestHTTPWaitDefaultUsesCanonicalDispatchOutcome(t *testing.T) {
	httpSpec := map[string]any{}
	applyHTTPStandardResponses(Resource{Spec: map[string]any{
		"delivery":       "wait",
		"authentication": map[string]any{"$ref": "std.authentication.none"},
	}}, httpSpec)
	seen := map[string]bool{}
	for _, response := range namedChildren(httpSpec, "response") {
		seen[refOrString(response["when"])] = true
	}
	if !seen["dispatch.wait_timeout"] || seen["dispatch.timeout"] {
		t.Fatalf("default outcomes = %#v", seen)
	}
}
