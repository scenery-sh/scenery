package main

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	cp "scenery.sh/internal/clientprojection"
	"scenery.sh/internal/graph"
	workerruntime "scenery.sh/runtime"
)

var workerPathParameter = regexp.MustCompile(`\{([a-z][a-z0-9_]*)(\.\.\.)?\}`)

func (p workerPlanProjection) httpBinding(binding, operation graph.Resource, policy *workerruntime.ContractHTTPPolicy) (workerruntime.WorkerHTTPPlan, error) {
	spec, _ := binding.Spec["http"].(map[string]any)
	gateway := p.resources[workerReference(binding, binding.Spec["gateway"], "http_gateway")]
	if gateway.Address == "" {
		return workerruntime.WorkerHTTPPlan{}, fmt.Errorf("HTTP gateway is unavailable")
	}
	path := strings.TrimRight(cp.StringValue(gateway.Spec["base_path"]), "/") + cp.StringValue(spec["path"])
	plan := workerruntime.WorkerHTTPPlan{
		Method: cp.StringValue(spec["method"]), Access: workerruntime.Public, Responses: make(map[string]workerruntime.WorkerResponsePlan),
		Path: workerPathParameter.ReplaceAllStringFunc(path, func(match string) string {
			if name, tail := strings.CutSuffix(match[1:len(match)-1], "..."); tail {
				return "*" + name
			}
			return ":" + match[1:len(match)-1]
		}),
	}
	switch cp.StringValue(binding.Spec["exposure"]) {
	case "application", "local":
		plan.Access = workerruntime.Private
	default:
		if workerRefText(binding.Spec["authentication"]) != "std.authentication.none" {
			plan.Access = workerruntime.Auth
		}
	}
	if tails := cp.NamedChildren(spec, "path_tail"); len(tails) == 1 {
		tail := tails[0]
		minimum, _ := cp.IntegerValue(tail["minimum_segments"])
		plan.PathTail = &workerruntime.ContractPathTail{
			CanonicalTemplate: path, Name: cp.StringValue(tail["name"]), Target: workerRefText(tail["to"]),
			Type: cp.StringValue(tail["target_type"]), EmptyCapture: cp.StringValue(tail["empty_capture"]),
			MinimumSegments: minimum, Decoding: cp.StringValue(tail["decoding"]), Guarantee: cp.StringValue(tail["guarantee"]),
			Precedence: []string{"literal", "parameter", "exact_end", "path_tail"},
		}
	}
	requestLimits, _ := spec["request_limit"].(map[string]any)
	responseLimits, _ := spec["response_limit"].(map[string]any)
	timeouts, _ := spec["timeouts"].(map[string]any)
	policy.GatewayAddress = gateway.Address
	policy.CORS = cp.LastRef(workerRefText(gateway.Spec["cors"]))
	policy.Forwarded = cp.LastRef(workerRefText(gateway.Spec["forwarded"]))
	if value, ok := gateway.Spec["cors"].(map[string]any); ok {
		policy.AllowedOrigins = workerStrings(value["origins"])
	}
	policy.TrustedProxyPrefixes = workerStrings(gateway.Spec["trusted_proxies"])
	if value, ok := gateway.Spec["trusted_proxies"].(map[string]any); ok {
		policy.TrustedProxyPrefixes = workerStrings(value["prefixes"])
	}
	policy.MaxRequestHeaderBytes = workerInt(requestLimits["header_bytes"])
	policy.MaxResponseBytes = workerInt(responseLimits["body_bytes"])
	policy.CompressionAlgorithms = workerStrings(responseLimits["compression_algorithms"])
	policy.CompressionThreshold = workerInt(responseLimits["compression_threshold_bytes"])
	if timeout := workerDuration(timeouts["total_invocation"]); timeout > 0 && (policy.TotalInvocationTimeoutNanos == 0 || timeout < policy.TotalInvocationTimeoutNanos) {
		policy.TotalInvocationTimeoutNanos = timeout
	}
	policy.ReadTimeoutNanos = workerDuration(timeouts["read"])
	policy.WriteTimeoutNanos = workerDuration(timeouts["write"])
	policy.IdleTimeoutNanos = workerDuration(timeouts["idle"])
	policy.FrameworkGuarantee = cp.StringValue(spec["guarantee"])
	policy.TransportStatuses = make(map[string]int)
	request, err := p.httpRequest(spec, operation)
	if err != nil {
		return plan, err
	}
	request.TransportStatuses = policy.TransportStatuses
	plan.Request = request
	for _, response := range cp.NamedChildren(spec, "response") {
		outcome := workerRefText(response["when"])
		status := int(workerInt(response["status"]))
		if !strings.HasPrefix(outcome, "result.") && !strings.HasPrefix(outcome, "error.") && outcome != "dispatch.enqueued" {
			policy.TransportStatuses[outcome] = status
			continue
		}
		mapped, err := p.httpResponse(response, outcome, operation, policy, cp.StringValue(binding.Spec["delivery"]) == "stream")
		if err != nil {
			return plan, err
		}
		plan.Responses[outcome] = mapped
	}
	return plan, nil
}

