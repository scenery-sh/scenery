package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
)

func buildDevMetadata(root string) (json.RawMessage, json.RawMessage, error) {
	result, err := compiler.Compile(root)
	if err != nil {
		return nil, nil, err
	}
	if !result.Valid() {
		return nil, nil, fmt.Errorf("app contract graph is invalid: %s", firstCompilerDiagnostic(result.Diagnostics))
	}
	endpoints, err := inspectEndpoints(result)
	if err != nil {
		return nil, nil, err
	}
	byService := map[string][]map[string]any{}
	apiByService := map[string][]map[string]any{}
	for _, endpoint := range endpoints {
		path := metadataPath(endpoint.Path)
		byService[endpoint.Service] = append(byService[endpoint.Service], map[string]any{
			"name": endpoint.Endpoint, "doc": "", "service_name": endpoint.Service,
			"access_type": endpoint.Access, "proto": "REGULAR", "path": path,
			"http_methods": endpoint.Methods, "request_schema": nil, "response_schema": nil, "tags": []any{},
		})
		apiByService[endpoint.Service] = append(apiByService[endpoint.Service], map[string]any{
			"name": endpoint.Endpoint, "path": endpoint.Path, "methods": endpoint.Methods,
			"raw": false, "access_type": endpoint.Access, "service_name": endpoint.Service,
		})
	}
	services := inspectServices(result)
	metadataServices := make([]map[string]any, 0, len(services))
	apiServices := make([]map[string]any, 0, len(services))
	for _, service := range services {
		metadataServices = append(metadataServices, map[string]any{
			"name": service.Name, "rel_path": service.RootRelDir, "rpcs": byService[service.Name],
			"migrations": []any{}, "databases": []any{}, "has_config": false, "buckets": []any{}, "metrics": []any{},
		})
		apiServices = append(apiServices, map[string]any{"name": service.Name, "rpcs": apiByService[service.Name]})
	}
	sort.Slice(metadataServices, func(i, j int) bool {
		return metadataServices[i]["name"].(string) < metadataServices[j]["name"].(string)
	})
	sort.Slice(apiServices, func(i, j int) bool { return apiServices[i]["name"].(string) < apiServices[j]["name"].(string) })
	metadata, err := json.Marshal(map[string]any{
		"module_path": "", "app_revision": result.Manifest.ContractRevision, "uncommitted_changes": false,
		"decls": []any{}, "pkgs": []any{}, "svcs": metadataServices, "cron_jobs": []any{}, "middleware": []any{},
		"cache_clusters": []any{}, "experiments": []any{}, "metrics": []any{}, "sql_databases": []any{},
		"gateways": []any{}, "buckets": []any{}, "language": "GO",
	})
	if err != nil {
		return nil, nil, err
	}
	apiEncoding, err := json.Marshal(map[string]any{"services": apiServices})
	if err != nil {
		return nil, nil, err
	}
	return metadata, apiEncoding, nil
}

func metadataPath(value string) map[string]any {
	segments := []map[string]any{}
	for _, part := range strings.Split(strings.Trim(value, "/"), "/") {
		if part == "" {
			continue
		}
		segment := map[string]any{"type": "LITERAL", "value": part, "value_type": "STRING"}
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			name := strings.TrimSuffix(strings.TrimPrefix(part, "{"), "}")
			name = strings.TrimSuffix(name, "...")
			segment = map[string]any{"type": "PARAM", "value": name, "value_type": "STRING"}
		}
		segments = append(segments, segment)
	}
	return map[string]any{"type": "URL", "segments": segments}
}
