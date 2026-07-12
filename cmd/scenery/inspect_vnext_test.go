package main

import (
	"io"
	"strings"
	"testing"

	appcfg "scenery.sh/internal/app"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/vnext"
)

func TestVNextCommandsRejectUnsupportedOutputBeforeWork(t *testing.T) {
	tests := []struct {
		name string
		run  func(io.Writer, []string) error
		args []string
	}{
		{name: "changes", run: runVNextChanges, args: []string{"rename", "house/record/scene", "renamed", "-o", "yaml"}},
		{name: "graph", run: runVNextGraph, args: []string{"house/record/scene", "-o", "yaml"}},
		{name: "generate", run: runVNextGenerate, args: []string{"-o", "yaml"}},
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

func TestVNextInspectDurableProjectsMergedExecution(t *testing.T) {
	result := &vnext.Result{Root: t.TempDir(), Manifest: &vnext.Manifest{Resources: []vnext.Resource{
		{Address: "house/service/house", Module: "house", Kind: "scenery.service/v1", Name: "house", Spec: map[string]any{}},
		{Address: "house/operation/process", Module: "house", Kind: "scenery.operation/v1", Name: "process", Spec: map[string]any{
			"service": map[string]any{"$ref": "house/service/house"}, "input": map[string]any{"$ref": "record.input"},
			"result": map[string]any{"name": "done", "type": map[string]any{"$ref": "record.output"}},
		}},
		{Address: "house/execution/process", Module: "house", Kind: "scenery.execution/v1", Name: "process", Spec: map[string]any{
			"operation": map[string]any{"$ref": "house/operation/process"}, "mode": "durable", "external_name": "ProcessScene",
		}},
	}}}
	response := buildVNextInspectDurableResponse(result.Root, appcfg.Config{Name: "test"}, result)
	if response.Durable.TaskCount != 1 || len(response.Declarations) != 1 {
		t.Fatalf("durable response = %#v", response)
	}
	declaration := response.Declarations[0]
	if declaration.Name != "ProcessScene" || declaration.Service != "house" || declaration.Input != "record.input" || declaration.Output != "record.output" {
		t.Fatalf("durable declaration = %#v", declaration)
	}
}

func TestVNextInspectProjectsNativeServiceAndPrivateEndpointIdentity(t *testing.T) {
	t.Parallel()

	resources := []vnext.Resource{
		{Address: "app/service/auth", Kind: "scenery.service/v1", Name: "auth", Module: "app", Spec: map[string]any{}},
		{Address: "app/service/users", Kind: "scenery.service/v1", Name: "users", Module: "app", Spec: map[string]any{}},
		{Address: "app/http_gateway/public_api", Kind: "scenery.http-gateway/v1", Name: "public_api", Module: "app", Spec: map[string]any{"base_path": "/"}},
		{
			Address: "app/operation/dev_bootstrap", Kind: "scenery.operation/v1", Name: "dev_bootstrap", Module: "app",
			Spec: map[string]any{"service": map[string]any{"$ref": "app/service/users"}, "input": map[string]any{"$ref": "record.input"}, "handler": map[string]any{"method": "DevBootstrap"}},
		},
		{
			Address: "app/binding/dev_bootstrap_http", Kind: "scenery.binding/v1", Name: "dev_bootstrap_http", Module: "app",
			Spec: map[string]any{
				"protocol": "http", "operation": map[string]any{"$ref": "app/operation/dev_bootstrap"}, "gateway": map[string]any{"$ref": "app/http_gateway/public_api"},
				"authentication": map[string]any{"$ref": "std.authentication.none"}, "http": map[string]any{"method": "POST", "path": "/users/dev-bootstrap"},
			},
			Origin: vnext.Origin{Kind: "authored"},
		},
	}
	result := &vnext.Result{Manifest: &vnext.Manifest{Resources: resources}}

	services := vnextInspectServices(result)
	if got := findInspectServiceEndpoints(services, "users"); len(got) != 1 || got[0] != "DevBootstrap" {
		t.Fatalf("users endpoints = %#v, want [DevBootstrap]", got)
	}
	if got := findInspectServiceEndpoints(services, "auth"); len(got) != 0 {
		t.Fatalf("auth endpoints = %#v, want none", got)
	}

	endpoints, err := vnextInspectEndpoints(result)
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

func findInspectServiceEndpoints(services []inspectdata.ServiceDetails, name string) []string {
	for _, service := range services {
		if service.Name == name {
			return service.Endpoints
		}
	}
	return nil
}
