package openapiprojection

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
)

// Options controls which gateways and endpoints are projected into OpenAPI 3.1.1 documents.
type Options struct {
	// Gateway filters to a single gateway by name, "http_gateway.<name>", or full resource address.
	Gateway string
	// IncludeFramework includes framework-owned HTTP bindings (such as standard-auth connection routes).
	IncludeFramework bool
	// Title overrides the generated OpenAPI info.title when non-empty.
	Title string
	// Version overrides the generated OpenAPI info.version when non-empty.
	Version string
}

// DocumentResult holds one projected OpenAPI 3.1.1 document and its gateway metadata.
type DocumentResult struct {
	GatewayAddress      string
	GatewayName         string
	BasePath            string
	OpenAPIVersion      string
	OpenAPIRevision     string
	HTTPSurfaceRevision string
	Operations          int
	Document            map[string]any
}

var pathTailSegmentPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\.\.\.\}`)

// Project builds deterministic OpenAPI 3.1.1 documents for HTTP gateways in manifest.
func Project(manifest *graph.Manifest, frameworkResources []graph.Resource, httpRevisions, openAPIRevisions map[string]string, opts Options) ([]DocumentResult, error) {
	if manifest == nil {
		return nil, fmt.Errorf("invalid_request: compiled manifest is required")
	}
	allResources := make([]graph.Resource, 0, len(manifest.Resources)+len(frameworkResources))
	allResources = append(allResources, manifest.Resources...)
	if opts.IncludeFramework {
		allResources = append(allResources, frameworkResources...)
	}
	byAddress := make(map[string]graph.Resource, len(allResources))
	var gateways []graph.Resource
	for _, resource := range allResources {
		byAddress[resource.Address] = resource
		if resource.Kind == "scenery.http-gateway" {
			gateways = append(gateways, resource)
		}
	}
	sort.Slice(gateways, func(i, j int) bool { return gateways[i].Address < gateways[j].Address })

	selectedGateways, err := selectGateways(gateways, opts.Gateway)
	if err != nil {
		return nil, err
	}

	results := make([]DocumentResult, 0, len(selectedGateways))
	for _, gateway := range selectedGateways {
		doc, opCount, err := projectGateway(manifest, gateway, allResources, byAddress, openAPIRevisions[gateway.Address], len(gateways), opts)
		if err != nil {
			return nil, err
		}
		results = append(results, DocumentResult{
			GatewayAddress:      gateway.Address,
			GatewayName:         gateway.Name,
			BasePath:            cp.StringValue(gateway.Spec["base_path"]),
			OpenAPIVersion:      OpenAPIVersion,
			OpenAPIRevision:     openAPIRevisions[gateway.Address],
			HTTPSurfaceRevision: httpRevisions[gateway.Address],
			Operations:          opCount,
			Document:            doc,
		})
	}
	return results, nil
}

func selectGateways(gateways []graph.Resource, filter string) ([]graph.Resource, error) {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return gateways, nil
	}
	for _, gateway := range gateways {
		if gateway.Name == filter || gateway.Address == filter || "http_gateway."+gateway.Name == filter {
			return []graph.Resource{gateway}, nil
		}
	}
	return nil, fmt.Errorf("http gateway %q not found", filter)
}

func projectGateway(
	manifest *graph.Manifest,
	gateway graph.Resource,
	allResources []graph.Resource,
	byAddress map[string]graph.Resource,
	openAPIRevision string,
	totalGateways int,
	opts Options,
) (map[string]any, int, error) {
	bindings := bindingsForGateway(allResources, gateway)
	builder := newSchemaBuilder(byAddress)

	// Pre-compute unique operation IDs deterministically.
	opIDs := assignOperationIDs(bindings, byAddress)
	paths := map[string]any{}
	tagSet := map[string]bool{}
	usesAuth := false

	type boundRoute struct {
		path    string
		method  string
		binding graph.Resource
	}
	basePath := cp.StringValue(gateway.Spec["base_path"])
	routes := make([]boundRoute, 0, len(bindings))
	for _, binding := range bindings {
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		method := strings.ToLower(cp.StringValue(httpSpec["method"]))
		rawPath := cp.StringValue(httpSpec["path"])
		if method == "" || rawPath == "" {
			continue
		}
		effectivePath := openAPIPathTemplate(joinHTTPPath(basePath, rawPath))
		routes = append(routes, boundRoute{path: effectivePath, method: method, binding: binding})
	}
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		if routes[i].method != routes[j].method {
			return routes[i].method < routes[j].method
		}
		return routes[i].binding.Address < routes[j].binding.Address
	})

	for _, route := range routes {
		operation, opTag, needsAuth, err := buildOperation(route.binding, byAddress, builder, opIDs[route.binding.Address])
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", route.binding.Address, err)
		}
		if opTag != "" {
			tagSet[opTag] = true
		}
		if needsAuth {
			usesAuth = true
		}
		pathItem, ok := paths[route.path].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			paths[route.path] = pathItem
		}
		pathItem[route.method] = operation
	}

	title := strings.TrimSpace(opts.Title)
	if title == "" {
		title = defaultDocumentTitle(manifest.Application.Name, gateway.Name, totalGateways)
	}
	version := strings.TrimSpace(opts.Version)
	if version == "" {
		version = defaultDocumentVersion(openAPIRevision)
	}

	doc := map[string]any{
		"openapi":           OpenAPIVersion,
		"jsonSchemaDialect": JSONSchemaDialect,
		"info": map[string]any{
			"title":   title,
			"version": version,
		},
		"paths": paths,
	}

	if len(tagSet) > 0 {
		tagNames := make([]string, 0, len(tagSet))
		for tag := range tagSet {
			tagNames = append(tagNames, tag)
		}
		sort.Strings(tagNames)
		tags := make([]map[string]any, 0, len(tagNames))
		for _, name := range tagNames {
			tags = append(tags, map[string]any{"name": name})
		}
		doc["tags"] = tags
	}

	components := map[string]any{}
	if len(builder.schemas) > 0 {
		schemas := make(map[string]any, len(builder.schemas))
		for name, schema := range builder.schemas {
			schemas[name] = schema
		}
		components["schemas"] = schemas
	}
	if usesAuth {
		components["securitySchemes"] = map[string]any{
			"bearerAuth": map[string]any{
				"type":         "http",
				"scheme":       "bearer",
				"bearerFormat": "JWT",
			},
			"sessionCookie": map[string]any{
				"type": "apiKey",
				"in":   "cookie",
				"name": "app_access",
			},
		}
	}
	if len(components) > 0 {
		doc["components"] = components
	}
	return doc, len(routes), nil
}

func bindingsForGateway(resources []graph.Resource, gateway graph.Resource) []graph.Resource {
	var bindings []graph.Resource
	for _, resource := range resources {
		if resource.Kind != "scenery.binding" || cp.StringValue(resource.Spec["protocol"]) != "http" {
			continue
		}
		target := cp.ResolveResourceRef(resource, cp.RefString(resource.Spec["gateway"]), "http_gateway")
		if target == gateway.Address {
			bindings = append(bindings, resource)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}

func assignOperationIDs(bindings []graph.Resource, byAddress map[string]graph.Resource) map[string]string {
	opCounts := map[string]int{}
	nameCounts := map[string]int{}
	for _, binding := range bindings {
		opAddr := cp.ResolveResourceRef(binding, cp.RefString(binding.Spec["operation"]), "operation")
		opCounts[opAddr]++
		if op, ok := byAddress[opAddr]; ok {
			nameCounts[op.Name]++
		}
	}
	used := map[string]bool{}
	ids := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		opAddr := cp.ResolveResourceRef(binding, cp.RefString(binding.Spec["operation"]), "operation")
		op, hasOp := byAddress[opAddr]
		base := binding.Name
		if hasOp {
			base = op.Name
			if nameCounts[op.Name] > opCounts[opAddr] {
				base = op.Module + "_" + op.Name
			}
		}
		id := cp.TSName(base)
		if hasOp && opCounts[opAddr] > 1 {
			id += "Via" + cp.GoName(binding.Name)
		}
		id = neutralOperationID(id)
		if id == "" {
			id = "operation"
		}
		candidate := id
		for suffix := 2; used[candidate]; suffix++ {
			candidate = fmt.Sprintf("%s%d", id, suffix)
		}
		used[candidate] = true
		ids[binding.Address] = candidate
	}
	return ids
}

func buildOperation(
	binding graph.Resource,
	byAddress map[string]graph.Resource,
	builder *schemaBuilder,
	operationID string,
) (map[string]any, string, bool, error) {
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	opAddr := cp.ResolveResourceRef(binding, cp.RefString(binding.Spec["operation"]), "operation")
	operation, hasOp := byAddress[opAddr]

	tag := operationTag(binding, operation, hasOp)
	op := map[string]any{
		"operationId": operationID,
	}
	if tag != "" {
		op["tags"] = []string{tag}
	}

	summary := cp.StringValue(binding.Spec["summary"])
	description := cp.StringValue(binding.Spec["description"])
	if hasOp {
		if summary == "" {
			summary = cp.StringValue(operation.Spec["summary"])
		}
		if description == "" {
			description = cp.StringValue(operation.Spec["description"])
		}
	}
	if summary == "" {
		if hasOp {
			summary = humanizeName(operation.Name)
		} else {
			summary = humanizeName(binding.Name)
		}
	}
	if summary != "" {
		op["summary"] = summary
	}
	if description != "" && description != summary {
		op["description"] = description
	}
	if binding.Spec["deprecated"] == true || (hasOp && operation.Spec["deprecated"] == true) {
		op["deprecated"] = true
	}

	authRef := cp.RefString(binding.Spec["authentication"])
	needsAuth := authRef != "" && authRef != "std.authentication.none"
	if needsAuth {
		op["security"] = []map[string]any{
			{"bearerAuth": []string{}},
			{"sessionCookie": []string{}},
		}
	}

	// Opaque framework bindings (such as OAuth browser callbacks) have no typed
	// request/response codec contract; project them concisely without failing.
	if cp.StringValue(httpSpec["guarantee"]) == "opaque" || !hasOp {
		op["responses"] = map[string]any{
			"default": map[string]any{
				"description": "Opaque HTTP response.",
			},
		}
		return op, tag, needsAuth, nil
	}

	shape := cp.ResolveOperationInputShape(byAddress, operation)
	params, err := buildParameters(httpSpec, operation, shape, builder)
	if err != nil {
		return nil, "", false, err
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	reqBody, err := buildRequestBody(httpSpec, operation, shape, builder)
	if err != nil {
		return nil, "", false, err
	}
	if reqBody != nil {
		op["requestBody"] = reqBody
	}

	responses, err := buildResponses(binding, httpSpec, operation, byAddress, builder)
	if err != nil {
		return nil, "", false, err
	}
	op["responses"] = responses
	return op, tag, needsAuth, nil
}

func operationTag(binding, operation graph.Resource, hasOp bool) string {
	if hasOp {
		if svc := cp.LastRef(cp.RefString(operation.Spec["service"])); svc != "" {
			return stripFrameworkBrand(svc)
		}
		if operation.Module != "" {
			return stripFrameworkBrand(operation.Module)
		}
	}
	return stripFrameworkBrand(binding.Module)
}

func defaultDocumentTitle(application, gatewayName string, totalGateways int) string {
	app := strings.TrimSpace(stripFrameworkBrand(humanizeName(application)))
	gw := strings.TrimSpace(stripFrameworkBrand(humanizeName(gatewayName)))
	switch {
	case app != "" && totalGateways > 1 && gw != "":
		return app + " - " + gw + " API"
	case app != "":
		return app + " API"
	case gw != "":
		return gw + " API"
	default:
		return "HTTP API"
	}
}

func defaultDocumentVersion(openAPIRevision string) string {
	trimmed := strings.TrimPrefix(strings.TrimSpace(openAPIRevision), "sha256:")
	if len(trimmed) >= 12 {
		return "0.0.0+" + trimmed[:12]
	}
	return "0.0.0"
}

func joinHTTPPath(basePath, routePath string) string {
	basePath = strings.TrimSpace(basePath)
	routePath = strings.TrimSpace(routePath)
	if basePath == "" || basePath == "/" {
		return routePath
	}
	basePath = strings.TrimRight(basePath, "/")
	if routePath == "" || routePath == "/" {
		return basePath
	}
	return basePath + "/" + strings.TrimLeft(routePath, "/")
}

func openAPIPathTemplate(path string) string {
	return pathTailSegmentPattern.ReplaceAllString(path, `{$1}`)
}

func humanizeName(value string) string {
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '_' || r == '-' || r == '.' || r == '/' })
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

func neutralOperationID(value string) string {
	cleaned := neutralSchemaIdent(value)
	if cleaned == "" {
		return ""
	}
	return strings.ToLower(cleaned[:1]) + cleaned[1:]
}
