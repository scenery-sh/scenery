package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
	inspectdata "scenery.sh/internal/inspect"
	"scenery.sh/internal/postgresdb"
)

func buildInspectAppResponse(appRoot string, cfg appcfg.Config, result *compiler.Result) inspectdata.AppResponse {
	services := inspectServices(result)
	endpoints, _ := inspectEndpoints(result)
	middleware, runtimeDeclarations := 0, 0
	for _, resource := range result.Manifest.Resources {
		switch resource.Kind {
		case "scenery.middleware":
			middleware++
		case "scenery.execution":
			if inspectString(resource.Spec["mode"]) == "durable" {
				runtimeDeclarations++
			}
		case "scenery.schedule":
			runtimeDeclarations++
		}
	}
	names := make([]string, 0, len(services))
	for _, service := range services {
		names = append(names, service.Name)
	}
	response := inspectdata.AppResponse{
		PayloadIdentity: inspectdata.NewPayloadIdentity("scenery.inspect.app", "app,config,counts,services,auth_handler"), App: inspectAppRef(appRoot, cfg, result), Config: cfg,
		Counts: inspectdata.AppCounts{Packages: len(services), Services: len(services), Endpoints: len(endpoints), Middleware: middleware, RuntimeDeclarations: runtimeDeclarations}, Services: names,
	}
	for _, service := range services {
		if service.Name == "auth" {
			response.Counts.AuthHandler = 1
			response.AuthHandler = &inspectdata.AuthBriefInfo{Service: "auth", Name: "AuthHandler"}
			break
		}
	}
	return response
}

func buildInspectServicesResponse(appRoot string, cfg appcfg.Config, result *compiler.Result) inspectdata.ServicesResponse {
	return inspectdata.ServicesResponse{PayloadIdentity: inspectdata.NewPayloadIdentity("scenery.inspect.services", "app,services"), App: inspectAppRef(appRoot, cfg, result), Services: inspectServices(result)}
}

func buildInspectRoutesResponse(appRoot string, cfg appcfg.Config, result *compiler.Result) (inspectdata.RoutesResponse, error) {
	endpoints, err := inspectEndpoints(result)
	if err != nil {
		return inspectdata.RoutesResponse{}, err
	}
	serviceRoots := inspectServiceRoots(result)
	routes := make([]inspectdata.RouteRecord, 0, len(endpoints))
	for _, endpoint := range endpoints {
		root := serviceRoots[endpoint.Service]
		routes = append(routes, inspectdata.RouteRecord{
			ID: endpoint.ID, Service: endpoint.Service, Endpoint: endpoint.Endpoint, Package: root,
			File: endpoint.File, Access: endpoint.Access, Raw: endpoint.Raw, Path: endpoint.Path, Methods: append([]string(nil), endpoint.Methods...),
			Tags: append([]string(nil), endpoint.Tags...), Receiver: endpoint.Receiver, Generated: endpoint.Generated, HasPayload: endpoint.HasPayload,
		})
	}
	return inspectdata.RoutesResponse{PayloadIdentity: inspectdata.NewPayloadIdentity("scenery.inspect.routes", "app,routes"), App: inspectAppRef(appRoot, cfg, result), Routes: routes}, nil
}

func buildInspectEndpointsResponse(appRoot string, cfg appcfg.Config, result *compiler.Result) (inspectdata.EndpointsResponse, error) {
	endpoints, err := inspectEndpoints(result)
	if err != nil {
		return inspectdata.EndpointsResponse{}, err
	}
	return inspectdata.EndpointsResponse{PayloadIdentity: inspectdata.NewPayloadIdentity("scenery.inspect.endpoints", "app,endpoints"), App: inspectAppRef(appRoot, cfg, result), Endpoints: endpoints}, nil
}

