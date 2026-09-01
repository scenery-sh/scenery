package generate

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type tsClientMethod struct {
	name          string
	inputName     string
	operationName string
	descriptor    map[string]any
}

func tsClientMethods(bindings, resources []Resource) []tsClientMethod {
	ops := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind == "scenery.operation" {
			ops[resource.Name] = resource
		}
	}
	operationCounts := map[string]int{}
	for _, binding := range bindings {
		operationCounts[lastRef(refString(binding.Spec["operation"]))]++
	}
	methods := make([]tsClientMethod, 0, len(bindings))
	for _, binding := range bindings {
		opName := lastRef(refString(binding.Spec["operation"]))
		operation := ops[opName]
		methodName := tsName(opName)
		if operationCounts[opName] > 1 {
			methodName += "Via" + goName(binding.Name)
		}
		inputName := "input"
		if refString(operation.Spec["input"]) == "std.type.unit" {
			inputName = "_input"
		}
		methods = append(methods, tsClientMethod{
			name:          methodName,
			inputName:     inputName,
			operationName: goName(opName),
			descriptor:    tsBindingCall(binding, operation, resources),
		})
	}
	return methods
}

type tsResponseToken struct {
	json   string
	shared string
}

func renderTSBindingsTable(methods []tsClientMethod) string {
	type caseInfo struct {
		json  string
		raw   any
		count int
		key   string
	}
	byJSON := map[string]*caseInfo{}
	var firstSeen []string
	for _, method := range methods {
		for _, response := range tsBindingResponseList(method.descriptor) {
			encoded, _ := json.Marshal(response)
			canonical := string(encoded)
			if info, ok := byJSON[canonical]; ok {
				info.count++
				continue
			}
			byJSON[canonical] = &caseInfo{json: canonical, raw: response, count: 1}
			firstSeen = append(firstSeen, canonical)
		}
	}
	usedKeys := map[string]string{}
	for _, canonical := range firstSeen {
		info := byJSON[canonical]
		if info.count < 2 {
			continue
		}
		info.key = uniqueSharedResponseKey(info.raw, canonical, usedKeys)
		usedKeys[info.key] = canonical
	}

	methodTokens := make([][]tsResponseToken, len(methods))
	runCount := map[string]int{}
	var runOrder []string
	for index, method := range methods {
		tokens := make([]tsResponseToken, 0)
		for _, response := range tsBindingResponseList(method.descriptor) {
			encoded, _ := json.Marshal(response)
			canonical := string(encoded)
			token := tsResponseToken{json: canonical, shared: byJSON[canonical].key}
			tokens = append(tokens, token)
		}
		methodTokens[index] = tokens
		for _, run := range maximalSharedResponseRuns(tokens) {
			if runCount[run] == 0 {
				runOrder = append(runOrder, run)
			}
			runCount[run]++
		}
	}
	internedRuns := map[string]string{}
	setNumber := 0
	for _, run := range runOrder {
		if runCount[run] < 2 {
			continue
		}
		internedRuns[run] = fmt.Sprintf("s%d", setNumber)
		setNumber++
	}

	var b strings.Builder
	if len(usedKeys) > 0 {
		b.WriteString("const sharedResponses = {")
		first := true
		for _, canonical := range firstSeen {
			info := byJSON[canonical]
			if info.key == "" {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, "%s:%s", info.key, info.json)
		}
		b.WriteString("} as const satisfies Record<string, Runtime.BindingResponseCase>;\n")
	}
	if len(internedRuns) > 0 {
		b.WriteString("const sharedResponseSets = {")
		first := true
		for _, run := range runOrder {
			name, ok := internedRuns[run]
			if !ok {
				continue
			}
			if !first {
				b.WriteByte(',')
			}
			first = false
			fmt.Fprintf(&b, "%s:[%s]", name, sharedResponseRunLiteral(run))
		}
		b.WriteString("} as const;\n")
	}
	b.WriteString("const bindings = {")
	for index, method := range methods {
		if index > 0 {
			b.WriteByte(',')
		}
		rest := make(map[string]any, len(method.descriptor))
		for key, value := range method.descriptor {
			if key != "responses" {
				rest[key] = value
			}
		}
		encoded, _ := json.Marshal(rest)
		fmt.Fprintf(&b, "%q:%s", method.name, injectJSONField(string(encoded), `"responses":`+renderTSResponseTokens(methodTokens[index], internedRuns)))
	}
	b.WriteString("} as const satisfies Record<string, Runtime.BindingCall>;\n")
	return b.String()
}

