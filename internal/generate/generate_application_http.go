package generate

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	scenery "scenery.sh/internal/contract"
)

func renderServiceOperationAdapters(b *strings.Builder, operations, bindings []Resource) error {
	for _, operation := range operations {
		handler, _ := operation.Spec["handler"].(map[string]any)
		method := stringValue(handler["method"])
		if method == "" {
			return fmt.Errorf("operation %s has no handler method", operation.Address)
		}
		operationName := goName(operation.Name)
		if operationUsesHTTPStream(operation, bindings) {
			fmt.Fprintf(b, "func (adapter *serviceAdapter) %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, scenery.ByteStream, error) {\n", method, operationName, operationName)
			b.WriteString("\tif adapter == nil || adapter.native == nil { return nil, scenery.ByteStream{}, fmt.Errorf(\"service is not initialized\") }\n\tnative := adapter.native\n")
			fmt.Fprintf(b, "\treturn native.%s(ctx, input)\n}\n\n", method)
		} else {
			fmt.Fprintf(b, "func (adapter *serviceAdapter) %s(ctx context.Context, input contract.%sInput) (contract.%sOutcome, error) {\n", method, operationName, operationName)
			b.WriteString("\tif adapter == nil || adapter.native == nil { return nil, fmt.Errorf(\"service is not initialized\") }\n\tnative := adapter.native\n")
			fmt.Fprintf(b, "\treturn native.%s(ctx, input)\n}\n\n", method)
		}
	}
	return nil
}

func operationUsesHTTPStream(operation Resource, bindings []Resource) bool {
	for _, binding := range bindings {
		if stringValue(binding.Spec["protocol"]) == "http" && stringValue(binding.Spec["delivery"]) == "stream" && resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation") == operation.Address {
			return true
		}
	}
	return false
}

