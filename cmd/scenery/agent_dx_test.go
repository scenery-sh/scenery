package main

import (
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
