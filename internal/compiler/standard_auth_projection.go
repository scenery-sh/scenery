package compiler

import (
	"errors"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
)

const standardAuthProjectionModule = "scenery_auth"

// standardAuthProjectionResources describes framework-owned endpoints for
// inspection and client generation. Runtime composition excludes this slice
// because scenery.sh/auth registers the handlers itself.
func frameworkAuthConfig(root string) (appcfg.AuthConfig, error) {
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		if errors.Is(err, appcfg.ErrRootNotFound) {
			return appcfg.AuthConfig{}, nil
		}
		return appcfg.AuthConfig{}, err
	}
	return cfg.Auth, nil
}

func standardAuthProjectionResources(cfg appcfg.AuthConfig, resources []Resource) []Resource {
	if !cfg.Enabled || len(cfg.Connections) == 0 {
		return nil
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
		return nil
	}
	sort.Slice(gateways, func(i, j int) bool { return gateways[i].Address < gateways[j].Address })

	resources = connectionProjectionResources(cfg, authentication, gateways)
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })
	return resources
}

func connectionProjectionResources(cfg appcfg.AuthConfig, authentication string, gateways []Resource) []Resource {
	module := standardAuthProjectionModule
	serviceAddress := resourceAddress(module, "service", "auth")
	connectInputAddress := resourceAddress(module, "record", "connection_start_input")
	providerInputAddress := resourceAddress(module, "record", "connection_provider_input")
	connectResponseAddress := resourceAddress(module, "record", "connection_start_response")
	connectionStatusAddress := resourceAddress(module, "enum", "connection_status")
	connectionResponseAddress := resourceAddress(module, "record", "connection_response")
	connectOperationAddress := resourceAddress(module, "operation", "connection_start")
	getOperationAddress := resourceAddress(module, "operation", "connection_status")
	disconnectOperationAddress := resourceAddress(module, "operation", "connection_disconnect")

	framework := Origin{Kind: "framework"}
	projected := []Resource{
		{Address: serviceAddress, Kind: "scenery.service", Name: "auth", Module: module, Spec: map[string]any{}, Origin: framework},
		{Address: connectInputAddress, Kind: "scenery.record", Name: "connection_start_input", Module: module, Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "provider", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "scopes", "type": map[string]any{"$expression": "list(string)"}},
				map[string]any{"name": "redirect_path", "type": map[string]any{"$expression": "optional(string)"}},
			},
			"unknown_fields": "reject",
		}, Origin: framework},
		{Address: providerInputAddress, Kind: "scenery.record", Name: "connection_provider_input", Module: module, Spec: map[string]any{"field": map[string]any{"name": "provider", "type": map[string]any{"$ref": "string"}}, "unknown_fields": "reject"}, Origin: framework},
		{Address: connectResponseAddress, Kind: "scenery.record", Name: "connection_start_response", Module: module, Spec: map[string]any{
			"field":          map[string]any{"name": "authorize_url", "type": map[string]any{"$ref": "string"}},
			"unknown_fields": "reject",
		}, Origin: framework},
		{Address: connectionStatusAddress, Kind: "scenery.enum", Name: "connection_status", Module: module, Spec: map[string]any{
			"value": []any{
				map[string]any{"name": "active"},
				map[string]any{"name": "reauth_required"},
				map[string]any{"name": "disconnected"},
			},
		}, Origin: framework},
		{Address: connectionResponseAddress, Kind: "scenery.record", Name: "connection_response", Module: module, Spec: map[string]any{
			"field": []any{
				map[string]any{"name": "provider", "type": map[string]any{"$ref": "string"}},
				map[string]any{"name": "issuer", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "subject", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "resource", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "revocation_status", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "status", "type": map[string]any{"$ref": connectionStatusAddress}},
				map[string]any{"name": "email", "type": map[string]any{"$expression": "optional(string)"}},
				map[string]any{"name": "scopes", "type": map[string]any{"$expression": "optional(list(string))"}},
				map[string]any{"name": "connected_at", "type": map[string]any{"$expression": "optional(datetime)"}},
				map[string]any{"name": "last_refresh_at", "type": map[string]any{"$expression": "optional(datetime)"}},
				map[string]any{"name": "reauth_reason", "type": map[string]any{"$expression": "optional(string)"}},
			},
			"unknown_fields": "reject",
		}, Origin: framework},
		frameworkOperation(connectOperationAddress, "connection_start", "ConnectionStart", serviceAddress, connectInputAddress, connectResponseAddress),
		frameworkOperation(getOperationAddress, "connection_status", "ConnectionStatus", serviceAddress, providerInputAddress, connectionResponseAddress),
		frameworkOperation(disconnectOperationAddress, "connection_disconnect", "ConnectionDisconnect", serviceAddress, providerInputAddress, connectionResponseAddress),
	}

	for _, gateway := range gateways {
		suffix := gateway.Name
		projected = append(projected,
			frameworkBinding(module, "connection_start_"+suffix+"_http", connectOperationAddress, gateway.Address, authentication, "POST", "/auth/connections/{provider}/start", true),
			frameworkBinding(module, "connection_status_"+suffix+"_http", getOperationAddress, gateway.Address, authentication, "GET", "/auth/connections/{provider}", false),
			frameworkBinding(module, "connection_disconnect_"+suffix+"_http", disconnectOperationAddress, gateway.Address, authentication, "POST", "/auth/connections/{provider}/disconnect", false),
		)
	}
	for _, provider := range cfg.Connections {
		name := "connection_callback_" + provider.ID
		operation := resourceAddress(module, "operation", name)
		projected = append(projected, Resource{
			Address: operation, Kind: "scenery.operation", Name: name, Module: module, Origin: framework,
			Spec: map[string]any{"service": map[string]any{"$ref": serviceAddress}, "handler": map[string]any{"method": "ConnectionCallback_" + provider.ID}},
		})
		for _, gateway := range gateways {
			// Browser callbacks are inspected as opaque framework HTTP endpoints,
			// which the existing client projection excludes from typed calls.
			projected = append(projected, Resource{
				Address: resourceAddress(module, "binding", name+"_"+gateway.Name+"_http"), Kind: "scenery.binding", Name: name + "_" + gateway.Name + "_http", Module: module, Origin: framework,
				Spec: map[string]any{
					"gateway": map[string]any{"$ref": gateway.Address}, "operation": map[string]any{"$ref": operation}, "protocol": "http", "delivery": "call",
					"authentication": map[string]any{"$ref": "std.authentication.none"}, "authorization": map[string]any{"$ref": "std.authorization.public"}, "pipeline": map[string]any{"$ref": "std.pipeline.empty"},
					"http": map[string]any{"method": "GET", "path": "/auth/connections/" + provider.ID + "/callback", "guarantee": "opaque"},
				},
			})
		}
	}
	return projected
}

