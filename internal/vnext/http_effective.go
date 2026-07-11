package vnext

import "strconv"

func applyHTTPEffectiveDefaults(resources []Resource) {
	byAddress := map[string]*Resource{}
	for index := range resources {
		byAddress[resources[index].Address] = &resources[index]
		if resources[index].Kind == "scenery.http-gateway/v1" && resources[index].Origin.Kind != "legacy_v0" {
			applyHTTPGatewayDefaults(&resources[index])
		}
	}
	for index := range resources {
		binding := &resources[index]
		if binding.Kind != "scenery.binding/v1" || binding.Origin.Kind == "legacy_v0" || binding.Spec["protocol"] != "http" {
			continue
		}
		gateway := byAddress[resolveResourceRef(*binding, refString(binding.Spec["gateway"]), "http_gateway")]
		if gateway != nil && stringValue(binding.Spec["exposure"]) == "" {
			binding.Spec["exposure"] = gateway.Spec["exposure"]
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		if httpSpec["guarantee"] == nil {
			httpSpec["guarantee"] = "framework_enforced"
		}
		if gateway != nil {
			for _, key := range []string{"request_limit", "response_limit", "timeouts"} {
				if httpSpec[key] == nil {
					httpSpec[key] = cloneSemanticValue(gateway.Spec[key])
				} else if inherited, ok := gateway.Spec[key].(map[string]any); ok {
					mergeHTTPDefaults(httpSpec[key], inherited)
				}
			}
		}
		applyHTTPStandardResponses(*binding, httpSpec)
		applyHTTPResponseDefaults(httpSpec)
	}
}

func applyHTTPResponseDefaults(httpSpec map[string]any) {
	for _, response := range namedChildren(httpSpec, "response") {
		for _, header := range namedChildren(response, "header") {
			if header["encoding"] == nil {
				header["encoding"] = "repeated"
			}
		}
		for _, cookie := range namedChildren(response, "cookie") {
			defaults := map[string]any{
				"path": "/", "domain": "", "max_age": exactNumericScalar("0"), "expires": "",
				"secure": true, "http_only": true, "same_site": "lax",
			}
			mergeHTTPDefaults(cookie, defaults)
		}
	}
}

func mergeHTTPDefaults(value any, defaults map[string]any) {
	target, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, fallback := range defaults {
		if target[key] == nil {
			target[key] = cloneSemanticValue(fallback)
		}
	}
}

func applyHTTPGatewayDefaults(gateway *Resource) {
	requestDefaults := map[string]any{
		"header_bytes": exactNumericScalar("65536"), "body_bytes": exactNumericScalar("8388608"), "decompressed_body_bytes": exactNumericScalar("16777216"),
		"multipart_body_bytes": exactNumericScalar("33554432"), "multipart_file_part_bytes": exactNumericScalar("16777216"),
		"multipart_non_file_part_bytes": exactNumericScalar("1048576"), "multipart_parts": exactNumericScalar("128"),
	}
	if gateway.Spec["request_limit"] == nil {
		gateway.Spec["request_limit"] = requestDefaults
	} else {
		mergeHTTPDefaults(gateway.Spec["request_limit"], requestDefaults)
	}
	responseDefaults := map[string]any{
		"body_bytes": exactNumericScalar("16777216"), "compression_algorithms": []any{"gzip"}, "compression_threshold_bytes": exactNumericScalar("1024"),
	}
	if gateway.Spec["response_limit"] == nil {
		gateway.Spec["response_limit"] = responseDefaults
	} else {
		mergeHTTPDefaults(gateway.Spec["response_limit"], responseDefaults)
	}
	timeoutDefaults := map[string]any{
		"read": durationSemanticScalar(30_000_000_000), "write": durationSemanticScalar(30_000_000_000),
		"idle": durationSemanticScalar(120_000_000_000), "total_invocation": durationSemanticScalar(2_400_000_000_000),
	}
	if gateway.Spec["timeouts"] == nil {
		gateway.Spec["timeouts"] = timeoutDefaults
	} else {
		mergeHTTPDefaults(gateway.Spec["timeouts"], timeoutDefaults)
	}
}

func durationSemanticScalar(nanoseconds int64) map[string]any {
	return map[string]any{"$scalar": "duration", "nanoseconds": strconv.FormatInt(nanoseconds, 10)}
}

func applyHTTPStandardResponses(binding Resource, httpSpec map[string]any) {
	existing := map[string]bool{}
	for _, response := range namedChildren(httpSpec, "response") {
		existing[refOrString(response["when"])] = true
	}
	defaults := []struct {
		when string
		code int
	}{
		{"transport.invalid_request", 400}, {"admission.rate_limited", 429}, {"system.internal", 500},
	}
	if httpSpec["body"] != nil {
		defaults = append(defaults, struct {
			when string
			code int
		}{"transport.unsupported_media_type", 415})
	}
	for _, response := range namedChildren(httpSpec, "response") {
		if response["body"] != nil {
			defaults = append(defaults, struct {
				when string
				code int
			}{"transport.not_acceptable", 406})
			break
		}
	}
	if refOrString(binding.Spec["authentication"]) != "std.authentication.none" {
		defaults = append(defaults,
			struct {
				when string
				code int
			}{"admission.unauthenticated", 401},
			struct {
				when string
				code int
			}{"admission.forbidden", 403},
		)
	}
	switch stringValue(binding.Spec["delivery"]) {
	case "enqueue":
		defaults = append(defaults, struct {
			when string
			code int
		}{"dispatch.rejected", 503})
	case "wait":
		defaults = append(defaults, struct {
			when string
			code int
		}{"dispatch.rejected", 503}, struct {
			when string
			code int
		}{"dispatch.wait_timeout", 504})
	}
	for _, item := range defaults {
		if existing[item.when] {
			continue
		}
		appendHTTPResponse(httpSpec, map[string]any{
			"name": lastRef(item.when), "when": map[string]any{"$ref": item.when}, "status": integerString(item.code),
			"body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": standardProblemSource(item.when)}},
		})
		existing[item.when] = true
	}
}

func appendHTTPResponse(httpSpec map[string]any, response map[string]any) {
	existing := httpSpec["response"]
	switch typed := existing.(type) {
	case nil:
		httpSpec["response"] = response
	case map[string]any:
		httpSpec["response"] = []any{typed, response}
	case []any:
		httpSpec["response"] = append(typed, response)
	}
}

func standardProblemSource(outcome string) string {
	root := lastRef(outcome)
	if root == "enqueued" {
		return "dispatch.receipt"
	}
	if outcome == "system.internal" {
		return "system.problem"
	}
	if len(outcome) >= 10 && outcome[:10] == "admission." {
		return "admission.problem"
	}
	if len(outcome) >= 9 && outcome[:9] == "dispatch." {
		return "dispatch.problem"
	}
	return "transport.problem"
}

func integerString(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var out []byte
	for value > 0 {
		out = append(out, digits[value%10])
		value /= 10
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return string(out)
}