func tsBindingResponseList(descriptor map[string]any) []any {
	responses, _ := descriptor["responses"].([]any)
	if responses == nil {
		return []any{}
	}
	return responses
}

func uniqueSharedResponseKey(raw any, canonical string, used map[string]string) string {
	base := sharedResponseKey(raw)
	key := base
	suffix := 2
	for {
		if existing, ok := used[key]; !ok || existing == canonical {
			return key
		}
		key = fmt.Sprintf("%s%d", base, suffix)
		suffix++
	}
}

func sharedResponseKey(raw any) string {
	item, _ := raw.(map[string]any)
	status := fmt.Sprint(item["status"])
	role, _ := item["role"].(string)
	name, _ := item["name"].(string)
	if code, _ := item["problemCode"].(string); code != "" {
		name = lastRef(code)
	}
	key := role + "_" + status + "_" + tsName(name)
	if !tsJSIdent(key) {
		return "r"
	}
	return key
}

func tsJSIdent(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		if char != '_' && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && (index == 0 || char < '0' || char > '9') {
			return false
		}
	}
	return true
}

func maximalSharedResponseRuns(tokens []tsResponseToken) []string {
	var runs []string
	start := -1
	flush := func(end int) {
		if start >= 0 && end-start >= 2 {
			keys := make([]string, 0, end-start)
			for _, token := range tokens[start:end] {
				keys = append(keys, token.shared)
			}
			runs = append(runs, strings.Join(keys, ","))
		}
		start = -1
	}
	for index, token := range tokens {
		if token.shared == "" {
			flush(index)
			continue
		}
		if start < 0 {
			start = index
		}
	}
	flush(len(tokens))
	return runs
}

func sharedResponseRunLiteral(run string) string {
	keys := strings.Split(run, ",")
	parts := make([]string, len(keys))
	for index, key := range keys {
		parts[index] = "sharedResponses." + key
	}
	return strings.Join(parts, ",")
}

func renderTSResponseTokens(tokens []tsResponseToken, internedRuns map[string]string) string {
	if len(tokens) == 0 {
		return "[]"
	}
	var b strings.Builder
	b.WriteByte('[')
	first := true
	emit := func(part string) {
		if !first {
			b.WriteByte(',')
		}
		first = false
		b.WriteString(part)
	}
	for index := 0; index < len(tokens); {
		if run := sharedResponseRunAt(tokens, index); internedRuns[run] != "" {
			emit("...sharedResponseSets." + internedRuns[run])
			index += strings.Count(run, ",") + 1
			continue
		}
		token := tokens[index]
		if token.shared != "" {
			emit("sharedResponses." + token.shared)
		} else {
			emit(token.json)
		}
		index++
	}
	b.WriteByte(']')
	return b.String()
}

func sharedResponseRunAt(tokens []tsResponseToken, start int) string {
	if start >= len(tokens) || tokens[start].shared == "" {
		return ""
	}
	end := start + 1
	for end < len(tokens) && tokens[end].shared != "" {
		end++
	}
	if end-start < 2 {
		return ""
	}
	keys := make([]string, 0, end-start)
	for _, token := range tokens[start:end] {
		keys = append(keys, token.shared)
	}
	return strings.Join(keys, ",")
}