func (p workerPlanProjection) httpRequest(spec map[string]any, operation graph.Resource) (workerruntime.ContractRequestSchema, error) {
	var request workerruntime.ContractRequestSchema
	shape := cp.ResolveOperationInputShape(p.resources, operation)
	fieldTarget := func(raw any) (cp.OperationInputField, bool) {
		field, ok := strings.CutPrefix(workerRefText(raw), "operation."+operation.Name+".input.")
		target, present := shape.Fields[field]
		return target, ok && present
	}
	for _, source := range []struct{ block, input string }{
		{"path_parameter", workerruntime.ContractSourcePath}, {"path_tail", workerruntime.ContractSourcePathTail},
		{"query_parameter", workerruntime.ContractSourceQuery}, {"header", workerruntime.ContractSourceHeader}, {"cookie", workerruntime.ContractSourceCookie},
	} {
		for _, mapping := range cp.NamedChildren(spec, source.block) {
			field, ok := fieldTarget(mapping["to"])
			if !ok {
				return request, fmt.Errorf("invalid %s mapping", source.block)
			}
			input := workerInputField(field)
			input.Source, input.Name, input.Encoding = source.input, cp.StringValue(mapping["name"]), cp.StringValue(mapping["encoding"])
			request.Mappings = append(request.Mappings, input)
		}
	}
	for _, mapping := range cp.NamedChildren(spec, "context") {
		field, ok := fieldTarget(mapping["to"])
		if !ok {
			return request, fmt.Errorf("invalid context mapping")
		}
		request.ContextMappings = append(request.ContextMappings, workerruntime.ContractContextMapping{Source: workerRefText(mapping["from"]), Target: field.WireName})
	}
	body, _ := spec["body"].(map[string]any)
	if body == nil {
		return request, nil
	}
	requestLimits, _ := spec["request_limit"].(map[string]any)
	mapping := &workerruntime.ContractBodyMapping{
		Codec: cp.StringValue(body["codec"]), Type: cp.TypeExpression(operation.Spec["input"]),
		AcceptedMediaTypes: workerStrings(body["accepted_media_types"]), SupportedContentEncoding: workerStrings(body["content_encodings"]),
	}
	if workerRefText(body["to"]) != "operation."+operation.Name+".input" {
		field, ok := fieldTarget(body["to"])
		if !ok {
			return request, fmt.Errorf("invalid body mapping")
		}
		mapping.Target, mapping.Type = field.WireName, cp.TypeExpression(field.Type)
	}
	mapping.Include = workerBodySelection(body["include"], operation, shape)
	mapping.Except = workerBodySelection(body["except"], operation, shape)
	mapping.MaxCompressedBytes = workerBodyLimit(body, requestLimits, "max_compressed_bytes", "body_bytes")
	mapping.MaxDecompressedBytes = workerBodyLimit(body, requestLimits, "max_decompressed_bytes", "decompressed_body_bytes")
	switch mapping.Codec {
	case "form":
		for _, field := range shape.Fields {
			if includesWorkerField(field.WireName, mapping.Include, mapping.Except) {
				mapping.Fields = append(mapping.Fields, workerInputField(field))
			}
		}
	case "multipart":
		mapping.MaxCompressedBytes = workerBodyLimit(body, requestLimits, "max_compressed_bytes", "multipart_body_bytes")
		mapping.MaxDecompressedBytes = workerBodyLimit(body, requestLimits, "max_decompressed_bytes", "multipart_body_bytes")
		mapping.MaxMultipartParts = int(workerBodyLimit(body, requestLimits, "max_parts", "multipart_parts"))
		for _, part := range cp.NamedChildren(body, "part") {
			field, ok := fieldTarget(part["to"])
			if !ok {
				return request, fmt.Errorf("invalid multipart mapping")
			}
			kind := cp.StringValue(part["kind"])
			limit := "multipart_non_file_part_bytes"
			if kind == "file" {
				limit = "multipart_file_part_bytes"
			}
			mapping.MultipartParts = append(mapping.MultipartParts, workerruntime.ContractMultipartPart{
				Name: cp.StringValue(part["name"]), Target: field.WireName, Type: workerInputField(field).Type, Kind: kind,
				MediaTypes: workerStrings(part["media_types"]), MaxBytes: workerBodyLimit(part, requestLimits, "max_bytes", limit),
				Multiple: part["multiple"] == true, Optional: field.Optional, RetainFilename: part["retain_filename"] == true,
			})
		}
	}
	request.Body = mapping
	return request, nil
}