func renderHTTPBindingRegistration(b *strings.Builder, resources []Resource, service, operation, binding Resource) error {
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	method, path := stringValue(httpSpec["method"]), stringValue(httpSpec["path"])
	if method == "" || path == "" {
		return fmt.Errorf("HTTP binding %s has no method or path", binding.Address)
	}
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	requestSchema, err := renderContractRequestSchema(resourceMap, operation, binding, httpSpec)
	if err != nil {
		return err
	}
	operationName := goName(operation.Name)
	handler, _ := operation.Spec["handler"].(map[string]any)
	handlerMethod := stringValue(handler["method"])
	endpointName := goName(binding.Name)
	contractPolicy := renderContractHTTPPolicy(resourceMap, binding, httpSpec)
	delivery := stringValue(binding.Spec["delivery"])
	execution, executionOK := executionForBinding(resourceMap, binding)
	fmt.Fprintf(b, "\t\t\tif err := sceneryruntime.RegisterEndpointChecked(&sceneryruntime.Endpoint{Service: %q, Name: %q, Access: %s, Path: %q, Methods: []string{%q},\n", service.Name, endpointName, runtimeAccess(binding), runtimeBindingPath(resourceMap, binding, path), method)
	fmt.Fprintf(b, "\t\t\t\tContractPolicy: %s,\n", contractPolicy)
	if pathTail := renderContractPathTail(resourceMap, binding, httpSpec); pathTail != "nil" {
		fmt.Fprintf(b, "\t\t\t\tContractPathTail: %s,\n", pathTail)
	}
	fmt.Fprintf(b, "\t\t\t\tDecodeContractRequest: func(request *http.Request, pathValues map[string]string) (sceneryruntime.ContractDecodedRequest, error) { input, err := sceneryruntime.DecodeContractInput[contract.%sInput](request, pathValues, %s); return sceneryruntime.ContractDecodedRequest{Payload: input}, err },\n", operationName, requestSchema)
	if delivery == "enqueue" {
		if !executionOK || stringValue(execution.Spec["mode"]) != "durable" {
			return fmt.Errorf("enqueue HTTP binding %s does not select a durable execution", binding.Address)
		}
		status := http.StatusServiceUnavailable
		if response, ok := responseMappings(httpSpec)["dispatch.rejected"]; ok {
			if configured, err := strconv.Atoi(stringValue(response["status"])); err == nil {
				status = configured
			}
		}
		fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context, _ []any, payload any) (any, error) { typed := payload.(contract.%sInput); copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; receipt, err := sceneryruntime.DispatchContractDurableExecutionWithOptions(ctx, %q, copied, options); if err != nil { return nil, &sceneryruntime.ContractTransportError{Outcome: \"dispatch.rejected\", Status: %d, Message: \"durable dispatch rejected\", Cause: err} }; return receipt, nil },\n", operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address, status)
	} else if delivery == "wait" {
		if !executionOK || stringValue(execution.Spec["mode"]) != "durable" {
			return fmt.Errorf("wait HTTP binding %s does not select a durable execution", binding.Address)
		}
		statuses := map[string]int{"dispatch.rejected": http.StatusServiceUnavailable, "dispatch.wait_timeout": http.StatusGatewayTimeout}
		for outcome := range statuses {
			if response, ok := responseMappings(httpSpec)[outcome]; ok {
				if configured, err := strconv.Atoi(stringValue(response["status"])); err == nil {
					statuses[outcome] = configured
				}
			}
		}
		fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context, _ []any, payload any) (any, error) { typed := payload.(contract.%sInput); copied, err := contract.Clone%sInput(typed); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; options, err := %s(copied); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; data, err := sceneryruntime.DispatchAndWaitContractDurableExecutionWithOptions(ctx, %q, copied, options); if err != nil { switch sceneryruntime.ContractDurableFailureOutcome(err) { case \"dispatch.rejected\": return nil, &sceneryruntime.ContractTransportError{Outcome: \"dispatch.rejected\", Status: %d, Message: \"durable dispatch rejected\", Cause: err}; case \"dispatch.wait_timeout\": return nil, &sceneryruntime.ContractTransportError{Outcome: \"dispatch.wait_timeout\", Status: %d, Message: \"durable wait timed out\", Cause: err}; default: return nil, sceneryruntime.ContractSystemError(err) } }; outcome, err := contract.Unmarshal%sOutcome(data); if err != nil { return nil, err }; return sceneryruntime.NewContractPreparedOutcome(outcome, data), nil },\n", operationName, operationName, durableDispatchOptionsFunction(execution), execution.Address, statuses["dispatch.rejected"], statuses["dispatch.wait_timeout"], operationName)
	} else if delivery == "stream" {
		fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context, _ []any, payload any) (any, error) { if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; copied, err := contract.Clone%sInput(payload.(contract.%sInput)); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; outcome, stream, err := service.%s(ctx, copied); if err != nil { _ = stream.Close(); if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; ", operationName, operationName, handlerMethod)
		if err := renderHTTPStreamOutcomeSanitizer(b, resourceMap, operation, httpSpec); err != nil {
			return fmt.Errorf("HTTP binding %s stream outcome: %w", binding.Address, err)
		}
		fmt.Fprintf(b, "cloned, err := contract.Clone%sOutcome(outcome); if err != nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.NewContractStreamOutcome(cloned, stream), nil },\n", operationName)
	} else {
		// Marshal the outcome once: the canonical envelope both feeds the
		// isolation clone and rides along so the response encoder can reuse
		// the payload bytes instead of marshaling the outcome again.
		fmt.Fprintf(b, "\t\t\t\tInvoke: func(ctx context.Context, _ []any, payload any) (any, error) { if service == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"service is not initialized\")) }; copied, err := contract.Clone%sInput(payload.(contract.%sInput)); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; outcome, err := service.%s(ctx, copied); if err != nil { if outcome != nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned outcome and error\")) }; return nil, sceneryruntime.ContractSystemError(err) }; if outcome == nil { return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome without error\")) }; raw, err := contract.Marshal%sOutcome(outcome); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; cloned, err := contract.Unmarshal%sOutcome(raw); if err != nil { return nil, sceneryruntime.ContractSystemError(err) }; if err := sceneryruntime.PublishContractOperationOutcome(ctx, %q, cloned); err != nil { return nil, sceneryruntime.ContractSystemError(err) }; return sceneryruntime.NewContractPreparedOutcome(cloned, raw), nil },\n", operationName, operationName, handlerMethod, operationName, operationName, operation.Address)
	}
	if delivery == "stream" {
		b.WriteString("\t\t\t\tEncodeContractOutcome: func(request *http.Request, outcome any) (sceneryruntime.ContractHTTPResponse, error) { _ = request; streamed, ok := outcome.(*sceneryruntime.ContractStreamOutcome); if !ok { return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(fmt.Errorf(\"stream handler returned %T\", outcome)) }; switch typed := streamed.Outcome.(type) {\n")
	} else {
		b.WriteString("\t\t\t\tEncodeContractOutcome: func(request *http.Request, outcome any) (sceneryruntime.ContractHTTPResponse, error) { _ = request; outcome, envelope := sceneryruntime.ContractPreparedOutcomeValue(outcome); _ = envelope; switch typed := outcome.(type) {\n")
	}
	responses := responseMappings(httpSpec)
	if delivery == "enqueue" {
		response, ok := responses["dispatch.enqueued"]
		if !ok {
			return fmt.Errorf("HTTP binding %s has no response for dispatch.enqueued", binding.Address)
		}
		status, err := strconv.Atoi(stringValue(response["status"]))
		if err != nil {
			return fmt.Errorf("HTTP binding %s dispatch.enqueued has invalid status", binding.Address)
		}
		if err := renderHTTPResponseCase(b, resourceMap, operation, "dispatch.enqueued", "scenery.ExecutionReceipt", status, "typed", map[string]any{"$ref": "std.type.execution_receipt"}, response, httpSpec, false); err != nil {
			return fmt.Errorf("HTTP binding %s response dispatch.enqueued: %w", binding.Address, err)
		}
		b.WriteString("\t\t\t\tdefault: return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(fmt.Errorf(\"unsupported handler outcome %T\", outcome)) } },\n")
		b.WriteString("\t\t\t}); err != nil { return err }\n")
		return nil
	}
	for _, outcomeKind := range []string{"result", "error"} {
		for _, variant := range namedChildren(operation.Spec, outcomeKind) {
			name := stringValue(variant["name"])
			response, ok := responses[outcomeKind+"."+name]
			if !ok {
				return fmt.Errorf("HTTP binding %s has no response for %s.%s", binding.Address, outcomeKind, name)
			}
			status, err := strconv.Atoi(stringValue(response["status"]))
			if err != nil {
				return fmt.Errorf("HTTP binding %s response %s has invalid status", binding.Address, name)
			}
			wrapper := operationName + goName(name)
			field := "typed.Value"
			if outcomeKind == "error" {
				field = "typed.Problem"
			}
			if err := renderHTTPResponseCase(b, resourceMap, operation, outcomeKind+"."+name, "contract."+wrapper, status, field, variant["type"], response, httpSpec, delivery == "stream"); err != nil {
				return fmt.Errorf("HTTP binding %s response %s: %w", binding.Address, name, err)
			}
		}
	}
	b.WriteString("\t\t\t\tdefault: return sceneryruntime.ContractHTTPResponse{}, fmt.Errorf(\"handler returned an unknown outcome %T\", outcome) } },\n\t\t\t}); err != nil { return err }\n")
	return nil
}

func renderHTTPResponseCase(b *strings.Builder, resources map[string]Resource, operation Resource, outcome, caseType string, status int, rootExpression string, rootType any, response, httpSpec map[string]any, streaming ...bool) error {
	streamResponse := len(streaming) > 0 && streaming[0]
	fmt.Fprintf(b, "\t\t\t\tcase %s:\n", caseType)
	if response["body"] == nil && len(namedChildren(response, "header")) == 0 && len(namedChildren(response, "cookie")) == 0 {
		b.WriteString("\t\t\t\t\t_ = typed\n")
	}
	if responseBody, _ := response["body"].(map[string]any); responseBody != nil {
		valueExpression, valueType, err := httpOutcomeValueExpression(resources, operation, outcome, refOrString(responseBody["from"]), rootExpression, rootType)
		if err != nil {
			return err
		}
		codec := stringValue(responseBody["codec"])
		produced := literalStringListFromValue(responseBody["produced_media_types"])
		if len(produced) == 0 {
			produced = []string{defaultHTTPMediaType(codec)}
		}
		options := renderContractResponseOptions(httpSpec, goWireTypeExpression(valueType))
		if streamResponse && strings.HasPrefix(outcome, "result.") && codec == "bytes" {
			b.WriteString("\t\t\t\t\tstream, err := streamed.TakeStream()\n")
			b.WriteString("\t\t\t\t\tif err != nil { return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(err) }\n")
			fmt.Fprintf(b, "\t\t\t\t\tresponse, err := sceneryruntime.EncodeContractByteStreamWithOptions(request, %d, stream, %#v, %s)\n", status, produced, options)
		} else {
			if streamResponse {
				b.WriteString("\t\t\t\t\tif err := streamed.RequireNoStream(); err != nil { return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(err) }\n")
			}
			if !streamResponse && strings.HasPrefix(caseType, "contract.") && valueExpression == rootExpression && (codec == "json" || codec == "problem_json") {
				// The body is the whole outcome value, so the canonical
				// envelope payload produced by Invoke is byte-identical to
				// the encoded body and can be reused.
				fmt.Fprintf(b, "\t\t\t\t\tresponse, err := sceneryruntime.EncodeContractPreparedRepresentationWithOptions(request, %d, envelope, %s, %q, %#v, %s)\n", status, valueExpression, codec, produced, options)
			} else {
				fmt.Fprintf(b, "\t\t\t\t\tresponse, err := sceneryruntime.EncodeContractRepresentationWithOptions(request, %d, %s, %q, %#v, %s)\n", status, valueExpression, codec, produced, options)
			}
		}
		b.WriteString("\t\t\t\t\tif err != nil { return sceneryruntime.ContractHTTPResponse{}, err }\n")
	} else {
		if streamResponse {
			b.WriteString("\t\t\t\t\tif err := streamed.RequireNoStream(); err != nil { return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(err) }\n")
		}
		fmt.Fprintf(b, "\t\t\t\t\tresponse := sceneryruntime.ContractHTTPResponse{Status: %d}\n", status)
	}
	for _, header := range namedChildren(response, "header") {
		expression, valueType, err := httpOutcomeValueExpression(resources, operation, outcome, refOrString(header["from"]), rootExpression, rootType)
		if err != nil {
			return err
		}
		valueExpression, encodedType, condition := httpResponseMetadataExpression(expression, valueType)
		if condition != "" {
			fmt.Fprintf(b, "\t\t\t\t\tif %s {\n", condition)
		}
		indent := "\t\t\t\t\t"
		if condition != "" {
			indent += "\t"
		}
		fmt.Fprintf(b, "%sif err := sceneryruntime.AddContractResponseHeader(&response, %q, %s, sceneryruntime.ContractResponseValueOptions{Encoding: %q, EncodeValue: func(value any) ([]byte, error) { return scenery.MarshalContractValue(value, %q) }}); err != nil { _ = response.Close(); return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(err) }\n", indent, stringValue(header["name"]), valueExpression, defaultString(stringValue(header["encoding"]), "repeated"), goWireTypeExpression(encodedType))
		if condition != "" {
			b.WriteString("\t\t\t\t\t}\n")
		}
	}
	for _, cookie := range namedChildren(response, "cookie") {
		expression, valueType, err := httpOutcomeValueExpression(resources, operation, outcome, refOrString(cookie["from"]), rootExpression, rootType)
		if err != nil {
			return err
		}
		valueExpression, encodedType, condition := httpResponseMetadataExpression(expression, valueType)
		secure, secureSet := cookie["secure"].(bool)
		if !secureSet {
			secure = true
		}
		httpOnly, httpOnlySet := cookie["http_only"].(bool)
		if !httpOnlySet {
			httpOnly = true
		}
		maxAge, _ := integerValue(cookie["max_age"])
		sameSite := "http.SameSiteLaxMode"
		switch stringValue(cookie["same_site"]) {
		case "strict":
			sameSite = "http.SameSiteStrictMode"
		case "none":
			sameSite = "http.SameSiteNoneMode"
		}
		if condition != "" {
			fmt.Fprintf(b, "\t\t\t\t\tif %s {\n", condition)
		}
		indent := "\t\t\t\t\t"
		if condition != "" {
			indent += "\t"
		}
		fmt.Fprintf(b, "%sif err := sceneryruntime.AddContractResponseCookie(&response, sceneryruntime.ContractResponseCookie{Name: %q, Path: %q, Domain: %q, MaxAge: %d, Expires: %q, Secure: %t, HTTPOnly: %t, SameSite: %s}, %s, sceneryruntime.ContractResponseValueOptions{EncodeValue: func(value any) ([]byte, error) { return scenery.MarshalContractValue(value, %q) }}); err != nil { _ = response.Close(); return sceneryruntime.ContractHTTPResponse{}, sceneryruntime.ContractSystemError(err) }\n", indent, stringValue(cookie["name"]), defaultString(stringValue(cookie["path"]), "/"), stringValue(cookie["domain"]), maxAge, stringValue(cookie["expires"]), secure, httpOnly, sameSite, valueExpression, goWireTypeExpression(encodedType))
		if condition != "" {
			b.WriteString("\t\t\t\t\t}\n")
		}
	}
	b.WriteString("\t\t\t\t\treturn response, nil\n")
	return nil
}

func renderHTTPStreamOutcomeSanitizer(b *strings.Builder, resources map[string]Resource, operation Resource, httpSpec map[string]any) error {
	b.WriteString("switch typed := outcome.(type) { ")
	for _, variant := range namedChildren(operation.Spec, "result") {
		name := stringValue(variant["name"])
		response := responseMappings(httpSpec)["result."+name]
		body, _ := response["body"].(map[string]any)
		if body == nil || stringValue(body["codec"]) != "bytes" {
			continue
		}
		wrapper := goName(operation.Name) + goName(name)
		expression, _, err := httpOutcomeValueExpression(resources, operation, "result."+name, refOrString(body["from"]), "typed.Value", variant["type"])
		if err != nil {
			return err
		}
		fmt.Fprintf(b, "case contract.%s: if len(%s) != 0 { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"stream handler returned buffered bytes\")) }; %s = nil; outcome = typed; ", wrapper, expression, expression)
		pointerExpression := strings.Replace(expression, "typed.", "copied.", 1)
		fmt.Fprintf(b, "case *contract.%s: if typed == nil { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"handler returned nil outcome\")) }; copied := *typed; if len(%s) != 0 { _ = stream.Close(); return nil, sceneryruntime.ContractSystemError(fmt.Errorf(\"stream handler returned buffered bytes\")) }; %s = nil; outcome = &copied; ", wrapper, pointerExpression, pointerExpression)
	}
	b.WriteString("}; ")
	return nil
}

func httpResponseMetadataExpression(expression string, valueType any) (string, any, string) {
	raw := strings.TrimSpace(typeExpression(valueType))
	if !wrappedType(raw, "optional") {
		return expression, valueType, ""
	}
	inner := strings.TrimSpace(raw[len("optional(") : len(raw)-1])
	return expression + ".Value", map[string]any{"$expression": inner}, expression + ".Set"
}

func httpOutcomeValueExpression(resources map[string]Resource, operation Resource, outcome, reference, rootExpression string, rootType any) (string, any, error) {
	if err := validateHTTPOutcomeValueRef(resources, operation, outcome, reference); err != nil {
		return "", nil, err
	}
	parts := strings.Split(reference, ".")
	if len(parts) <= 2 {
		return rootExpression, rootType, nil
	}
	if outcome == "dispatch.enqueued" && len(parts) == 3 && parts[0] == "dispatch" && parts[1] == "receipt" {
		fields := map[string]string{
			"durable_identity": "DurableIdentity", "execution_id": "ExecutionID",
			"accepted_revision": "AcceptedRevision", "status_url": "StatusURL",
		}
		return rootExpression + "." + fields[parts[2]], map[string]any{"$ref": "string"}, nil
	}
	typeValue := rootType
	if refOrString(rootType) == "std.type.problem" {
		return rootExpression + "." + goName(parts[2]), map[string]any{"$ref": "string"}, nil
	}
	typeValue = recordFieldType(resources, operation.Module, rootType, parts[2:])
	var expression strings.Builder
	expression.WriteString(rootExpression)
	for _, field := range parts[2:] {
		expression.WriteString("." + goName(field))
	}
	return expression.String(), typeValue, nil
}

func renderContractRequestSchema(resources map[string]Resource, operation, binding Resource, httpSpec map[string]any) (string, error) {
	shape := resolveOperationInputShape(resources, operation)
	var mappings []string
	for _, source := range []struct {
		block, runtime string
	}{
		{"path_parameter", "sceneryruntime.ContractSourcePath"},
		{"path_tail", "sceneryruntime.ContractSourcePathTail"},
		{"query_parameter", "sceneryruntime.ContractSourceQuery"},
		{"header", "sceneryruntime.ContractSourceHeader"},
		{"cookie", "sceneryruntime.ContractSourceCookie"},
	} {
		for _, mapping := range namedChildren(httpSpec, source.block) {
			field, whole, ok := resolveOperationInputTarget(operation, shape, refOrString(mapping["to"]))
			if !ok || whole {
				return "", fmt.Errorf("HTTP binding %s has invalid %s target", binding.Address, source.block)
			}
			mappings = append(mappings, fmt.Sprintf("{Source: %s, Name: %q, Target: %q, Type: %q, Encoding: %q, Optional: %t, EnumValues: %#v}", source.runtime, stringValue(mapping["name"]), field.WireName, httpRuntimeTypeExpression(field.Type), stringValue(mapping["encoding"]), field.Optional, field.EnumValues))
		}
	}
	var contextMappings []string
	for _, mapping := range namedChildren(httpSpec, "context") {
		field, whole, ok := resolveOperationInputTarget(operation, shape, refOrString(mapping["to"]))
		if !ok || whole {
			return "", fmt.Errorf("HTTP binding %s has invalid context target", binding.Address)
		}
		contextMappings = append(contextMappings, fmt.Sprintf("{Source: %q, Target: %q}", refOrString(mapping["from"]), field.WireName))
	}

	bodyLiteral := "nil"
	if body, _ := httpSpec["body"].(map[string]any); body != nil {
		field, whole, ok := resolveOperationInputTarget(operation, shape, refOrString(body["to"]))
		if !ok {
			return "", fmt.Errorf("HTTP binding %s has invalid body target", binding.Address)
		}
		target := ""
		if !whole {
			target = field.WireName
		}
		include, err := runtimeBodySelection(body, "include", operation, shape)
		if err != nil {
			return "", fmt.Errorf("HTTP binding %s: %w", binding.Address, err)
		}
		except, err := runtimeBodySelection(body, "except", operation, shape)
		if err != nil {
			return "", fmt.Errorf("HTTP binding %s: %w", binding.Address, err)
		}
		accepted := literalStringListFromValue(body["accepted_media_types"])
		contentEncodings := literalStringListFromValue(body["content_encodings"])
		fields, fieldErr := renderContractBodyFields(operation, shape, body)
		if fieldErr != nil {
			return "", fmt.Errorf("HTTP binding %s: %w", binding.Address, fieldErr)
		}
		requestLimits, _ := httpSpec["request_limit"].(map[string]any)
		defaultFilePartBytes, _ := integerValue(requestLimits["multipart_file_part_bytes"])
		defaultNonFilePartBytes, _ := integerValue(requestLimits["multipart_non_file_part_bytes"])
		parts, partErr := renderContractMultipartParts(operation, shape, body, defaultFilePartBytes, defaultNonFilePartBytes)
		if partErr != nil {
			return "", fmt.Errorf("HTTP binding %s: %w", binding.Address, partErr)
		}
		compressedLimit, ok := integerValue(body["max_compressed_bytes"])
		if !ok {
			if stringValue(body["codec"]) == "multipart" {
				compressedLimit, _ = integerValue(requestLimits["multipart_body_bytes"])
			} else {
				compressedLimit, _ = integerValue(requestLimits["body_bytes"])
			}
		}
		decompressedLimit, ok := integerValue(body["max_decompressed_bytes"])
		if !ok {
			if stringValue(body["codec"]) == "multipart" {
				decompressedLimit, _ = integerValue(requestLimits["multipart_body_bytes"])
			} else {
				decompressedLimit, _ = integerValue(requestLimits["decompressed_body_bytes"])
			}
		}
		maxParts, ok := integerValue(body["max_parts"])
		if !ok {
			maxParts, _ = integerValue(requestLimits["multipart_parts"])
		}
		typeValue := operation.Spec["input"]
		if !whole {
			typeValue = field.Type
		}
		typeExpression := goWireTypeExpression(typeValue)
		bodyLiteral = fmt.Sprintf("&sceneryruntime.ContractBodyMapping{Codec: %q, Target: %q, Type: %q, DecodeValue: func(data []byte, target any) error { return scenery.UnmarshalContractValue(data, target, %q) }, Include: %#v, Except: %#v, AcceptedMediaTypes: %#v, SupportedContentEncoding: %#v, MaxCompressedBytes: %d, MaxDecompressedBytes: %d, Fields: []sceneryruntime.ContractInputMapping{%s}, MultipartParts: []sceneryruntime.ContractMultipartPart{%s}, MaxMultipartParts: %d}", stringValue(body["codec"]), target, typeExpression, typeExpression, include, except, accepted, contentEncodings, compressedLimit, decompressedLimit, strings.Join(fields, ", "), strings.Join(parts, ", "), maxParts)
	}
	statuses := map[string]int{}
	for _, response := range namedChildren(httpSpec, "response") {
		when := refOrString(response["when"])
		if !strings.HasPrefix(when, "transport.") && !strings.HasPrefix(when, "admission.") && !strings.HasPrefix(when, "dispatch.") && !strings.HasPrefix(when, "system.") {
			continue
		}
		if status, err := strconv.Atoi(stringValue(response["status"])); err == nil {
			statuses[when] = status
		}
	}
	return fmt.Sprintf("sceneryruntime.ContractRequestSchema{Mappings: []sceneryruntime.ContractInputMapping{%s}, ContextMappings: []sceneryruntime.ContractContextMapping{%s}, Body: %s, TransportStatuses: %s}", strings.Join(mappings, ", "), strings.Join(contextMappings, ", "), bodyLiteral, goStringIntMap(statuses)), nil
}

func renderContractPathTail(resources map[string]Resource, binding Resource, httpSpec map[string]any) string {
	tails := namedChildren(httpSpec, "path_tail")
	if len(tails) != 1 {
		return "nil"
	}
	tail := tails[0]
	gateway := resources[resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway")]
	canonicalTemplate := joinHTTPPath(stringValue(gateway.Spec["base_path"]), stringValue(httpSpec["path"]))
	minimumSegments, _ := integerValue(tail["minimum_segments"])
	return fmt.Sprintf("&sceneryruntime.ContractPathTail{CanonicalTemplate: %q, Name: %q, Target: %q, Type: %q, EmptyCapture: %q, MinimumSegments: %d, Decoding: %q, Guarantee: %q, Precedence: []string{%q, %q, %q, %q}}", canonicalTemplate, stringValue(tail["name"]), refOrString(tail["to"]), stringValue(tail["target_type"]), stringValue(tail["empty_capture"]), minimumSegments, stringValue(tail["decoding"]), stringValue(tail["guarantee"]), "literal", "parameter", "exact_end", "path_tail")
}

func renderContractHTTPPolicy(resources map[string]Resource, binding Resource, httpSpec map[string]any) string {
	gatewayAddress := resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway")
	gateway := resources[gatewayAddress]
	cors := lastRef(refOrString(gateway.Spec["cors"]))
	forwarded := lastRef(refOrString(gateway.Spec["forwarded"]))
	trusted := literalStringListFromValue(gateway.Spec["trusted_proxies"])
	if value, ok := gateway.Spec["trusted_proxies"].(map[string]any); ok {
		trusted = literalStringListFromValue(value["prefixes"])
	}
	allowedOrigins := []string{}
	if value, ok := gateway.Spec["cors"].(map[string]any); ok {
		allowedOrigins = literalStringListFromValue(value["origins"])
	}
	requestLimits, _ := httpSpec["request_limit"].(map[string]any)
	responseLimits, _ := httpSpec["response_limit"].(map[string]any)
	timeouts, _ := httpSpec["timeouts"].(map[string]any)
	headerBytes, _ := integerValue(requestLimits["header_bytes"])
	responseBytes, _ := integerValue(responseLimits["body_bytes"])
	threshold, _ := integerValue(responseLimits["compression_threshold_bytes"])
	compression := literalStringListFromValue(responseLimits["compression_algorithms"])
	status := map[string]int{}
	for _, response := range namedChildren(httpSpec, "response") {
		when := refOrString(response["when"])
		if value, ok := integerValue(response["status"]); ok {
			status[when] = value
		}
	}
	authorization := resources[resolveResourceRef(binding, refString(binding.Spec["authorization"]), "authorization")]
	strategy := stringValue(authorization.Spec["strategy"])
	if strategy == "" {
		strategy = lastRef(refOrString(binding.Spec["authorization"]))
	}
	rules := renderContractAuthorizationRules(authorization)
	pipeline := resources[resolveResourceRef(binding, refString(binding.Spec["pipeline"]), "pipeline")]
	steps := []string{}
	for _, step := range orderedChildren(pipeline.Spec, "step") {
		steps = append(steps, refOrString(step["use"]))
	}
	return fmt.Sprintf("&sceneryruntime.ContractHTTPPolicy{BindingAddress: %q, GatewayAddress: %q, CORS: %q, AllowedOrigins: %#v, Forwarded: %q, TrustedProxyPrefixes: %#v, MaxRequestHeaderBytes: %d, MaxResponseBytes: %d, CompressionAlgorithms: %#v, CompressionThreshold: %d, TotalInvocationTimeoutNanos: %d, ReadTimeoutNanos: %d, WriteTimeoutNanos: %d, IdleTimeoutNanos: %d, AuthorizationStrategy: %q, AuthorizationRuleCount: %d, AuthorizationRules: []sceneryruntime.ContractAuthorizationRule{%s}, PipelineSteps: %#v, FrameworkGuarantee: %q, TransportStatuses: %s}", binding.Address, gatewayAddress, cors, allowedOrigins, forwarded, trusted, headerBytes, responseBytes, compression, threshold, durationNanos(timeouts["total_invocation"]), durationNanos(timeouts["read"]), durationNanos(timeouts["write"]), durationNanos(timeouts["idle"]), strategy, len(rules), strings.Join(rules, ", "), steps, stringValue(httpSpec["guarantee"]), goStringIntMap(status))
}

func renderContractInternalPolicy(resources map[string]Resource, binding Resource) string {
	return renderContractInvocationPolicy(resources, binding, binding.Address, binding.Spec["authorization"], binding.Spec["pipeline"])
}

func renderContractInvocationPolicy(resources map[string]Resource, owner Resource, address string, authorizationValue, pipelineValue any) string {
	authorization := resources[resolveResourceRef(owner, refString(authorizationValue), "authorization")]
	strategy := stringValue(authorization.Spec["strategy"])
	if strategy == "" {
		strategy = lastRef(refOrString(authorizationValue))
	}
	if strategy == "application" || strategy == "scheduled" {
		strategy = "public"
	}
	rules := renderContractAuthorizationRules(authorization)
	pipeline := resources[resolveResourceRef(owner, refString(pipelineValue), "pipeline")]
	steps := []string{}
	for _, step := range orderedChildren(pipeline.Spec, "step") {
		steps = append(steps, refOrString(step["use"]))
	}
	return fmt.Sprintf("&sceneryruntime.ContractHTTPPolicy{BindingAddress: %q, AuthorizationStrategy: %q, AuthorizationRuleCount: %d, AuthorizationRules: []sceneryruntime.ContractAuthorizationRule{%s}, PipelineSteps: %#v}", address, strategy, len(rules), strings.Join(rules, ", "), steps)
}

func renderContractAuthorizationRules(authorization Resource) []string {
	var rules []string
	for _, rule := range orderedChildren(authorization.Spec, "rule") {
		effect, value := "allow", rule["allow"]
		if denied, ok := rule["deny"]; ok {
			effect, value = "deny", denied
		}
		expression := "false"
		switch value := value.(type) {
		case bool:
			expression = strconv.FormatBool(value)
		case map[string]any:
			if raw := stringValue(value["$expression"]); raw != "" {
				expression = raw
			}
		}
		rules = append(rules, fmt.Sprintf("{Name: %q, Effect: %q, Expression: %q}", stringValue(rule["name"]), effect, expression))
	}
	return rules
}

func renderContractResponseOptions(httpSpec map[string]any, typeExpression string) string {
	limits, _ := httpSpec["response_limit"].(map[string]any)
	maximum, _ := integerValue(limits["body_bytes"])
	threshold, _ := integerValue(limits["compression_threshold_bytes"])
	algorithms := literalStringListFromValue(limits["compression_algorithms"])
	encoder := "nil"
	if typeExpression != "" {
		encoder = fmt.Sprintf("func(value any) ([]byte, error) { return scenery.MarshalContractValue(value, %q) }", typeExpression)
	}
	return fmt.Sprintf("sceneryruntime.ContractResponseOptions{MaxBytes: %d, CompressionAlgorithms: %#v, CompressionThreshold: %d, TypeExpression: %q, EncodeValue: %s}", maximum, algorithms, threshold, typeExpression, encoder)
}

func durationNanos(value any) int64 {
	if scalar, ok := value.(map[string]any); ok && stringValue(scalar["$scalar"]) == "duration" {
		parsed, _ := strconv.ParseInt(stringValue(scalar["nanoseconds"]), 10, 64)
		return parsed
	}
	text := stringValue(value)
	parsed, err := scenery.ParseDuration(text)
	if err != nil {
		return 0
	}
	nanoseconds := parsed.Nanoseconds()
	if !nanoseconds.IsInt64() {
		return 0
	}
	return nanoseconds.Int64()
}

func renderContractBodyFields(operation Resource, shape operationInputShape, body map[string]any) ([]string, error) {
	if stringValue(body["codec"]) != "form" || shape.Record == nil {
		return nil, nil
	}
	include := httpBodyFieldSelection(body, "include", operation, shape)
	except := httpBodyFieldSelection(body, "except", operation, shape)
	if len(include.invalid) > 0 || len(except.invalid) > 0 {
		return nil, fmt.Errorf("invalid form body field selection")
	}
	var fields []string
	for name, field := range shape.Fields {
		if include.present && !include.fields[name] || except.fields[name] {
			continue
		}
		fields = append(fields, fmt.Sprintf("{Name: %q, Target: %q, Type: %q, Optional: %t, EnumValues: %#v}", field.WireName, field.WireName, httpRuntimeTypeExpression(field.Type), field.Optional, field.EnumValues))
	}
	sort.Strings(fields)
	return fields, nil
}

func renderContractMultipartParts(operation Resource, shape operationInputShape, body map[string]any, defaultFilePartBytes, defaultNonFilePartBytes int) ([]string, error) {
	if stringValue(body["codec"]) != "multipart" {
		return nil, nil
	}
	var parts []string
	for _, part := range namedChildren(body, "part") {
		field, whole, ok := resolveOperationInputTarget(operation, shape, refOrString(part["to"]))
		if !ok || whole {
			return nil, fmt.Errorf("multipart part %q has invalid target", stringValue(part["name"]))
		}
		maxBytes, _ := integerValue(part["max_bytes"])
		if maxBytes == 0 {
			if stringValue(part["kind"]) == "file" {
				maxBytes = defaultFilePartBytes
			} else {
				maxBytes = defaultNonFilePartBytes
			}
		}
		parts = append(parts, fmt.Sprintf("{Name: %q, Target: %q, Type: %q, Kind: %q, MediaTypes: %#v, MaxBytes: %d, Multiple: %t, Optional: %t, RetainFilename: %t}", stringValue(part["name"]), field.WireName, httpRuntimeTypeExpression(field.Type), stringValue(part["kind"]), literalStringListFromValue(part["media_types"]), maxBytes, part["multiple"] == true, field.Optional, part["retain_filename"] == true))
	}
	sort.Strings(parts)
	return parts, nil
}

func runtimeBodySelection(body map[string]any, key string, operation Resource, shape operationInputShape) ([]string, error) {
	selection := httpBodyFieldSelection(body, key, operation, shape)
	if len(selection.invalid) > 0 {
		return nil, fmt.Errorf("invalid body %s field selection", key)
	}
	var wires []string
	for name := range selection.fields {
		wires = append(wires, shape.Fields[name].WireName)
	}
	sort.Strings(wires)
	return wires, nil
}

func httpRuntimeTypeExpression(value any) string {
	expression := typeExpression(value)
	return rewriteHTTPRuntimeType(strings.TrimSpace(expression))
}

func rewriteHTTPRuntimeType(expression string) string {
	for _, wrapper := range []string{"optional", "nullable", "list", "set"} {
		prefix := wrapper + "("
		if strings.HasPrefix(expression, prefix) && strings.HasSuffix(expression, ")") {
			return wrapper + "(" + rewriteHTTPRuntimeType(strings.TrimSpace(expression[len(prefix):len(expression)-1])) + ")"
		}
	}
	if strings.HasPrefix(expression, "enum.") {
		return "string"
	}
	return expression
}

func runtimeBindingPath(resources map[string]Resource, binding Resource, bindingPath string) string {
	gatewayAddress := resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway")
	gateway := resources[gatewayAddress]
	return runtimeHTTPPath(joinHTTPPath(stringValue(gateway.Spec["base_path"]), bindingPath))
}

func defaultHTTPMediaType(codec string) string {
	switch codec {
	case "problem_json":
		return "application/problem+json"
	case "text":
		return "text/plain"
	case "bytes":
		return "application/octet-stream"
	case "form":
		return "application/x-www-form-urlencoded"
	case "multipart":
		return "multipart/form-data"
	default:
		return "application/json"
	}
}

func literalStringListFromValue(value any) []string {
	items, _ := value.([]any)
	result := make([]string, 0, len(items))
	for _, item := range items {
		if text, ok := item.(string); ok {
			result = append(result, text)
		}
	}
	return result
}

func goStringIntMap(values map[string]int) string {
	if len(values) == 0 {
		return "nil"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var entries []string
	for _, key := range keys {
		entries = append(entries, fmt.Sprintf("%q: %d", key, values[key]))
	}
	return "map[string]int{" + strings.Join(entries, ", ") + "}"
}

func goStringStringMap(values map[string]string) string {
	if len(values) == 0 {
		return "map[string]string{}"
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, fmt.Sprintf("%q: %q", key, values[key]))
	}
	return "map[string]string{" + strings.Join(entries, ", ") + "}"
}
