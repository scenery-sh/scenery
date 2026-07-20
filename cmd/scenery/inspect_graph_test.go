package main

import (
	"io"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	inspectdata "scenery.sh/internal/inspect"
)

func TestContractCommandsRejectUnsupportedOutputBeforeWork(t *testing.T) {
	tests := []struct {
		name string
		run  func(io.Writer, []string) error
		args []string
	}{
		{name: "changes", run: runContractChanges, args: []string{"rename", "house/record/scene", "renamed", "-o", "yaml"}},
		{name: "graph", run: runContractGraph, args: []string{"house/record/scene", "-o", "yaml"}},
		{name: "generate", run: runContractGenerate, args: []string{"-o", "yaml"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.run(io.Discard, test.args)
			if err == nil || !strings.Contains(err.Error(), `unsupported output "yaml"`) {
				t.Fatalf("error = %v, want unsupported output", err)
			}
		})
	}
}

func TestContractInspectDurableProjectsMergedExecution(t *testing.T) {
	result := &compiler.Result{Root: t.TempDir(), Manifest: &graph.Manifest{Resources: []graph.Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service", Name: "house", Spec: map[string]any{}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation", Name: "process", Spec: map[string]any{
			"service": map[string]any{"$ref": "house/service/house"}, "input": map[string]any{"$ref": "record.input"},
			"result": map[string]any{"name": "done", "type": map[string]any{"$ref": "record.output"}},
		}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "house/operation/process"}, "mode": "durable", "external_name": "ProcessScene",
		}},
	}}}
	response := buildInspectDurableResponse(result.Root, appcfg.Config{Name: "test"}, result)
	if response.Durable.TaskCount != 1 || len(response.Declarations) != 1 {
		t.Fatalf("durable response = %#v", response)
	}
	declaration := response.Declarations[0]
	if declaration.Name != "ProcessScene" || declaration.Service != "house" || declaration.Input != "record.input" || declaration.Output != "record.output" {
		t.Fatalf("durable declaration = %#v", declaration)
	}
}

func TestContractInspectProjectsNativeServiceAndPrivateEndpointIdentity(t *testing.T) {
	t.Parallel()

	resources := []graph.Resource{
		{Address: "app/service/auth", Kind: "scenery.service", Name: "auth", Module: "app", Spec: map[string]any{}},
		{Address: "app/service/users", Kind: "scenery.service", Name: "users", Module: "app", Spec: map[string]any{}},
		{Address: "app/http_gateway/public_api", Kind: "scenery.http-gateway", Name: "public_api", Module: "app", Spec: map[string]any{"base_path": "/"}},
		{
			Address: "app/operation/dev_bootstrap", Kind: "scenery.operation", Name: "dev_bootstrap", Module: "app",
			Spec: map[string]any{"service": map[string]any{"$ref": "app/service/users"}, "input": map[string]any{"$ref": "record.input"}, "handler": map[string]any{"method": "DevBootstrap"}},
		},
		{
			Address: "app/binding/dev_bootstrap_http", Kind: "scenery.binding", Name: "dev_bootstrap_http", Module: "app",
			Spec: map[string]any{
				"protocol": "http", "operation": map[string]any{"$ref": "app/operation/dev_bootstrap"}, "gateway": map[string]any{"$ref": "app/http_gateway/public_api"},
				"authentication": map[string]any{"$ref": "std.authentication.none"}, "http": map[string]any{"method": "POST", "path": "/users/dev-bootstrap"},
			},
			Origin: graph.Origin{Kind: "authored"},
		},
	}
	result := &compiler.Result{Manifest: &graph.Manifest{Resources: resources}}

	services := inspectServices(result)
	if got := findInspectServiceEndpoints(services, "users"); len(got) != 1 || got[0] != "DevBootstrap" {
		t.Fatalf("users endpoints = %#v, want [DevBootstrap]", got)
	}
	if got := findInspectServiceEndpoints(services, "auth"); len(got) != 0 {
		t.Fatalf("auth endpoints = %#v, want none", got)
	}

	endpoints, err := inspectEndpoints(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 {
		t.Fatalf("endpoints = %#v, want 1", endpoints)
	}
	if endpoints[0].ID != "users.DevBootstrap" || endpoints[0].Access != "public" {
		t.Fatalf("public endpoint = %#v", endpoints[0])
	}
}

func TestContractInspectIncludesFrameworkOwnedGoogleEndpoints(t *testing.T) {
	t.Parallel()

	result := &compiler.Result{
		Manifest: &graph.Manifest{Resources: []graph.Resource{
			{Address: "app/http_gateway/public_api", Kind: "scenery.http-gateway", Name: "public_api", Module: "app", Spec: map[string]any{"base_path": "/"}},
		}},
		FrameworkResources: []graph.Resource{
			{Address: "scenery_auth/service/auth", Kind: "scenery.service", Name: "auth", Module: "scenery_auth", Spec: map[string]any{}, Origin: graph.Origin{Kind: "framework"}},
			{Address: "scenery_auth/operation/google_connect_start", Kind: "scenery.operation", Name: "google_connect_start", Module: "scenery_auth", Spec: map[string]any{
				"service": map[string]any{"$ref": "scenery_auth/service/auth"},
				"handler": map[string]any{"method": "GoogleConnectStart"},
			}, Origin: graph.Origin{Kind: "framework"}},
			{Address: "scenery_auth/binding/google_connect_start_public_api_http", Kind: "scenery.binding", Name: "google_connect_start_public_api_http", Module: "scenery_auth", Spec: map[string]any{
				"protocol":       "http",
				"operation":      map[string]any{"$ref": "scenery_auth/operation/google_connect_start"},
				"gateway":        map[string]any{"$ref": "app/http_gateway/public_api"},
				"authentication": map[string]any{"$ref": "app/authentication/standard"},
				"http":           map[string]any{"method": "POST", "path": "/auth/google/connect/start", "body": map[string]any{}},
			}, Origin: graph.Origin{Kind: "framework"}},
		},
	}

	endpoints, err := inspectEndpoints(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 1 || endpoints[0].ID != "auth.GoogleConnectStart" || endpoints[0].Access != "auth" || !endpoints[0].Generated {
		t.Fatalf("framework endpoints = %#v", endpoints)
	}
	services := inspectServices(result)
	if got := findInspectServiceEndpoints(services, "auth"); len(got) != 1 || got[0] != "GoogleConnectStart" {
		t.Fatalf("auth endpoints = %#v", got)
	}
}

func findInspectServiceEndpoints(services []inspectdata.ServiceDetails, name string) []string {
	for _, service := range services {
		if service.Name == name {
			return service.Endpoints
		}
	}
	return nil
}
