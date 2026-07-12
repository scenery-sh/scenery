package vnext

import "strconv"

func applyHTTPEffectiveDefaults(resources []Resource) {
	byAddress := map[string]*Resource{}
	for index := range resources {
		byAddress[resources[index].Address] = &resources[index]
		if resources[index].Kind == "scenery.http-gateway/v1" {
			applyHTTPGatewayDefaults(&resources[index])
		}
	}
	for index := range resources {
		binding := &resources[index]
		if binding.Kind != "scenery.binding/v1" || binding.Spec["protocol"] != "http" {
			continue
		}
		gateway := byAddress[resolveResourceRef(*binding, refString(binding.Spec["gateway"]), "http_gateway")]
		if gateway != nil && stringValue(binding.Spec["exposure"]) == "" {
			binding.Spec["exposure"] = gateway.Spec["exposure"]
			setFieldProvenance(&binding.Origin, "/spec/exposure", binding.Spec["exposure"], inheritedHTTPField(gateway, "/spec/exposure"))
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		applyHTTPPathTailEffective(binding, httpSpec, byAddress)
		if httpSpec["guarantee"] == nil {
			httpSpec["guarantee"] = "framework_enforced"
			setFieldProvenance(&binding.Origin, "/spec/http/guarantee", httpSpec["guarantee"], httpDefaultField())
		}
		if gateway != nil {
			for _, key := range []string{"request_limit", "response_limit", "timeouts"} {
				path := "/spec/http/" + key
				if httpSpec[key] == nil {
					httpSpec[key] = cloneSemanticValue(gateway.Spec[key])
					setFieldProvenance(&binding.Origin, path, httpSpec[key], inheritedHTTPField(gateway, "/spec/"+key))
				} else if inherited, ok := gateway.Spec[key].(map[string]any); ok {
					mergeHTTPDefaultsWithProvenance(binding, httpSpec[key], path, inherited, func(name string) FieldProvenance {
						return inheritedHTTPField(gateway, "/spec/"+key+"/"+escapeJSONPointer(name))
					})
				}
			}
		}
		applyHTTPStandardResponses(binding, httpSpec)
		applyHTTPResponseDefaults(binding, httpSpec)
	}
}

func applyHTTPResponseDefaults(binding *Resource, httpSpec map[string]any) {
	for _, response := range provenanceNamedChildren(httpSpec, "response", "/spec/http") {
		for _, header := range provenanceNamedChildren(response.Value, "header", response.Path) {
			if header.Value["encoding"] == nil {
				header.Value["encoding"] = "repeated"
				setFieldProvenance(&binding.Origin, provenanceChildPath(header.Path, "encoding"), header.Value["encoding"], httpDefaultField())
			}
		}
		for _, cookie := range provenanceNamedChildren(response.Value, "cookie", response.Path) {
			defaults := map[string]any{
				"path": "/", "domain": "", "max_age": exactNumericScalar("0"), "expires": "",
				"secure": true, "http_only": true, "same_site": "lax",
			}
			mergeHTTPDefaultsWithProvenance(binding, cookie.Value, cookie.Path, defaults, func(string) FieldProvenance { return httpDefaultField() })
		}
	}
}

func mergeHTTPDefaultsWithProvenance(resource *Resource, value any, path string, defaults map[string]any, provenance func(string) FieldProvenance) {
	target, ok := value.(map[string]any)
	if !ok {
		return
	}
	for key, fallback := range defaults {
		if target[key] != nil {
			continue
		}
		target[key] = cloneSemanticValue(fallback)
		setFieldProvenance(&resource.Origin, path+"/"+escapeJSONPointer(key), target[key], provenance(key))
	}
}

func applyHTTPGatewayDefaults(gateway *Resource) {
	requestDefaults := map[string]any{
		"header_bytes": exactNumericScalar("65536"), "body_bytes": exactNumericScalar("8388608"), "decompressed_body_bytes": exactNumericScalar("16777216"),
		"multipart_body_bytes": exactNumericScalar("33554432"), "multipart_file_part_bytes": exactNumericScalar("16777216"),
		"multipart_non_file_part_bytes": exactNumericScalar("1048576"), "multipart_parts": exactNumericScalar("128"),
	}
	if gateway.Spec["request_limit"] == nil {
		gateway.Spec["request_limit"] = cloneSemanticValue(requestDefaults)
		setFieldProvenance(&gateway.Origin, "/spec/request_limit", gateway.Spec["request_limit"], httpDefaultField())
	} else {
		mergeHTTPDefaultsWithProvenance(gateway, gateway.Spec["request_limit"], "/spec/request_limit", requestDefaults, func(string) FieldProvenance { return httpDefaultField() })
	}
	responseDefaults := map[string]any{
		"body_bytes": exactNumericScalar("16777216"), "compression_algorithms": []any{"gzip"}, "compression_threshold_bytes": exactNumericScalar("1024"),
	}
	if gateway.Spec["response_limit"] == nil {
		gateway.Spec["response_limit"] = cloneSemanticValue(responseDefaults)
		setFieldProvenance(&gateway.Origin, "/spec/response_limit", gateway.Spec["response_limit"], httpDefaultField())
	} else {
		mergeHTTPDefaultsWithProvenance(gateway, gateway.Spec["response_limit"], "/spec/response_limit", responseDefaults, func(string) FieldProvenance { return httpDefaultField() })
	}
	timeoutDefaults := map[string]any{
		"read": durationSemanticScalar(30_000_000_000), "write": durationSemanticScalar(30_000_000_000),
		"idle": durationSemanticScalar(120_000_000_000), "total_invocation": durationSemanticScalar(2_400_000_000_000),
	}
	if gateway.Spec["timeouts"] == nil {
		gateway.Spec["timeouts"] = cloneSemanticValue(timeoutDefaults)
		setFieldProvenance(&gateway.Origin, "/spec/timeouts", gateway.Spec["timeouts"], httpDefaultField())
	} else {
		mergeHTTPDefaultsWithProvenance(gateway, gateway.Spec["timeouts"], "/spec/timeouts", timeoutDefaults, func(string) FieldProvenance { return httpDefaultField() })
	}
}

func httpDefaultField() FieldProvenance {
	return FieldProvenance{Kind: "default", ProvidedBy: "scenery.http-codec/v1", Transformations: []string{"http_profile_default"}}
}

func inheritedHTTPField(gateway *Resource, path string) FieldProvenance {
	field := gateway.Origin.FieldProvenance[path]
	field.Kind = "inheritance"
	field.ProvidedBy = gateway.Address
	field.SourceAddress = gateway.Address
	field.Transformations = append(append([]string(nil), field.Transformations...), "gateway_inheritance")
	return field
}

func durationSemanticScalar(nanoseconds int64) map[string]any {
	return map[string]any{"$scalar": "duration", "nanoseconds": strconv.FormatInt(nanoseconds, 10)}
}

func applyHTTPStandardResponses(binding *Resource, httpSpec map[string]any) {
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
		appendHTTPResponse(binding, httpSpec, map[string]any{
			"name": lastRef(item.when), "when": map[string]any{"$ref": item.when}, "status": integerString(item.code),
			"body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": standardProblemSource(item.when)}},
		})
		for _, candidate := range provenanceNamedChildren(httpSpec, "response", "/spec/http") {
			if stringValue(candidate.Value["name"]) == lastRef(item.when) {
				setFieldProvenance(&binding.Origin, candidate.Path, candidate.Value, httpDefaultField())
				break
			}
		}
		existing[item.when] = true
	}
}

func appendHTTPResponse(binding *Resource, httpSpec map[string]any, response map[string]any) {
	existing := httpSpec["response"]
	switch typed := existing.(type) {
	case nil:
		httpSpec["response"] = response
	case map[string]any:
		if binding != nil {
			rebaseFieldProvenance(&binding.Origin, "/spec/http/response", "/spec/http/response/0")
		}
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