func buildInspectDurableResponse(appRoot string, cfg appcfg.Config, result *compiler.Result) inspectDurableResponse {
	resources := map[string]graph.Resource{}
	for _, resource := range result.Manifest.Resources {
		resources[resource.Address] = resource
	}
	sourcePaths := map[string]string{}
	for _, source := range result.Sources {
		sourcePaths[source.ID] = source.Relative
	}
	declarations := []durableDeclaration{}
	for _, execution := range result.Manifest.Resources {
		if execution.Kind != "scenery.execution" || inspectString(execution.Spec["mode"]) != "durable" {
			continue
		}
		operation := resources[inspectResolveReference(execution, inspectReference(execution.Spec["operation"]), "operation")]
		service := resources[inspectResolveReference(operation, inspectReference(operation.Spec["service"]), "service")]
		serviceName := service.Name
		if serviceName == "" {
			serviceName = execution.Module
		}
		name := inspectString(execution.Spec["external_name"])
		if name == "" {
			name = execution.Name
		}
		schema := serviceName
		if database, ok := cfg.DatabaseService(serviceName); ok {
			schema = database.Schema
		}
		file := sourcePaths[execution.Origin.SourceID]
		line := 0
		input, output := inspectTypeExpression(operation.Spec["input"]), inspectOperationOutputType(operation)
		if execution.Origin.DeclarationRange != nil {
			line = execution.Origin.DeclarationRange.Start.Line
		}
		declarations = append(declarations, durableDeclaration{
			Kind: "durable_task", Name: name, Service: serviceName, Schema: schema, File: file, Line: line,
			Input: input, Output: output,
		})
	}
	sort.Slice(declarations, func(i, j int) bool {
		if declarations[i].Service != declarations[j].Service {
			return declarations[i].Service < declarations[j].Service
		}
		return declarations[i].Name < declarations[j].Name
	})
	services := durableServices(declarations)
	databaseURL := durableDatabaseURLForInspect(appRoot, cfg)
	return inspectDurableResponse{
		cliPayloadIdentity: newCLIPayloadIdentity("scenery.inspect.durable"), App: inspectAppRef(appRoot, cfg, result),
		Durable:      inspectDurableRecord{Database: inspectDurableDatabase{Name: postgresdb.DatabaseNameFromURL(databaseURL), URL: postgresdb.RedactURL(databaseURL)}, Schema: "scenery", TaskCount: len(declarations), ServiceCount: len(services)},
		Declarations: declarations, Services: services,
	}
}

func inspectTypeExpression(value any) string {
	if reference := inspectReference(value); reference != "" {
		return reference
	}
	if expression, ok := value.(map[string]any); ok {
		return inspectString(expression["$expression"])
	}
	return inspectString(value)
}

func inspectOperationOutputType(operation graph.Resource) string {
	results := inspectNamedChildren(operation.Spec["result"])
	if len(results) == 0 {
		return ""
	}
	return inspectTypeExpression(results[0]["type"])
}

func inspectNamedChildren(value any) []map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return []map[string]any{typed}
	case []any:
		result := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if child, ok := item.(map[string]any); ok {
				result = append(result, child)
			}
		}
		return result
	default:
		return nil
	}
}

func inspectServices(result *compiler.Result) []inspectdata.ServiceDetails {
	if result == nil || result.Manifest == nil {
		return []inspectdata.ServiceDetails{}
	}
	resources := map[string]graph.Resource{}
	for _, resource := range result.Manifest.Resources {
		resources[resource.Address] = resource
	}
	operations := map[string][]string{}
	middleware := map[string][]string{}
	for _, resource := range result.Manifest.Resources {
		switch resource.Kind {
		case "scenery.operation":
			handler, _ := resource.Spec["handler"].(map[string]any)
			name := inspectString(handler["method"])
			if name == "" {
				name = resource.Name
			}
			service := resources[inspectResolveReference(resource, inspectReference(resource.Spec["service"]), "service")]
			serviceName := service.Name
			if serviceName == "" {
				serviceName = resource.Module
			}
			operations[serviceName] = append(operations[serviceName], name)
		case "scenery.middleware":
			middleware[resource.Module] = append(middleware[resource.Module], resource.Name)
		}
	}
	roots := inspectServiceRoots(result)
	var services []inspectdata.ServiceDetails
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.service" {
			continue
		}
		root := roots[resource.Name]
		if root == "" {
			root = roots[resource.Module]
		}
		service := inspectdata.ServiceDetails{
			Name: resource.Name, RootRelDir: root, RootAbsDir: filepath.Join(result.Root, filepath.FromSlash(strings.TrimPrefix(root, "./"))),
			PackageDirs: []string{root}, Endpoints: canonicalInspectStrings(operations[resource.Name]), Middleware: canonicalInspectStrings(middleware[resource.Module]),
		}
		if resource.Name == "auth" {
			service.AuthHandler = &inspectdata.AuthHandlerRef{Name: "AuthHandler", Package: "scenery.sh/auth"}
		}
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool { return services[i].Name < services[j].Name })
	return services
}