func injectJSONField(objectJSON, fieldJS string) string {
	inner := strings.TrimSuffix(strings.TrimPrefix(objectJSON, "{"), "}")
	if inner == "" {
		return "{" + fieldJS + "}"
	}
	return "{" + inner + "," + fieldJS + "}"
}

func tsBindingCall(binding, operation Resource, resources []Resource) map[string]any {
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	method, _ := httpSpec["method"].(string)
	path, _ := httpSpec["path"].(string)
	path = tsBindingPath(binding, path, resources)
	pathTails := namedChildren(httpSpec, "path_tail")
	if len(pathTails) == 1 {
		path = strings.TrimSuffix(path, "/{"+stringValue(pathTails[0]["name"])+"...}")
	}
	call := map[string]any{
		"address":            binding.Address,
		"method":             method,
		"path":               path,
		"responseLimitBytes": tsResponseLimitBytes(httpSpec),
		"responses":          tsBindingResponses(operation, httpSpec, resources),
	}
	if parameters := tsBindingFieldMappings(httpSpec, "path_parameter", operation, resources, false); len(parameters) > 0 {
		call["pathParameters"] = parameters
	}
	if len(pathTails) == 1 {
		call["pathTail"] = tsBindingFieldMapping(pathTails[0], operation, resources, false)
	}
	if query := tsBindingFieldMappings(httpSpec, "query_parameter", operation, resources, true); len(query) > 0 {
		call["query"] = query
	}
	if headers := tsBindingFieldMappings(httpSpec, "header", operation, resources, true); len(headers) > 0 {
		call["headers"] = headers
	}
	if cookies := tsBindingFieldMappings(httpSpec, "cookie", operation, resources, false); len(cookies) > 0 {
		call["cookies"] = cookies
	}
	if body := tsBindingRequestBody(httpSpec, operation, resources); body != nil {
		call["body"] = body
	}
	return call
}

func tsBindingFieldMappings(httpSpec map[string]any, kind string, operation Resource, resources []Resource, withEncoding bool) []map[string]any {
	children := namedChildren(httpSpec, kind)
	if len(children) == 0 {
		return nil
	}
	mappings := make([]map[string]any, 0, len(children))
	for _, mapping := range children {
		mappings = append(mappings, tsBindingFieldMapping(mapping, operation, resources, withEncoding))
	}
	return mappings
}

func tsBindingFieldMapping(mapping map[string]any, operation Resource, resources []Resource, withEncoding bool) map[string]any {
	result := map[string]any{
		"name":     stringValue(mapping["name"]),
		"property": tsInputTargetProperty(mapping["to"]),
		"value":    tsDescriptor(tsOperationFieldType(operation, resources, mapping["to"]), operation.Module),
	}
	if withEncoding {
		result["encoding"] = defaultTSQueryEncoding(stringValue(mapping["encoding"]))
	}
	return result
}

func tsBindingRequestBody(httpSpec map[string]any, operation Resource, resources []Resource) map[string]any {
	body, _ := httpSpec["body"].(map[string]any)
	if body == nil {
		return nil
	}
	codec := stringValue(body["codec"])
	result := map[string]any{"codec": codec}
	target := refOrString(body["to"])
	if target != "" && target != "operation."+operation.Name+".input" {
		result["property"] = tsInputTargetProperty(body["to"])
		result["value"] = tsDescriptor(tsOperationFieldType(operation, resources, body["to"]), operation.Module)
	} else if selected := tsSelectedBodyFields(body, operation, resources); selected != nil {
		properties := make([]string, 0, len(selected))
		fields := make([]any, 0, len(selected))
		for _, field := range selected {
			name := stringValue(field["name"])
			property := tsName(name)
			properties = append(properties, property)
			descriptor := map[string]any{
				"property": property,
				"wire":     wireName(field, name),
				"value":    tsDescriptor(field["type"], operation.Module),
				"optional": isOptionalType(field["type"]),
			}
			if constraints := tsFieldConstraints(field); len(constraints) > 0 {
				descriptor["constraints"] = constraints
			}
			fields = append(fields, descriptor)
		}
		result["select"] = properties
		result["value"] = map[string]any{"kind": "record", "fields": fields, "preserveUnknown": false}
	} else {
		result["value"] = tsDescriptor(operation.Spec["input"], operation.Module)
	}
	if codec == "multipart" {
		result["multipart"] = tsMultipartBodyDescriptorValue(httpSpec, operation, resources)
		return result
	}
	requestMediaTypes := literalStringListFromValue(body["accepted_media_types"])
	if len(requestMediaTypes) == 0 {
		requestMediaTypes = []string{defaultHTTPMediaType(codec)}
	}
	result["contentType"] = requestMediaTypes[0]
	return result
}