func workerInputField(field cp.OperationInputField) workerruntime.ContractInputMapping {
	expression := cp.TypeExpression(field.Type)
	if len(field.EnumValues) > 0 {
		expression = workerEnumType.ReplaceAllString(expression, "string")
	}
	return workerruntime.ContractInputMapping{Name: field.WireName, Target: field.WireName, Type: expression, Optional: field.Optional, EnumValues: field.EnumValues}
}

var workerEnumType = regexp.MustCompile(`enum\.[a-z][a-z0-9_]*`)

func workerBodySelection(value any, operation graph.Resource, shape cp.OperationInputShape) []string {
	var result []string
	items, _ := value.([]any)
	for _, item := range items {
		field := strings.TrimPrefix(workerRefText(item), "operation."+operation.Name+".input.")
		if found, ok := shape.Fields[field]; ok {
			result = append(result, found.WireName)
		}
	}
	return result
}

func includesWorkerField(field string, include, exclude []string) bool {
	for _, candidate := range exclude {
		if candidate == field {
			return false
		}
	}
	for _, candidate := range include {
		if candidate == field {
			return true
		}
	}
	return len(include) == 0
}

func workerInt(value any) int64 {
	integer, _ := cp.IntegerValue(value)
	return int64(integer)
}

func workerBodyLimit(specific, defaults map[string]any, key, fallback string) int64 {
	if value, ok := cp.IntegerValue(specific[key]); ok {
		return int64(value)
	}
	return workerInt(defaults[fallback])
}