func inspectEndpoints(result *compiler.Result) ([]inspectdata.EndpointRecord, error) {
	if result == nil || result.Manifest == nil {
		return []inspectdata.EndpointRecord{}, nil
	}
	resources := map[string]graph.Resource{}
	sourcePaths := map[string]string{}
	for _, source := range result.Sources {
		sourcePaths[source.ID] = source.Relative
	}
	for _, resource := range result.Manifest.Resources {
		resources[resource.Address] = resource
	}
	type endpointGroup struct {
		record  inspectdata.EndpointRecord
		methods map[string]bool
	}
	groups := map[string]*endpointGroup{}
	for _, binding := range result.Manifest.Resources {
		if binding.Kind != "scenery.binding" {
			continue
		}
		protocol := inspectString(binding.Spec["protocol"])
		if protocol != "http" {
			continue
		}
		operation := resources[inspectResolveReference(binding, inspectReference(binding.Spec["operation"]), "operation")]
		if operation.Kind != "scenery.operation" {
			return nil, fmt.Errorf("binding %s has no operation", binding.Address)
		}
		service := resources[inspectResolveReference(operation, inspectReference(operation.Spec["service"]), "service")]
		if service.Kind != "scenery.service" {
			return nil, fmt.Errorf("operation %s has no service", operation.Address)
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		endpointName := inspectString(handler["method"])
		if endpointName == "" {
			endpointName = operation.Name
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		method, route := strings.ToUpper(inspectString(httpSpec["method"])), inspectString(httpSpec["path"])
		if method == "" || route == "" {
			return nil, fmt.Errorf("HTTP binding %s has no method or path", binding.Address)
		}
		gateway := resources[inspectResolveReference(binding, inspectReference(binding.Spec["gateway"]), "http_gateway")]
		effectivePath := inspectJoinPath(inspectString(gateway.Spec["base_path"]), route)
		access := inspectBindingAccess(binding)
		_, hasPayload := httpSpec["body"]
		key := service.Name + "\x00" + endpointName + "\x00" + effectivePath + "\x00" + access
		group := groups[key]
		if group == nil {
			group = &endpointGroup{record: inspectdata.EndpointRecord{
				ID: service.Name + "." + endpointName, Service: service.Name, Endpoint: endpointName, Access: access,
				Path: effectivePath, Generated: operation.Origin.Kind == "expanded", HasPayload: hasPayload,
				File: sourcePaths[operation.Origin.SourceID],
			}, methods: map[string]bool{}}
			groups[key] = group
		}
		group.methods[method] = true
	}
	endpoints := make([]inspectdata.EndpointRecord, 0, len(groups))
	for _, group := range groups {
		group.record.Methods = canonicalInspectStrings(mapKeys(group.methods))
		endpoints = append(endpoints, group.record)
	}
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].Service != endpoints[j].Service {
			return endpoints[i].Service < endpoints[j].Service
		}
		if endpoints[i].Endpoint != endpoints[j].Endpoint {
			return endpoints[i].Endpoint < endpoints[j].Endpoint
		}
		return endpoints[i].Path < endpoints[j].Path
	})
	return endpoints, nil
}

func inspectServiceRoots(result *compiler.Result) map[string]string {
	roots := map[string]string{}
	if result == nil || result.Manifest == nil {
		return roots
	}
	for _, resource := range result.Manifest.Resources {
		if resource.Kind != "scenery.module" {
			continue
		}
		root := inspectString(resource.Spec["workspace_package_root"])
		if root == "" {
			root = inspectString(resource.Spec["source"])
		}
		roots[resource.Name] = strings.TrimPrefix(root, "./")
	}
	return roots
}

func inspectAppRef(appRoot string, cfg appcfg.Config, result *compiler.Result) inspectdata.AppRef {
	modulePath := ""
	for _, resource := range result.Manifest.Resources {
		if resource.Kind == "scenery.go-module" {
			modulePath = inspectString(resource.Spec["import_path"])
			break
		}
	}
	return inspectdata.AppRef{Name: cfg.Name, ID: cfg.ID, Root: appRoot, ConfigPath: cfg.SourcePath(appRoot), ModulePath: modulePath}
}

func inspectBindingAccess(binding graph.Resource) string {
	authentication := inspectReference(binding.Spec["authentication"])
	if authentication == "" {
		authentication = inspectString(binding.Spec["authentication"])
	}
	if authentication == "public" || strings.HasSuffix(authentication, ".none") {
		return "public"
	}
	return "auth"
}

func inspectResolveReference(resource graph.Resource, reference, kind string) string {
	if strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) == 2 {
		module := resource.Module
		if parts[0] == "app" {
			module, parts = "app", []string{kind, parts[1]}
		}
		return module + "/" + parts[0] + "/" + parts[1]
	}
	return ""
}

func inspectReference(value any) string {
	object, _ := value.(map[string]any)
	reference, _ := object["$ref"].(string)
	return reference
}

func inspectString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if scalar, ok := value.(map[string]any); ok {
		if text, ok := scalar["value"].(string); ok {
			return text
		}
	}
	return ""
}

func inspectJoinPath(base, child string) string {
	base, child = strings.TrimSuffix(base, "/"), strings.TrimPrefix(child, "/")
	if base == "" || base == "/" {
		return "/" + child
	}
	return base + "/" + child
}

func canonicalInspectStrings(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	result := mapKeys(seen)
	sort.Strings(result)
	return result
}

func mapKeys(values map[string]bool) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	return result
}