func tsBindingResponses(operation Resource, httpSpec map[string]any, resources []Resource) []any {
	groups := map[int][]map[string]any{}
	seen := map[int]bool{}
	var statuses []int
	for _, response := range namedChildren(httpSpec, "response") {
		status := tsResponseStatus(response)
		if !seen[status] {
			seen[status] = true
			statuses = append(statuses, status)
		}
		groups[status] = append(groups[status], response)
	}
	sort.Ints(statuses)
	out := make([]any, 0)
	for _, status := range statuses {
		var failures, completions []map[string]any
		for _, response := range groups[status] {
			when := refString(response["when"])
			if strings.HasPrefix(when, "result.") || strings.HasPrefix(when, "error.") || when == "dispatch.enqueued" {
				completions = append(completions, response)
			} else {
				failures = append(failures, response)
			}
		}
		for _, response := range failures {
			out = append(out, tsBindingResponseCase(operation, response, resources, status, "failure"))
		}
		for _, response := range completions {
			out = append(out, tsBindingResponseCase(operation, response, resources, status, "completion"))
		}
	}
	return out
}

func tsBindingResponseCase(operation Resource, response map[string]any, resources []Resource, status int, role string) map[string]any {
	when := refString(response["when"])
	name := stringValue(response["name"])
	if name == "" {
		name = lastRef(when)
	}
	kind := "failure"
	switch {
	case strings.HasPrefix(when, "result."):
		kind = "result"
	case strings.HasPrefix(when, "error."):
		kind = "error"
	case when == "dispatch.enqueued":
		kind = "enqueue"
	}
	item := map[string]any{
		"status": status,
		"role":   role,
		"kind":   kind,
		"name":   name,
	}
	if role == "failure" {
		item["problemCode"] = when
		if strings.HasPrefix(when, "system.") {
			item["throwOnMatch"] = true
		}
	}
	if body := tsBindingResponseBody(operation, response, resources); body != nil {
		item["body"] = body
	}
	if headers := tsBindingResponseHeaders(operation, response, resources); len(headers) > 0 {
		item["headers"] = headers
	}
	if cookies := tsBindingResponseCookies(operation, response, resources); len(cookies) > 0 {
		item["cookies"] = cookies
	}
	return item
}

func tsBindingResponseBody(operation Resource, response map[string]any, resources []Resource) map[string]any {
	body, _ := response["body"].(map[string]any)
	if body == nil {
		return nil
	}
	when := refString(response["when"])
	valueType, path := tsResponseMappedValue(operation, when, refOrString(body["from"]), resources)
	codec := stringValue(body["codec"])
	produced := literalStringListFromValue(body["produced_media_types"])
	if len(produced) == 0 {
		produced = []string{defaultHTTPMediaType(codec)}
	}
	return map[string]any{
		"codec":              codec,
		"producedMediaTypes": produced,
		"path":               tsResponsePath(path),
		"value":              tsDescriptor(valueType, operation.Module),
	}
}

