package vnext

import (
	"fmt"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	scenery "scenery.sh"
)

var httpPathParameterPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\}`)
var httpPathTailPattern = regexp.MustCompile(`\{([a-z][a-z0-9_]*)\.\.\.\}`)

func validateHTTPResources(resources []Resource) []Diagnostic {
	byAddress := map[string]Resource{}
	for _, resource := range resources {
		byAddress[resource.Address] = resource
	}
	routes := map[string]string{}
	type routeEntry struct {
		gateway, method, shape, address string
		pathTail                        bool
	}
	var routeEntries []routeEntry
	var diagnostics []Diagnostic
	for _, binding := range resources {
		if binding.Kind != "scenery.binding/v1" || binding.Spec["protocol"] != "http" {
			continue
		}
		httpSpec, _ := binding.Spec["http"].(map[string]any)
		if stringValue(binding.Spec["delivery"]) == "stream" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN7008", Severity: "error", Message: "unsupported_profile: HTTP stream delivery requires a negotiated streaming profile", Address: binding.Address})
		}
		if httpUsesUnsupportedStreamingCodec(httpSpec) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN7008", Severity: "error", Message: "unsupported_profile: server_sent_events requires a negotiated streaming profile", Address: binding.Address})
		}
		method, _ := httpSpec["method"].(string)
		bindingPath, _ := httpSpec["path"].(string)
		gatewayRef := resolveResourceRef(binding, refString(binding.Spec["gateway"]), "http_gateway")
		gateway, gatewayOK := byAddress[gatewayRef]
		if method == "" || bindingPath == "" || gatewayRef == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2001", Severity: "error", Message: "HTTP binding requires gateway, method, and path", Address: binding.Address})
			continue
		}
		if method != strings.ToUpper(method) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2101", Severity: "error", Message: "HTTP method must use canonical uppercase form", Address: binding.Address})
		}
		if !validHTTPPath(bindingPath) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2102", Severity: "error", Message: "HTTP path must be absolute, normalized, and use only complete canonical parameter segments", Address: binding.Address})
		}
		if body, _ := httpSpec["body"].(map[string]any); body != nil && body["codec"] == "raw" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2103", Severity: "error", Message: "codec = raw is not part of scenery.http-codec/v1", Address: binding.Address})
		}
		if refOrString(httpSpec["codec_profile"]) != "std.codec.http_json_v1" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2105", Severity: "error", Message: "unsupported HTTP codec profile", Address: binding.Address})
		}
		if gatewayOK && gateway.Spec["exposure"] == "internet" {
			if binding.Spec["authentication"] == nil || binding.Spec["authorization"] == nil || binding.Spec["pipeline"] == nil {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2104", Severity: "error", Message: "internet HTTP binding requires explicit authentication, authorization, and pipeline", Address: binding.Address})
			}
		}
		authentication := refOrString(binding.Spec["authentication"])
		authorization := refOrString(binding.Spec["authorization"])
		if authorization == "" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2108", Severity: "error", Message: "HTTP authorization must be explicit; std.authorization.none is the deny-all policy", Address: binding.Address})
		}
		if authentication == "std.authentication.none" && authorization != "std.authorization.public" && authorization != "std.authorization.none" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2109", Severity: "error", Message: "anonymous HTTP access requires std.authorization.public", Address: binding.Address})
		}
		if gatewayOK {
			if widerExposure(stringValue(binding.Spec["exposure"]), stringValue(gateway.Spec["exposure"])) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2106", Severity: "error", Message: "HTTP binding exposure cannot widen its gateway", Address: binding.Address})
			}
			if refOrString(gateway.Spec["forwarded"]) != "std.forwarded_headers.reject" && refOrString(gateway.Spec["trusted_proxies"]) == "std.trusted_proxies.none" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2107", Severity: "error", Message: "forwarded headers require a trusted-proxy policy", Address: gateway.Address})
			}
			diagnostics = append(diagnostics, validateHTTPEffectiveLimits(binding, gateway, httpSpec)...)
		}
		diagnostics = append(diagnostics, validateHTTPWireLabels(binding, httpSpec)...)
		diagnostics = append(diagnostics, validateHTTPPathMappings(binding, httpSpec, bindingPath)...)
		if operation, ok := byAddress[resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")]; ok {
			diagnostics = append(diagnostics, validateHTTPInputMappings(byAddress, binding, operation, httpSpec)...)
			diagnostics = append(diagnostics, validateHTTPResponses(byAddress, binding, operation, httpSpec)...)
		}
		basePath, _ := gateway.Spec["base_path"].(string)
		effectivePath := joinHTTPPath(basePath, bindingPath)
		canonicalMethod, routeShape := strings.ToUpper(method), canonicalRoute(effectivePath)
		key := canonicalMethod + " " + gatewayRef + " " + routeShape
		if owner, exists := routes[key]; exists {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2002", Severity: "error", Message: "duplicate HTTP route " + key, Address: binding.Address, Related: []Related{{Address: owner}}})
		} else {
			usesTail := httpSpecUsesPathTail(httpSpec) || httpPathTailPattern.MatchString(bindingPath)
			for _, existing := range routeEntries {
				if (usesTail || existing.pathTail) && existing.gateway == gatewayRef && existing.shape == routeShape && httpRouteMethodsOverlap(canonicalMethod, existing.method) {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN2002", Severity: "error", Message: "duplicate HTTP route match set " + gatewayRef + " " + routeShape, Address: binding.Address, Related: []Related{{Address: existing.address}}})
					break
				}
			}
			routes[key] = binding.Address
			routeEntries = append(routeEntries, routeEntry{gateway: gatewayRef, method: canonicalMethod, shape: routeShape, address: binding.Address, pathTail: usesTail})
		}
	}
	return diagnostics
}

func httpRouteMethodsOverlap(left, right string) bool {
	if left == "*" || right == "*" || left == right {
		return true
	}
	return left == http.MethodGet && right == http.MethodHead || left == http.MethodHead && right == http.MethodGet
}

func validateHTTPWireLabels(binding Resource, httpSpec map[string]any) []Diagnostic {
	var diagnostics []Diagnostic
	validate := func(blockType string, schema *authoredBlockSchema, children []map[string]any) {
		for _, child := range children {
			label := stringValue(child["name"])
			if !validAuthoredLabel(schema, label) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1013", Severity: "error", Message: fmt.Sprintf("HTTP %s label %q violates %s policy", blockType, label, schema.LabelPolicy), Address: binding.Address})
			}
		}
	}
	validate("path_parameter", httpPathParameterSourceSchema, namedChildren(httpSpec, "path_parameter"))
	validate("path_tail", httpPathTailSourceSchema, namedChildren(httpSpec, "path_tail"))
	validate("query_parameter", httpQueryParameterSourceSchema, namedChildren(httpSpec, "query_parameter"))
	validate("header", httpHeaderSourceSchema, namedChildren(httpSpec, "header"))
	validate("cookie", httpCookieSourceSchema, namedChildren(httpSpec, "cookie"))
	if body, _ := httpSpec["body"].(map[string]any); body != nil {
		validate("multipart part", httpMultipartPartSourceSchema, namedChildren(body, "part"))
	}
	for _, response := range namedChildren(httpSpec, "response") {
		validate("response header", httpResponseHeaderSourceSchema, namedChildren(response, "header"))
		validate("response cookie", httpResponseCookieSourceSchema, namedChildren(response, "cookie"))
	}
	return diagnostics
}

func httpUsesUnsupportedStreamingCodec(httpSpec map[string]any) bool {
	if body, _ := httpSpec["body"].(map[string]any); stringValue(body["codec"]) == "server_sent_events" {
		return true
	}
	for _, response := range namedChildren(httpSpec, "response") {
		if body, _ := response["body"].(map[string]any); stringValue(body["codec"]) == "server_sent_events" {
			return true
		}
	}
	return false
}

func validateHTTPEffectiveLimits(binding, gateway Resource, httpSpec map[string]any) []Diagnostic {
	var diagnostics []Diagnostic
	for _, group := range []string{"request_limit", "response_limit"} {
		gatewayValues, _ := gateway.Spec[group].(map[string]any)
		bindingValues, ok := httpSpec[group].(map[string]any)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2120", Severity: "error", Message: "HTTP effective " + group + " is missing", Address: binding.Address})
			continue
		}
		for name, gatewayValue := range gatewayValues {
			if name == "compression_algorithms" {
				allowed := map[string]bool{}
				for _, value := range literalStringListFromValue(gatewayValue) {
					allowed[value] = true
				}
				for _, value := range literalStringListFromValue(bindingValues[name]) {
					if value != "gzip" || !allowed[value] {
						diagnostics = append(diagnostics, Diagnostic{Code: "SCN2121", Severity: "error", Message: "HTTP binding compression cannot widen or use an unsupported gateway algorithm", Address: binding.Address})
					}
				}
				continue
			}
			gatewayLimit, gatewayOK := integerValue(gatewayValue)
			bindingLimit, bindingOK := integerValue(bindingValues[name])
			if !gatewayOK || !bindingOK || gatewayLimit < 0 || bindingLimit < 0 {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2120", Severity: "error", Message: "HTTP limits must be non-negative exact integers", Address: binding.Address})
				continue
			}
			if bindingLimit > gatewayLimit {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2121", Severity: "error", Message: "HTTP binding limit " + name + " cannot widen its gateway", Address: binding.Address})
			}
		}
	}
	gatewayTimeouts, _ := gateway.Spec["timeouts"].(map[string]any)
	bindingTimeouts, _ := httpSpec["timeouts"].(map[string]any)
	for name, gatewayValue := range gatewayTimeouts {
		gatewayDuration, gatewayErr := scenery.ParseDuration(stringValue(gatewayValue))
		bindingDuration, bindingErr := scenery.ParseDuration(stringValue(bindingTimeouts[name]))
		if gatewayErr != nil || bindingErr != nil || bindingDuration.Sign() < 0 || !gatewayDuration.Nanoseconds().IsInt64() || !bindingDuration.Nanoseconds().IsInt64() {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2122", Severity: "error", Message: "HTTP timeout " + name + " is invalid", Address: binding.Address})
		} else if bindingDuration.Cmp(gatewayDuration) > 0 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2123", Severity: "error", Message: "HTTP binding timeout " + name + " cannot widen its gateway", Address: binding.Address})
		}
	}
	return diagnostics
}

func validateHTTPPathMappings(binding Resource, httpSpec map[string]any, routePath string) []Diagnostic {
	segments, err := parseHTTPPathTemplate(routePath)
	if err != nil {
		return nil
	}
	wantParameters, wantTails := map[string]int{}, map[string]int{}
	for _, segment := range segments {
		switch segment.kind {
		case httpTemplateParameter:
			wantParameters[segment.name]++
		case httpTemplateTail:
			wantTails[segment.name]++
		}
	}
	gotParameters, gotTails := map[string]int{}, map[string]int{}
	for _, mapping := range namedChildren(httpSpec, "path_parameter") {
		gotParameters[stringValue(mapping["name"])]++
	}
	for _, mapping := range namedChildren(httpSpec, "path_tail") {
		gotTails[stringValue(mapping["name"])]++
	}
	if !exactHTTPMappingNames(wantParameters, gotParameters) {
		return []Diagnostic{{Code: "SCN2110", Severity: "error", Message: "every HTTP path parameter requires exactly one matching mapping", Address: binding.Address}}
	}
	if !exactHTTPMappingNames(wantTails, gotTails) {
		return []Diagnostic{{Code: "SCN2110", Severity: "error", Message: "every HTTP path tail requires exactly one matching path_tail mapping", Address: binding.Address}}
	}
	return nil
}

func exactHTTPMappingNames(want, got map[string]int) bool {
	if len(want) != len(got) {
		return false
	}
	for name, count := range want {
		if count != 1 || got[name] != 1 {
			return false
		}
	}
	return true
}

func validateHTTPResponses(resources map[string]Resource, binding, operation Resource, httpSpec map[string]any) []Diagnostic {
	needed := map[string]bool{}
	delivery := stringValue(binding.Spec["delivery"])
	if delivery == "call" || delivery == "wait" {
		for _, kind := range []string{"result", "error"} {
			for _, outcome := range namedChildren(operation.Spec, kind) {
				needed[kind+"."+stringValue(outcome["name"])] = true
			}
		}
	}
	if delivery == "enqueue" {
		needed["dispatch.enqueued"] = true
		needed["dispatch.rejected"] = true
	}
	if delivery == "wait" {
		needed["dispatch.rejected"] = true
		needed["dispatch.wait_timeout"] = true
	}
	seen := map[string]bool{}
	statuses := map[int][]string{}
	var diagnostics []Diagnostic
	for _, response := range namedChildren(httpSpec, "response") {
		outcome := refOrString(response["when"])
		if seen[outcome] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2111", Severity: "error", Message: "HTTP outcome has more than one response mapping", Address: binding.Address})
		}
		seen[outcome] = true
		status, err := strconv.Atoi(stringValue(response["status"]))
		if err != nil || status < 100 || status > 599 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2112", Severity: "error", Message: "HTTP response status must be between 100 and 599", Address: binding.Address})
		} else {
			statuses[status] = append(statuses[status], outcome)
		}
		if err == nil && (status == 204 || status == 304) && response["body"] != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2112", Severity: "error", Message: fmt.Sprintf("HTTP %d response cannot have a body", status), Address: binding.Address})
		}
		if body, _ := response["body"].(map[string]any); body != nil {
			if body["codec"] == "raw" {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2103", Severity: "error", Message: "codec = raw is not part of scenery.http-codec/v1", Address: binding.Address})
			}
			seenMedia := map[string]bool{}
			for _, mediaType := range literalStringListFromValue(body["produced_media_types"]) {
				parsed, _, parseErr := mime.ParseMediaType(mediaType)
				if parseErr != nil || parsed == "" || seenMedia[mediaType] {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response produced_media_types must be valid and unique", Address: binding.Address})
					break
				}
				seenMedia[mediaType] = true
			}
		}
		diagnostics = append(diagnostics, validateHTTPResponseMappings(resources, binding, operation, outcome, response)...)
	}
	for status := range statuses {
		type completionResponse struct {
			outcome  string
			response map[string]any
		}
		var completions []completionResponse
		for _, response := range namedChildren(httpSpec, "response") {
			if responseStatus, _ := strconv.Atoi(stringValue(response["status"])); responseStatus != status {
				continue
			}
			outcome := refOrString(response["when"])
			if !strings.HasPrefix(outcome, "result.") && !strings.HasPrefix(outcome, "error.") && outcome != "dispatch.enqueued" {
				continue
			}
			for _, previous := range completions {
				if !httpResponseDecodersDisjoint(resources, operation, previous.response, response) {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("HTTP status %d has wire-indistinguishable typed completion mappings %s and %s", status, previous.outcome, outcome), Address: binding.Address})
				}
			}
			completions = append(completions, completionResponse{outcome: outcome, response: response})
		}
	}
	for outcome := range needed {
		if !seen[outcome] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2111", Severity: "error", Message: "reachable outcome " + outcome + " has no HTTP response mapping", Address: binding.Address})
		}
	}
	return diagnostics
}

func validateHTTPResponseMappings(resources map[string]Resource, binding, operation Resource, outcome string, response map[string]any) []Diagnostic {
	type responseMapping struct {
		kind  string
		value map[string]any
	}
	var mappings []responseMapping
	if body, _ := response["body"].(map[string]any); body != nil {
		mappings = append(mappings, responseMapping{kind: "body", value: body})
	}
	for _, kind := range []string{"header", "cookie"} {
		for _, mapping := range namedChildren(response, kind) {
			mappings = append(mappings, responseMapping{kind: kind, value: mapping})
		}
	}

	seenPaths := map[string]string{}
	seenNames := map[string]bool{}
	var diagnostics []Diagnostic
	for _, mapping := range mappings {
		reference := refOrString(mapping.value["from"])
		if err := validateHTTPOutcomeValueRef(resources, operation, outcome, reference); err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response " + mapping.kind + " " + err.Error(), Address: binding.Address})
			continue
		}
		valueType, path, err := httpOutcomeMappedValueType(resources, operation, outcome, reference)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response " + mapping.kind + " " + err.Error(), Address: binding.Address})
			continue
		}
		pathKey := strings.Join(path, ".")
		for existing, owner := range seenPaths {
			if responsePathsOverlap(existing, pathKey) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: fmt.Sprintf("HTTP response %s mapping overlaps %s at %q", mapping.kind, owner, pathKey), Address: binding.Address})
				break
			}
		}
		seenPaths[pathKey] = mapping.kind

		switch mapping.kind {
		case "body":
			if !httpResponseBodyCodecSupports(stringValue(mapping.value["codec"]), valueType) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response body codec is incompatible with its mapped value", Address: binding.Address})
			}
		case "header":
			name := stringValue(mapping.value["name"])
			if name == "" || name != strings.ToLower(name) || !httpHeaderNamePattern.MatchString(name) || name == "set-cookie" || seenNames["header\x00"+name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2115", Severity: "error", Message: "HTTP response headers require unique canonical lower-case names other than set-cookie", Address: binding.Address})
			}
			seenNames["header\x00"+name] = true
			encoding := defaultString(stringValue(mapping.value["encoding"]), "repeated")
			if !httpResponseHeaderTypeSupported(valueType, encoding, resources, operation.Module) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response header requires a supported scalar, scalar collection, or explicit JSON encoding", Address: binding.Address})
			}
		case "cookie":
			name := stringValue(mapping.value["name"])
			if name == "" || !httpHeaderNamePattern.MatchString(name) || seenNames["cookie\x00"+name] {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2115", Severity: "error", Message: "HTTP response cookies require unique valid names", Address: binding.Address})
			}
			seenNames["cookie\x00"+name] = true
			if !httpResponseCookieTypeSupported(valueType, resources, operation.Module) {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN2114", Severity: "error", Message: "HTTP response cookie requires an optional or required scalar with a supported wire codec", Address: binding.Address})
			}
			diagnostics = append(diagnostics, validateHTTPResponseCookie(binding, mapping.value)...)
		}
	}

	rootType := httpOutcomeRootType(operation, outcome)
	for _, required := range httpResponseRequiredPaths(resources, operation.Module, rootType, nil, map[string]bool{}) {
		covered := false
		for mapped := range seenPaths {
			if mapped == "" || mapped == required || strings.HasPrefix(required, mapped+".") {
				covered = true
				break
			}
		}
		if !covered {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN2113", Severity: "error", Message: "HTTP response leaves required outcome value " + defaultString(required, "<root>") + " unmapped", Address: binding.Address})
		}
	}
	return diagnostics
}

func responsePathsOverlap(left, right string) bool {
	return left == right || left == "" || right == "" || strings.HasPrefix(left, right+".") || strings.HasPrefix(right, left+".")
}

func httpOutcomeRootType(operation Resource, outcome string) any {
	parts := strings.Split(outcome, ".")
	if len(parts) == 2 && (parts[0] == "result" || parts[0] == "error") {
		return operationVariantType(operation, parts[0], parts[1])
	}
	if outcome == "dispatch.enqueued" {
		return map[string]any{"$ref": "std.type.execution_receipt"}
	}
	return map[string]any{"$ref": "std.type.problem"}
}

func httpOutcomeMappedValueType(resources map[string]Resource, operation Resource, outcome, reference string) (any, []string, error) {
	rootType := httpOutcomeRootType(operation, outcome)
	parts := strings.Split(reference, ".")
	path := parts[2:]
	if len(path) == 0 {
		return rootType, nil, nil
	}
	switch refOrString(rootType) {
	case "std.type.problem":
		return map[string]any{"$ref": "string"}, path, nil
	case "std.type.execution_receipt":
		if path[0] == "status_url" {
			return map[string]any{"$ref": "url"}, path, nil
		}
		return map[string]any{"$ref": "string"}, path, nil
	default:
		valueType := recordFieldType(resources, operation.Module, rootType, path)
		if valueType == nil {
			return nil, nil, fmt.Errorf("references an unknown outcome field")
		}
		return valueType, path, nil
	}
}

func httpResponseRequiredPaths(resources map[string]Resource, module string, value any, prefix []string, visiting map[string]bool) []string {
	expression := strings.TrimSpace(typeExpression(value))
	if wrappedType(expression, "optional") {
		return nil
	}
	if wrappedType(expression, "nullable") {
		return []string{strings.Join(prefix, ".")}
	}
	if expression == "std.type.problem" {
		return []string{joinResponsePath(prefix, "code"), joinResponsePath(prefix, "message")}
	}
	if expression == "std.type.unit" {
		return nil
	}
	if expression == "std.type.execution_receipt" {
		return []string{joinResponsePath(prefix, "durable_identity"), joinResponsePath(prefix, "execution_id"), joinResponsePath(prefix, "accepted_revision")}
	}
	record, ok := recordResourceForType(resources, module, value)
	if !ok {
		return []string{strings.Join(prefix, ".")}
	}
	address := record.Address
	if visiting[address] {
		return []string{strings.Join(prefix, ".")}
	}
	module = record.Module
	nextVisiting := make(map[string]bool, len(visiting)+1)
	for key, set := range visiting {
		nextVisiting[key] = set
	}
	nextVisiting[address] = true
	var required []string
	for _, field := range namedChildren(record.Spec, "field") {
		if isOptionalType(field["type"]) {
			continue
		}
		fieldPrefix := append(append([]string(nil), prefix...), stringValue(field["name"]))
		child := httpResponseRequiredPaths(resources, module, field["type"], fieldPrefix, nextVisiting)
		if len(child) == 0 {
			child = []string{strings.Join(fieldPrefix, ".")}
		}
		required = append(required, child...)
	}
	return required
}

func joinResponsePath(prefix []string, field string) string {
	return strings.Join(append(append([]string(nil), prefix...), field), ".")
}

func httpResponseBodyCodecSupports(codec string, value any) bool {
	expression := unwrapHTTPType(typeExpression(value))
	switch codec {
	case "json":
		return expression != "std.type.problem"
	case "problem_json":
		return expression == "std.type.problem"
	case "text":
		return expression == "string" || strings.HasPrefix(expression, "enum.")
	case "bytes":
		return expression == "bytes"
	default:
		return false
	}
}

func httpResponseHeaderTypeSupported(value any, encoding string, resources map[string]Resource, module string) bool {
	if encoding == "json" {
		return true
	}
	if hasTypeWrapper(typeExpression(value), "nullable") {
		return false
	}
	return httpMappedTypeSupported(value, encoding, resources, module)
}

func httpResponseCookieTypeSupported(value any, resources map[string]Resource, module string) bool {
	if hasTypeWrapper(typeExpression(value), "nullable") {
		return false
	}
	expression := unwrapHTTPType(typeExpression(value))
	return !strings.HasPrefix(expression, "list(") && !strings.HasPrefix(expression, "set(") && httpPathScalarType(map[string]any{"$ref": expression}, resources, module)
}

func validateHTTPResponseCookie(binding Resource, cookie map[string]any) []Diagnostic {
	pathValue := defaultString(stringValue(cookie["path"]), "/")
	domain := stringValue(cookie["domain"])
	maxAge, maxAgeOK := integerValue(cookie["max_age"])
	expires := stringValue(cookie["expires"])
	sameSite := defaultString(stringValue(cookie["same_site"]), "lax")
	secure, _ := cookie["secure"].(bool)
	probe := &http.Cookie{Name: stringValue(cookie["name"]), Value: "value", Path: pathValue, Domain: domain}
	if err := probe.Valid(); err != nil || !strings.HasPrefix(pathValue, "/") || !maxAgeOK || maxAge < 0 || expires != "" && !validHTTPDateTime(expires) || !eventStringIn([]string{"lax", "strict", "none"}, sameSite) || sameSite == "none" && !secure {
		return []Diagnostic{{Code: "SCN2115", Severity: "error", Message: "HTTP response cookie attributes are invalid; SameSite=None also requires Secure", Address: binding.Address}}
	}
	return nil
}

func validHTTPDateTime(value string) bool {
	parsed, err := scenery.ParseDateTime(value)
	return err == nil && parsed.String() == value
}

func validateHTTPOutcomeValueRef(resources map[string]Resource, operation Resource, outcome, reference string) error {
	if reference == "" {
		return fmt.Errorf("requires a typed from reference")
	}
	if !strings.HasPrefix(outcome, "result.") && !strings.HasPrefix(outcome, "error.") {
		if outcome == "dispatch.enqueued" {
			parts := strings.Split(reference, ".")
			if len(parts) == 2 && reference == "dispatch.receipt" {
				return nil
			}
			if len(parts) == 3 && parts[0] == "dispatch" && parts[1] == "receipt" {
				switch parts[2] {
				case "durable_identity", "execution_id", "accepted_revision", "status_url":
					return nil
				}
			}
			return fmt.Errorf("from reference does not match dispatch.enqueued receipt")
		}
		problem := standardProblemSource(outcome)
		if reference == problem {
			return nil
		}
		if strings.HasPrefix(reference, problem+".") {
			parts := strings.Split(reference, ".")
			if len(parts) == 3 && (parts[2] == "code" || parts[2] == "message" || parts[2] == "path") {
				return nil
			}
		}
		return fmt.Errorf("from reference does not match %s", outcome)
	}
	parts := strings.Split(reference, ".")
	outcomeParts := strings.Split(outcome, ".")
	if len(parts) < 2 || parts[0] != outcomeParts[0] || parts[1] != outcomeParts[1] {
		return fmt.Errorf("from reference does not match %s", outcome)
	}
	if len(parts) == 2 {
		return nil
	}
	variantType := operationVariantType(operation, outcomeParts[0], outcomeParts[1])
	if refOrString(variantType) == "std.type.problem" {
		if len(parts) == 3 && (parts[2] == "code" || parts[2] == "message" || parts[2] == "path") {
			return nil
		}
		return fmt.Errorf("references an unknown problem field")
	}
	if recordFieldType(resources, operation.Module, variantType, parts[2:]) != nil {
		return nil
	}
	return fmt.Errorf("references an unknown outcome field")
}

func refOrString(value any) string {
	if ref := refString(value); ref != "" {
		return ref
	}
	return stringValue(value)
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	if scalar, ok := value.(map[string]any); ok {
		switch scalar["$scalar"] {
		case "int":
			text, _ := scalar["value"].(string)
			return text
		case "decimal":
			coefficient, _ := scalar["coefficient"].(string)
			scale, err := strconv.Atoi(fmt.Sprint(scalar["scale"]))
			if err == nil {
				return decimalScalarText(coefficient, scale)
			}
		case "duration":
			nanoseconds, err := strconv.ParseInt(fmt.Sprint(scalar["nanoseconds"]), 10, 64)
			if err == nil {
				return time.Duration(nanoseconds).String()
			}
		case "size":
			return fmt.Sprint(scalar["bytes"])
		default:
			if text, ok := scalar["value"].(string); ok {
				return text
			}
		}
	}
	return ""
}

func decimalScalarText(coefficient string, scale int) string {
	negative := strings.HasPrefix(coefficient, "-")
	digits := strings.TrimPrefix(coefficient, "-")
	if digits == "" || digits == "0" {
		return "0"
	}
	if scale <= 0 {
		if negative {
			return "-" + digits
		}
		return digits
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	position := len(digits) - scale
	value := digits[:position] + "." + digits[position:]
	if negative {
		value = "-" + value
	}
	return value
}

func widerExposure(binding, gateway string) bool {
	if binding == "" || gateway == "" {
		return false
	}
	rank := map[string]int{"local": 0, "application": 1, "private_network": 2, "internet": 3}
	bindingRank, bindingOK := rank[binding]
	gatewayRank, gatewayOK := rank[gateway]
	return bindingOK && gatewayOK && bindingRank > gatewayRank
}

func resolveResourceRef(resource Resource, reference, kind string) string {
	if reference == "" {
		return ""
	}
	if strings.Contains(reference, "/") {
		return reference
	}
	parts := strings.Split(reference, ".")
	if len(parts) == 2 {
		module := resource.Module
		if rootResourceKinds[kind] || rootResourceKinds[parts[0]] {
			module = "app"
		}
		return resourceAddress(module, parts[0], parts[1])
	}
	return reference
}

func validHTTPPath(value string) bool {
	_, err := parseHTTPPathTemplate(value)
	return err == nil
}

func joinHTTPPath(base, binding string) string {
	if base == "" || base == "/" {
		return binding
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(binding, "/")
}