func frameworkOperation(address, name, method, service, input, output string) Resource {
	var errors []any
	for _, name := range []string{"unavailable", "invalid_request", "unauthenticated", "forbidden", "internal"} {
		errors = append(errors, map[string]any{"name": name, "type": map[string]any{"$ref": "std.type.problem"}})
	}
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
			"error":   errors,
		},
		Origin: Origin{Kind: "framework"},
	}
}

func frameworkBinding(module, name, operation, gateway, authentication, method, path string, hasBody bool) Resource {
	operationName := operation[strings.LastIndex(operation, "/")+1:]
	httpSpec := map[string]any{
		"path_parameter": map[string]any{"name": "provider", "to": map[string]any{"$ref": "operation." + operationName + ".input.provider"}},
		"method":         method,
		"path":           path,
		"guarantee":      "framework_enforced",
		"response": []any{
			frameworkResponse("success", "result.success", "200", "json"),
			frameworkResponse("invalid_request", "transport.invalid_request", "400", "problem_json"),
			frameworkResponse("invalid_request", "error.invalid_request", "400", "problem_json"),
			frameworkResponse("unauthenticated", "admission.unauthenticated", "401", "problem_json"),
			frameworkResponse("unauthenticated", "error.unauthenticated", "401", "problem_json"),
			frameworkResponse("forbidden", "admission.forbidden", "403", "problem_json"),
			frameworkResponse("forbidden", "error.forbidden", "403", "problem_json"),
			frameworkResponse("internal", "system.internal", "500", "problem_json"),
			frameworkResponse("internal", "error.internal", "500", "problem_json"),
			frameworkResponse("unavailable", "error.unavailable", "503", "problem_json"),
		},
	}
	if hasBody {
		httpSpec["body"] = map[string]any{
			"codec":   "json",
			"to":      map[string]any{"$ref": "operation.connection_start.input"},
			"include": []any{map[string]any{"$ref": "operation.connection_start.input.scopes"}, map[string]any{"$ref": "operation.connection_start.input.redirect_path"}},
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
	switch when {
	case "result.success":
		from = "result.success"
	case "admission.unauthenticated", "admission.forbidden":
		from = "admission.problem"
	case "system.internal":
		from = "system.problem"
	}
	if strings.HasPrefix(when, "error.") {
		from = when
	}
	body := map[string]any{"codec": codec, "from": map[string]any{"$ref": from}}
	if strings.HasPrefix(when, "error.") {
		// Standard-auth domain errors use errs.HTTPError's JSON envelope.
		body["produced_media_types"] = []any{"application/json"}
	}
	return map[string]any{
		"name":   name,
		"when":   map[string]any{"$ref": when},
		"status": status,
		"body":   body,
	}
}
