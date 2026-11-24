package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

func TestAgentCallResolvesGatewayAndSessionBinding(t *testing.T) {
	service := graph.Resource{Address: "pricing/service/pricing", Kind: "scenery.service", Name: "pricing", Module: "pricing"}
	operation := graph.Resource{Address: "pricing/operation/quote", Kind: "scenery.operation", Name: "quote", Module: "pricing",
		Spec: map[string]any{"service": map[string]any{"$ref": "service.pricing"}, "handler": map[string]any{"method": "Quote"}}}
	gateway := graph.Resource{Address: "app/http_gateway/api", Kind: "scenery.http-gateway", Spec: map[string]any{"base_path": "/api"}}
	public := testAgentBinding("public", "/quote", "std.authentication.none")
	session := testAgentBinding("session", "/quote/{id}", "authentication.session")
	resources := []graph.Resource{service, operation, gateway, public, session}
	g := agentGraph{Result: &compiler.Result{Manifest: &graph.Manifest{Resources: resources}}, resources: map[string]graph.Resource{}}
	for _, resource := range resources {
		g.resources[resource.Address] = resource
	}
	for _, test := range []struct {
		target, path string
		as           bool
		want         graph.Resource
	}{
		{operation.Address, "/api/quote", false, public},
		{"pricing.Quote", "/api/quote/{id}", true, session},
		{"POST /api/quote/order-7?view=full", "/api/quote/order-7?view=full", false, session},
	} {
		binding, route, err := g.callTarget(test.target, test.as)
		if err != nil || binding.Address != test.want.Address || route.Path != test.path {
			t.Fatalf("%s resolved to %s %+v: %v", test.target, binding.Address, route, err)
		}
	}
}

func testAgentBinding(name, path, auth string) graph.Resource {
	return graph.Resource{Address: "pricing/binding/" + name, Kind: "scenery.binding", Module: "pricing",
		Spec: map[string]any{
			"protocol": "http", "gateway": map[string]any{"$ref": "http_gateway.api"}, "operation": map[string]any{"$ref": "operation.quote"},
			"authentication": map[string]any{"$ref": auth}, "http": map[string]any{"method": "POST", "path": path},
		}}
}

func TestWorkerSourceWaitMatchesTheFailedRevision(t *testing.T) {
	root := t.TempDir()
	writeWatchFile(t, root, "app.scn", "application \"test\" {}")
	path := filepath.Join("quote", "service.ts")
	writeWatchFile(t, root, path, "const value = ;")
	control := workerDevControl{watch: workerInputWatch{root: root}}
	inputs, err := control.watch.capture(nil, nil, true, false)
	if err != nil {
		t.Fatal(err)
	}
	failure := describeDevBuildFailure(&workerJSBuildError{service: "quote/service/quote", err: fmt.Errorf("unexpected ';'\n at %s:1:15", filepath.Join(root, path))}, root, nil, nil)
	control.failure(inputs, failure, &workerRebuildTiming{RebuiltModules: []string{}, SkippedModules: []string{}})
	recorder := httptest.NewRecorder()
	control.status(recorder, httptest.NewRequest(http.MethodGet, "/status?timeout_ms=30000", nil))
	results := recorder.Body.String()
	if recorder.Code != 200 || !strings.Contains(results, `"outcome":"failed"`) || !strings.Contains(results, `"module":"quote","file":"quote/service.ts","line":1,"column":15`) {
		t.Fatalf("unexpected wait answer: %s", results)
	}
	writeWatchFile(t, root, path, "const value = 42;")
	recorder = httptest.NewRecorder()
	control.status(recorder, httptest.NewRequest(http.MethodGet, "/status", nil))
	if result := recorder.Body.String(); !strings.Contains(result, `"outcome":"building"`) || strings.Contains(result, "compile_error") {
		t.Fatalf("stale failure leaked to a new snapshot: %s", result)
	}
}
