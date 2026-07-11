package main

import (
	"io"
	"os"
	"path/filepath"
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

func TestIsVNextGenerateFindsAncestorAndIgnoresOptionValues(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "scenery.scn"), []byte("language { edition = \"2027\" }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(root, "nested", "client")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Chdir(nested)
	for _, args := range [][]string{nil, {"--app-root", "client"}, {"--target", "client"}} {
		if !isVNextGenerate(args) {
			t.Fatalf("isVNextGenerate(%q) = false", args)
		}
	}
	for _, args := range [][]string{{"client"}, {"--lang", "typescript", "client"}, {"--output", "out", "client"}} {
		if isVNextGenerate(args) {
			t.Fatalf("legacy generate %q routed to vNext", args)
		}
	}
}

func TestVNextInspectProjectsLegacyServiceAndPrivateEndpointIdentity(t *testing.T) {
	t.Parallel()

	resources := []vnext.Resource{
		{Address: "app/service/auth", Kind: "scenery.service/v1", Name: "auth", Module: "app", Spec: map[string]any{}},
		{Address: "app/service/users", Kind: "scenery.service/v1", Name: "users", Module: "app", Spec: map[string]any{}},
		{Address: "app/http_gateway/public_api", Kind: "scenery.http-gateway/v1", Name: "public_api", Module: "app", Spec: map[string]any{"base_path": "/"}},
		{
			Address: "app/operation/dev_bootstrap", Kind: "scenery.operation/v1", Name: "dev_bootstrap", Module: "app",
			Spec: map[string]any{"service": map[string]any{"$ref": "app/service/users"}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": "DevBootstrap"}},
		},
		{
			Address: "app/binding/dev_bootstrap_http", Kind: "scenery.binding/v1", Name: "dev_bootstrap_http", Module: "app",
			Spec: map[string]any{
				"protocol": "http", "operation": map[string]any{"$ref": "app/operation/dev_bootstrap"}, "gateway": map[string]any{"$ref": "app/http_gateway/public_api"},
				"authentication": map[string]any{"$ref": "std.authentication.none"}, "http": map[string]any{"method": "POST", "path": "/users/dev-bootstrap"},
			},
			Origin: vnext.Origin{Kind: "legacy_v0", LegacyIdentity: map[string]any{"path": "/users/dev-bootstrap", "methods": []string{"POST"}, "access": "public"}},
		},
		{Address: "audit/service/audit", Kind: "scenery.service/v1", Name: "audit", Module: "audit", Spec: map[string]any{}},
		{
			Address: "audit/operation/prune_old_logs", Kind: "scenery.operation/v1", Name: "prune_old_logs", Module: "audit",
			Spec: map[string]any{"service": map[string]any{"$ref": "audit/service/audit"}, "handler": map[string]any{"method": "PruneOldLogs"}},
		},
		{
			Address: "audit/binding/prune_old_logs_internal", Kind: "scenery.binding/v1", Name: "prune_old_logs_internal", Module: "audit",
			Spec:   map[string]any{"protocol": "internal", "operation": map[string]any{"$ref": "audit/operation/prune_old_logs"}},
			Origin: vnext.Origin{Kind: "legacy_v0", LegacyIdentity: map[string]any{"path": "/audit/prune-old-logs", "methods": []string{"POST"}, "access": "private"}},
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
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %#v, want 2", endpoints)
	}
	if endpoints[0].ID != "audit.PruneOldLogs" || endpoints[0].Access != "private" || endpoints[0].Path != "/audit/prune-old-logs" {
		t.Fatalf("private endpoint = %#v", endpoints[0])
	}
	if endpoints[1].ID != "users.DevBootstrap" || endpoints[1].Access != "public" {
		t.Fatalf("standard auth endpoint = %#v", endpoints[1])
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
