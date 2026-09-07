package generate

import (
	"testing"

	"scenery.sh/internal/compiler"
)

func TestClientCoverageReportsEmptySelectedTarget(t *testing.T) {
	result := &compiler.Result{Manifest: &Manifest{Resources: []Resource{
		{Kind: "scenery.typescript-client", Name: "public_api", Address: "app/typescript_client/public_api", Spec: map[string]any{}},
	}}}
	coverage := ClientCoverageFor(result, "typescript_client.public_api")
	if len(coverage) != 1 || coverage[0].Bindings != 0 || coverage[0].Message == "" {
		t.Fatalf("coverage = %+v", coverage)
	}
	if got := ClientCoverageFor(result, "typescript_client.other"); len(got) != 0 {
		t.Fatalf("unselected = %+v", got)
	}
	result.Manifest.Resources = append(result.Manifest.Resources,
		Resource{Kind: "scenery.operation", Name: "read", Address: "inbox/operation/read", Module: "inbox", Spec: map[string]any{}},
		Resource{Kind: "scenery.binding", Name: "read", Address: "inbox/binding/read", Module: "inbox", Origin: compiler.Origin{Kind: "authored"}, Spec: map[string]any{"operation": "inbox/operation/read", "protocol": "http", "http": map[string]any{}}},
	)
	coverage = ClientCoverageFor(result, "")
	if len(coverage) != 1 || coverage[0].Bindings != 1 || coverage[0].Message != "" {
		t.Fatalf("covered = %+v", coverage)
	}
}