func workerStrings(value any) []string {
	items, _ := value.([]any)
	var result []string
	for _, item := range items {
		if text := cp.StringValue(item); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func (p workerPlanProjection) httpResponse(spec map[string]any, outcome string, operation graph.Resource, policy *workerruntime.ContractHTTPPolicy, stream bool) (workerruntime.WorkerResponsePlan, error) {
	result := workerruntime.WorkerResponsePlan{
		Status: int(workerInt(spec["status"])),
		Options: workerruntime.ContractResponseOptions{
			MaxBytes: policy.MaxResponseBytes, CompressionAlgorithms: policy.CompressionAlgorithms, CompressionThreshold: policy.CompressionThreshold,
		},
	}
	if result.Status < http.StatusContinue || result.Status > 599 {
		return result, fmt.Errorf("outcome %s has no valid HTTP status", outcome)
	}
	for _, header := range cp.NamedChildren(spec, "header") {
		value, err := p.httpResponseValue(operation, outcome, workerRefText(header["from"]))
		if err != nil {
			return result, err
		}
		result.Headers = append(result.Headers, workerruntime.WorkerResponseHeader{
			Name: cp.StringValue(header["name"]), Encoding: cp.StringValue(header["encoding"]), Value: value,
		})
	}
	for _, cookie := range cp.NamedChildren(spec, "cookie") {
		value, err := p.httpResponseValue(operation, outcome, workerRefText(cookie["from"]))
		if err != nil {
			return result, err
		}
		result.Cookies = append(result.Cookies, workerruntime.WorkerResponseCookie{Cookie: workerResponseCookie(cookie), Value: value})
	}
	body, _ := spec["body"].(map[string]any)
	if body == nil {
		return result, nil
	}
	result.Codec = cp.StringValue(body["codec"])
	result.Media = workerStrings(body["produced_media_types"])
	result.Stream = stream && strings.HasPrefix(outcome, "result.") && result.Codec == "bytes"
	if len(result.Media) == 0 {
		defaults := map[string]string{"problem_json": "application/problem+json", "text": "text/plain", "bytes": "application/octet-stream", "json": "application/json"}
		result.Media = []string{defaults[result.Codec]}
	}
	var err error
	result.Body, err = p.httpResponseValue(operation, outcome, workerRefText(body["from"]))
	result.Body.Optional = false
	return result, err
}

func workerResponseCookie(spec map[string]any) workerruntime.ContractResponseCookie {
	secure, secureSet := spec["secure"].(bool)
	httpOnly, httpOnlySet := spec["http_only"].(bool)
	sameSite := http.SameSiteLaxMode
	switch cp.StringValue(spec["same_site"]) {
	case "strict":
		sameSite = http.SameSiteStrictMode
	case "none":
		sameSite = http.SameSiteNoneMode
	}
	return workerruntime.ContractResponseCookie{
		Name: cp.StringValue(spec["name"]), Path: cp.StringValue(spec["path"]), Domain: cp.StringValue(spec["domain"]),
		MaxAge: int(workerInt(spec["max_age"])), Expires: cp.StringValue(spec["expires"]),
		Secure: secure || !secureSet, HTTPOnly: httpOnly || !httpOnlySet, SameSite: sameSite,
	}
}

func (p workerPlanProjection) httpResponseValue(operation graph.Resource, outcome, target string) (workerruntime.WorkerResponseValue, error) {
	var result workerruntime.WorkerResponseValue
	root := outcome
	if outcome == "dispatch.enqueued" {
		root = "dispatch.receipt"
	}
	var path string
	if target != root {
		var valid bool
		path, valid = strings.CutPrefix(target, root+".")
		if !valid {
			return result, fmt.Errorf("response value %s does not select outcome %s", target, outcome)
		}
	}
	if outcome == "dispatch.enqueued" {
		switch path {
		case "":
			return result, nil
		case "durable_identity", "execution_id", "accepted_revision", "status_url":
			return workerruntime.WorkerResponseValue{Path: []string{path}, DefaultEmpty: path == "status_url"}, nil
		}
		return result, fmt.Errorf("response value %s has no receipt wire mapping", target)
	}
	kind, name, _ := strings.Cut(outcome, ".")
	var typeExpression string
	for _, variant := range cp.NamedChildren(operation.Spec, kind) {
		if cp.StringValue(variant["name"]) == name {
			typeExpression = workerTypeExpression(operation.Module, variant["type"])
			break
		}
	}
	if path == "" {
		result.Optional = strings.HasPrefix(strings.TrimSpace(typeExpression), "optional(")
		return result, nil
	}
	if typeExpression == "std.type.problem" {
		switch path {
		case "code", "message", "path":
			return workerruntime.WorkerResponseValue{Path: []string{path}, DefaultEmpty: path == "path"}, nil
		}
	}
	for field := range strings.SplitSeq(path, ".") {
		record := p.resources[typeExpression]
		found := false
		for _, declaration := range cp.NamedChildren(record.Spec, "field") {
			if cp.StringValue(declaration["name"]) == field {
				result.Path = append(result.Path, cp.WireName(declaration, field))
				typeExpression = workerTypeExpression(record.Module, declaration["type"])
				found = true
				break
			}
		}
		if !found {
			return result, fmt.Errorf("response field %s has no wire mapping", field)
		}
	}
	result.Optional = strings.HasPrefix(strings.TrimSpace(typeExpression), "optional(")
	return result, nil
}