func tsBindingResponseHeaders(operation Resource, response map[string]any, resources []Resource) []map[string]any {
	when := refString(response["when"])
	children := namedChildren(response, "header")
	if len(children) == 0 {
		return nil
	}
	headers := make([]map[string]any, 0, len(children))
	for _, header := range children {
		valueType, path := tsResponseMappedValue(operation, when, refOrString(header["from"]), resources)
		headers = append(headers, map[string]any{
			"name":     stringValue(header["name"]),
			"encoding": defaultString(stringValue(header["encoding"]), "repeated"),
			"path":     tsResponsePath(path),
			"value":    tsDescriptor(valueType, operation.Module),
		})
	}
	return headers
}

func tsBindingResponseCookies(operation Resource, response map[string]any, resources []Resource) []map[string]any {
	when := refString(response["when"])
	children := namedChildren(response, "cookie")
	if len(children) == 0 {
		return nil
	}
	cookies := make([]map[string]any, 0, len(children))
	for _, cookie := range children {
		valueType, path := tsResponseMappedValue(operation, when, refOrString(cookie["from"]), resources)
		cookies = append(cookies, map[string]any{
			"name":  stringValue(cookie["name"]),
			"path":  tsResponsePath(path),
			"value": tsDescriptor(valueType, operation.Module),
		})
	}
	return cookies
}

func tsResponsePath(path []string) []string {
	if path == nil {
		return []string{}
	}
	return path
}

func tsResponseStatus(response map[string]any) int {
	if value, ok := integerValue(response["status"]); ok {
		return value
	}
	status, _ := strconv.Atoi(integerText(response["status"]))
	return status
}

func tsResponseLimitBytes(httpSpec map[string]any) int {
	maximum := 16 << 20
	if limits, _ := httpSpec["response_limit"].(map[string]any); limits != nil {
		if value, ok := integerValue(limits["bytes"]); ok && value > 0 {
			maximum = value
		}
	}
	return maximum
}

func tsResponseMappedValue(operation Resource, when, from string, resources []Resource) (any, []string) {
	valueType := tsResponseValueType(operation, when)
	parts := strings.Split(from, ".")
	if len(parts) <= 2 {
		return valueType, nil
	}
	semanticPath := parts[2:]
	if refOrString(valueType) == "std.type.problem" {
		return map[string]any{"$ref": "string"}, semanticPath
	}
	if refOrString(valueType) == "std.type.execution_receipt" {
		property := map[string]string{
			"durable_identity":  "durableIdentity",
			"execution_id":      "executionId",
			"accepted_revision": "acceptedRevision",
			"status_url":        "statusUrl",
		}[semanticPath[0]]
		fieldType := any(map[string]any{"$ref": "string"})
		if semanticPath[0] == "status_url" {
			fieldType = map[string]any{"$ref": "url"}
		}
		return fieldType, []string{property}
	}
	resourceMap := resourcesByAddress(&Manifest{Resources: resources})
	fieldType := recordFieldType(resourceMap, operation.Module, valueType, semanticPath)
	if fieldType == nil {
		return valueType, nil
	}
	return fieldType, tsRecordPropertyPath(resourceMap, operation.Module, valueType, semanticPath)
}

func tsRecordPropertyPath(resources map[string]Resource, module string, value any, path []string) []string {
	current := value
	properties := make([]string, 0, len(path))
	for _, name := range path {
		record, ok := recordResourceForType(resources, module, current)
		if !ok {
			return nil
		}
		module = record.Module
		found := false
		for _, field := range namedChildren(record.Spec, "field") {
			if stringValue(field["name"]) == name {
				properties = append(properties, tsName(name))
				current = field["type"]
				found = true
				break
			}
		}
		if !found {
			return nil
		}
	}
	return properties
}

func tsResponseValueType(operation Resource, when string) any {
	variant := lastRef(when)
	if strings.HasPrefix(when, "result.") {
		return operationVariantType(operation, "result", variant)
	}
	if strings.HasPrefix(when, "error.") {
		return operationVariantType(operation, "error", variant)
	}
	if when == "dispatch.enqueued" {
		return map[string]any{"$ref": "std.type.execution_receipt"}
	}
	return map[string]any{"$ref": "std.type.problem"}
}
