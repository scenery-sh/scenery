package compiler

import (
	"errors"
	"sort"

	appcfg "scenery.sh/internal/app"
)

const standardAuthProjectionModule = "scenery_auth"

// standardAuthProjectionResources describes framework-owned endpoints for
// inspection and client generation. Runtime composition excludes this slice
// because scenery.sh/auth registers the handlers itself.
func standardAuthProjectionResources(root string, resources []Resource) ([]Resource, error) {
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		if errors.Is(err, appcfg.ErrRootNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !cfg.Auth.Enabled || !cfg.Auth.GoogleOAuth.Enabled {
		return nil, nil
	}

	authentication := ""
	var gateways []Resource
	for _, resource := range resources {
		switch resource.Kind {
		case "scenery.authentication":
			if refString(resource.Spec["provider"]) == "std.provider.standard_auth" {
				authentication = resource.Address
			}
		case "scenery.http-gateway":
			gateways = append(gateways, resource)
		}
	}
	if authentication == "" || len(gateways) == 0 {
		return nil, nil
	}
	sort.Slice(gateways, func(i, j int) bool { return gateways[i].Address < gateways[j].Address })

	resources = googleConnectionProjectionResources(authentication, gateways)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	return resources, nil
}

func googleConnectionProjectionResources(authentication string, gateways []Resource) []Resource {
	module := standardAuthProjectionModule
	serviceAddress := resourceAddress(module, "service", "auth")
	connectInputAddress := resourceAddress(module, "record", "google_connect_start_input")
	connectResponseAddress := resourceAddress(module, "record", "google_connect_start_response")
	connectionStatusAddress := resourceAddress(module, "enum", "google_connection_status")
	connectionResponseAddress := resourceAddress(module, "record", "google_connection_response")
	connectOperationAddress := resourceAddress(module, "operation", "google_connect_start")
	getOperationAddress := resourceAddress(module, "operation", "get_google_connection")
	disconnectOperationAddress := resourceAddress(module, "operation", "disconnect_google_connection")

	framework := Origin{Kind: "framework"}
	projected := []Resource{
		{Address: serviceAddress, Kind: "scenery.service", Name: "auth", Module: module, Spec: map[string]any{}, Origin: framework},
		{Address: connectInputAddress, Kind: "scenery.record", Name: "google_connect_start_input", Module: module, Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "scopes", "type": map[string]any{"$expression": "list(string)"}},
				map[string]any{"name": "redirect_path", "type": map[string]any{"$expression": "optional(string)"}},
			},
			"unknown_fields": "reject",
		}, Origin: framework},
		{Address: connectResponseAddress, Kind: "scenery.record", Name: "google_connect_start_response", Module: module, Spec: map[string]any{
			"field":          map[string]any{"name": "authorize_url", "type": map[string]any{"$ref": "string"}},
			"unknown_fields": "reject",
		}, Origin: framework},
		{Address: connectionStatusAddress, Kind: "scenery.enum", Name: "google_connection_status", Module: module, Spec: map[string]any{
			"value": []any{
				map[string]any{"name": "active"},
				map[string]any{"name": "reauth_required"},
				map[string]any{"name": "disconnected"},
			},
		}, Origin: framework},
		{Address: connectionResponseAddress, Kind: "scenery.record", Name: "google_connection_response", Module: module, Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "status", "type": map[string]any{"$ref": connectionStatusAddress}},
				map[string]any{"name": "email", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "scopes", "type": map[string]any{"$expression": "optional(list(string))"}},
				map[string]any{"name": "connected_at", "type": map[string]any{"$expression": "optional(datetime)"}},
				map[string]any{"name": "last_refresh_at", "type": map[string]any{"$expression": "optional(datetime)"}},
				map[string]any{"name": "reauth_reason", "type": map[string]any{"$expression": "optional(string)"}},
			},
			"unknown_fields": "reject",
		}, Origin: framework},
		frameworkOperation(connectOperationAddress, "google_connect_start", "GoogleConnectStart", serviceAddress, connectInputAddress, connectResponseAddress),
		frameworkOperation(getOperationAddress, "get_google_connection", "GetGoogleConnection", serviceAddress, "std.type.unit", connectionResponseAddress),
		frameworkOperation(disconnectOperationAddress, "disconnect_google_connection", "DisconnectGoogleConnection", serviceAddress, "std.type.unit", connectionResponseAddress),
	}

	for _, gateway := range gateways {
		suffix := gateway.Name
		projected = append(projected,
			frameworkBinding(module, "google_connect_start_"+suffix+"_http", connectOperationAddress, gateway.Address, authentication, "POST", "/auth/google/connect/start", true),
			frameworkBinding(module, "get_google_connection_"+suffix+"_http", getOperationAddress, gateway.Address, authentication, "GET", "/auth/google/connection", false),
			frameworkBinding(module, "disconnect_google_connection_"+suffix+"_http", disconnectOperationAddress, gateway.Address, authentication, "POST", "/auth/google/connection/disconnect", false),
		)
	}
	return projected
}

func frameworkOperation(address, name, method, service, input, output string) Resource {
	return Resource{
		Address: address,
		Kind:    "scenery.operation",
		Name:    name,
		Module:  standardAuthProjectionModule,
		Spec: map[string]any{
			"service": map[string]any{"$ref": service},
			"input":   map[string]any{"$ref": input},
			"handler": map[string]any{"method": method},
			"result":  map[string]any{"name": "success", "type": map[string]any{"$ref": output}},
		},
		Origin: Origin{Kind: "framework"},
	}
}

func frameworkBinding(module, name, operation, gateway, authentication, method, path string, hasBody bool) Resource {
	httpSpec := map[string]any{
		"method":    method,
		"path":      path,
		"guarantee": "framework_enforced",
		"response": []any{
			frameworkResponse("success", "result.success", "200", "json"),
			frameworkResponse("invalid_request", "transport.invalid_request", "400", "problem_json"),
			frameworkResponse("unauthenticated", "admission.unauthenticated", "401", "problem_json"),
			frameworkResponse("forbidden", "admission.forbidden", "403", "problem_json"),
			frameworkResponse("internal", "system.internal", "500", "problem_json"),
		},
	}
	if hasBody {
		httpSpec["body"] = map[string]any{
			"codec": "json",
			"to":    map[string]any{"$ref": "operation.google_connect_start.input"},
		}
	}
	return Resource{
		Address: resourceAddress(module, "binding", name),
		Kind:    "scenery.binding",
		Name:    name,
		Module:  module,
		Spec: map[string]any{
			"gateway":        map[string]any{"$ref": gateway},
			"operation":      map[string]any{"$ref": operation},
			"protocol":       "http",
			"delivery":       "call",
			"authentication": map[string]any{"$ref": authentication},
			"authorization":  map[string]any{"$ref": "std.authorization.public"},
			"pipeline":       map[string]any{"$ref": "std.pipeline.empty"},
			"http":           httpSpec,
		},
		Origin: Origin{Kind: "framework"},
	}
}

func frameworkResponse(name, when, status, codec string) map[string]any {
	from := "transport.problem"
	switch {
	case when == "result.success":
		from = "result.success"
	case when == "admission.unauthenticated" || when == "admission.forbidden":
		from = "admission.problem"
	case when == "system.internal":
		from = "system.problem"
	}
	return map[string]any{
		"name":   name,
		"when":   map[string]any{"$ref": when},
		"status": status,
		"body": map[string]any{
			"codec": codec,
			"from":  map[string]any{"$ref": from},
		},
	}
}
