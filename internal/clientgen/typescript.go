package clientgen

import (
	"bytes"
	"fmt"
	"go/types"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
)

type TypeScriptOptions struct {
	AppSlug            string
	StandardAuth       bool
	StandardAuthGoogle bool
}

type tsGenerator struct {
	app         *model.App
	opts        TypeScriptOptions
	namespaces  map[string]*tsNamespace
	namespaceMu []string
	namedTypes  map[string]string
	inProgress  map[string]bool
}

type tsNamespace struct {
	Name      string
	typeOrder []string
	types     map[string]string
	methods   []string
}

type requestFieldLocation string

const (
	requestFieldBody   requestFieldLocation = "body"
	requestFieldQuery  requestFieldLocation = "query"
	requestFieldHeader requestFieldLocation = "header"
	requestFieldCookie requestFieldLocation = "cookie"
)

type requestField struct {
	Name     string
	JSONName string
	Type     types.Type
	Location requestFieldLocation
	Target   string
}

var tsIdentifier = regexp.MustCompile(`^[A-Za-z_$][A-Za-z0-9_$]*$`)

func GenerateTypeScript(app *model.App, opts TypeScriptOptions) ([]byte, error) {
	if app == nil {
		return nil, fmt.Errorf("nil app")
	}
	gen := &tsGenerator{
		app:        app,
		opts:       opts,
		namespaces: make(map[string]*tsNamespace),
		namedTypes: make(map[string]string),
		inProgress: make(map[string]bool),
	}

	for _, svc := range app.Services {
		if !tsIdentifier.MatchString(svc.Name) {
			continue
		}
		ns := gen.namespace(svc.Name)
		for _, ep := range svc.Endpoints {
			if ep.Access == runtimeapi.Private {
				continue
			}
			method, err := gen.renderEndpointMethod(svc.Name, ep)
			if err != nil {
				return nil, err
			}
			ns.methods = append(ns.methods, method)
		}
	}

	return gen.render()
}

func (g *tsGenerator) renderEndpointMethod(namespace string, ep *model.Endpoint) (string, error) {
	if ep == nil {
		return "", fmt.Errorf("nil endpoint")
	}

	pathExpr := renderPathExpression(ep.Path)
	if ep.Raw {
		return g.renderRawMethod(pathExpr, ep)
	}

	regular, err := g.renderTypedMethod(namespace, ep, pathExpr, false)
	if err != nil {
		return "", err
	}
	withMeta, err := g.renderTypedMethod(namespace, ep, pathExpr, true)
	if err != nil {
		return "", err
	}
	return regular + "\n\n" + withMeta, nil
}

func (g *tsGenerator) renderTypedMethod(namespace string, ep *model.Endpoint, pathExpr string, withMeta bool) (string, error) {
	methodName := preferredHTTPMethod(ep.Methods)
	params := make([]string, 0, len(ep.PathParams)+1)
	for _, pathParam := range ep.PathParams {
		params = append(params, fmt.Sprintf("%s: %s", pathParam.Name, tsTypeForParamKind(pathParam.Kind)))
	}

	payloadType := ""
	if ep.Payload != nil && ep.Payload.Type != nil {
		var err error
		payloadType, err = g.typeRef(ep.Payload.Type, namespace)
		if err != nil {
			return "", err
		}
		params = append(params, fmt.Sprintf("params: %s", payloadType))
	}

	responseType := "void"
	if ep.Response != nil && ep.Response.Type != nil {
		var err error
		responseType, err = g.typeRef(ep.Response.Type, namespace)
		if err != nil {
			return "", err
		}
	}

	params = append(params, "options?: CallParameters")

	var lines []string
	if ep.Payload != nil && ep.Payload.Type != nil {
		fields, ok := extractRequestFields(ep.Payload.Type, queryDefaultMethod(methodName))
		if ok {
			var queryLines []string
			var headerLines []string
			var cookieLines []string
			var bodyLines []string
			for _, field := range fields {
				accessor := "params." + field.Name
				switch field.Location {
				case requestFieldQuery:
					queryLines = append(queryLines, fmt.Sprintf("%s: %s", tsPropertyName(field.Target), "encodeQueryValue("+accessor+")"))
				case requestFieldHeader:
					headerLines = append(headerLines, fmt.Sprintf("%q: encodeHeaderValue(%s)", field.Target, accessor))
				case requestFieldCookie:
					cookieLines = append(cookieLines, fmt.Sprintf("%q: encodeHeaderValue(%s)", field.Target, accessor))
				case requestFieldBody:
					bodyLines = append(bodyLines, fmt.Sprintf("%s: %s", tsPropertyName(field.JSONName), accessor))
				}
			}
			if len(queryLines) > 0 {
				lines = append(lines, "const query = makeRecord<string, string | string[]>({")
				for _, line := range queryLines {
					lines = append(lines, "    "+line+",")
				}
				lines = append(lines, "})")
			}
			if len(headerLines) > 0 || len(cookieLines) > 0 {
				lines = append(lines, "const headers = makeRecord<string, string>({")
				for _, line := range headerLines {
					lines = append(lines, "    "+line+",")
				}
				if len(cookieLines) > 0 {
					lines = append(lines, "    \"Cookie\": encodeCookieHeader(makeRecord<string, string>({")
					for _, line := range cookieLines {
						lines = append(lines, "        "+line+",")
					}
					lines = append(lines, "    })),")
				}
				lines = append(lines, "})")
			}

			bodyExpr := "undefined"
			if !queryDefaultMethod(methodName) {
				switch {
				case len(bodyLines) == 0:
					bodyExpr = "undefined"
				case canPassPayloadAsJSON(fields):
					bodyExpr = "JSON.stringify(params)"
				default:
					lines = append(lines, "const body = {")
					for _, line := range bodyLines {
						lines = append(lines, "    "+line+",")
					}
					lines = append(lines, "}")
					bodyExpr = "JSON.stringify(body)"
				}
			}

			optionsExpr := renderCallOptions(len(queryLines) > 0, len(headerLines) > 0 || len(cookieLines) > 0)
			lines = append(lines, renderTypedEndpointCall(ep, methodName, pathExpr, bodyExpr, optionsExpr, ep.Payload != nil, withMeta))
		} else {
			lines = append(lines, renderTypedEndpointCall(ep, methodName, pathExpr, "JSON.stringify(params)", "options", true, withMeta))
		}
	} else {
		lines = append(lines, renderTypedEndpointCall(ep, methodName, pathExpr, "undefined", "options", false, withMeta))
	}

	if withMeta {
		if responseType == "void" {
			lines = append(lines, "return typedVoidAPIResponse(resp)")
		} else {
			lines = append(lines, fmt.Sprintf("return await decodeTypedAPIResponse(resp) as APIResponse<%s>", responseType))
		}
	} else if responseType == "void" {
		lines[len(lines)-1] = strings.Replace(lines[len(lines)-1], "const resp = await ", "await ", 1)
	} else {
		lines = append(lines, fmt.Sprintf("return await decodeTypedResponse(resp) as %s", responseType))
	}

	var buf bytes.Buffer
	name := ep.Name
	returnType := responseType
	if withMeta {
		name += "WithMeta"
		returnType = fmt.Sprintf("APIResponse<%s>", responseType)
	}
	buf.WriteString(fmt.Sprintf("public async %s(%s): Promise<%s> {\n", name, strings.Join(params, ", "), returnType))
	for _, line := range lines {
		buf.WriteString("    " + line + "\n")
	}
	buf.WriteString("}")
	return buf.String(), nil
}

func (g *tsGenerator) renderRawMethod(pathExpr string, ep *model.Endpoint) (string, error) {
	rawPathParams := rawPathParamNames(ep.Path)
	params := make([]string, 0, len(rawPathParams)+3)
	for _, pathParam := range rawPathParams {
		params = append(params, fmt.Sprintf("%s: string", pathParam))
	}
	methodType := "string"
	if len(ep.Methods) == 1 && ep.Methods[0] != "*" {
		methodType = strconv.Quote(ep.Methods[0])
	}
	params = append(params, fmt.Sprintf("method: %s", methodType))
	params = append(params, `body?: RequestInit["body"]`)
	params = append(params, "options?: CallParameters")

	var buf bytes.Buffer
	buf.WriteString(fmt.Sprintf("public async %s(%s): Promise<globalThis.Response> {\n", ep.Name, strings.Join(params, ", ")))
	buf.WriteString(fmt.Sprintf("    return await this.baseClient.callAPI(method, %s, body, options)\n", pathExpr))
	buf.WriteString("}")
	return buf.String(), nil
}

func (g *tsGenerator) namespace(name string) *tsNamespace {
	if ns, ok := g.namespaces[name]; ok {
		return ns
	}
	ns := &tsNamespace{
		Name:  name,
		types: make(map[string]string),
	}
	g.namespaces[name] = ns
	g.namespaceMu = append(g.namespaceMu, name)
	return ns
}

func (g *tsGenerator) typeRef(typ types.Type, currentNamespace string) (string, error) {
	if typ == nil {
		return "JSONValue", nil
	}

	switch value := typ.(type) {
	case *types.Pointer:
		return g.typeRef(value.Elem(), currentNamespace)
	case *types.Named:
		if special, ok := specialTypeRef(value); ok {
			return special, nil
		}
		return g.ensureNamedType(currentNamespace, value)
	case *types.Alias:
		return g.typeRef(types.Unalias(value), currentNamespace)
	case *types.Basic:
		return tsPrimitive(value), nil
	case *types.Slice:
		if isByteSlice(value) {
			return "string", nil
		}
		elem, err := g.typeRef(value.Elem(), currentNamespace)
		if err != nil {
			return "", err
		}
		return elem + "[]", nil
	case *types.Array:
		elem, err := g.typeRef(value.Elem(), currentNamespace)
		if err != nil {
			return "", err
		}
		return elem + "[]", nil
	case *types.Map:
		elem, err := g.typeRef(value.Elem(), currentNamespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Record<string, %s>", elem), nil
	case *types.Struct:
		return g.inlineStructType(value, currentNamespace)
	case *types.Interface:
		return "JSONValue", nil
	default:
		return "JSONValue", nil
	}
}

func (g *tsGenerator) ensureNamedType(currentNamespace string, named *types.Named) (string, error) {
	id := namedTypeID(named)
	if ref, ok := g.namedTypes[id]; ok {
		return ref, nil
	}
	obj := named.Obj()
	if obj == nil {
		underlying := named.Underlying()
		return g.typeRef(underlying, currentNamespace)
	}
	nsName := namespaceForNamed(obj)
	ref := obj.Name()
	if nsName != currentNamespace {
		ref = nsName + "." + ref
	}
	g.namedTypes[id] = ref
	if g.inProgress[id] {
		return ref, nil
	}

	g.inProgress[id] = true
	def, err := g.renderNamedDefinition(nsName, named)
	delete(g.inProgress, id)
	if err != nil {
		return "", err
	}
	if def != "" {
		ns := g.namespace(nsName)
		if _, exists := ns.types[obj.Name()]; !exists {
			ns.types[obj.Name()] = def
			ns.typeOrder = append(ns.typeOrder, obj.Name())
		}
	}
	return ref, nil
}

func (g *tsGenerator) renderNamedDefinition(namespace string, named *types.Named) (string, error) {
	obj := named.Obj()
	if obj == nil {
		return "", nil
	}
	name := obj.Name()
	switch underlying := named.Underlying().(type) {
	case *types.Basic:
		return fmt.Sprintf("export type %s = %s", name, tsPrimitive(underlying)), nil
	case *types.Slice:
		ref, err := g.typeRef(underlying, namespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("export type %s = %s", name, ref), nil
	case *types.Array:
		ref, err := g.typeRef(underlying, namespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("export type %s = %s", name, ref), nil
	case *types.Map:
		ref, err := g.typeRef(underlying, namespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("export type %s = %s", name, ref), nil
	case *types.Pointer:
		ref, err := g.typeRef(underlying.Elem(), namespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("export type %s = %s", name, ref), nil
	case *types.Interface:
		return fmt.Sprintf("export type %s = JSONValue", name), nil
	case *types.Struct:
		var buf bytes.Buffer
		buf.WriteString(fmt.Sprintf("export interface %s {\n", name))
		wroteField := false
		for i := 0; i < underlying.NumFields(); i++ {
			field := underlying.Field(i)
			if !field.Exported() {
				continue
			}
			tag := underlying.Tag(i)
			jsonName := jsonFieldName(field.Name(), tag)
			if jsonName == "-" {
				continue
			}
			fieldType, err := g.typeRef(field.Type(), namespace)
			if err != nil {
				return "", err
			}
			buf.WriteString(fmt.Sprintf("    %s: %s\n", tsPropertyName(jsonName), fieldType))
			wroteField = true
		}
		if !wroteField {
			buf.WriteString("}\n")
			return strings.TrimSuffix(buf.String(), "\n"), nil
		}
		buf.WriteString("}")
		return buf.String(), nil
	default:
		ref, err := g.typeRef(underlying, namespace)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("export type %s = %s", name, ref), nil
	}
}

func (g *tsGenerator) inlineStructType(strct *types.Struct, currentNamespace string) (string, error) {
	var lines []string
	for i := 0; i < strct.NumFields(); i++ {
		field := strct.Field(i)
		if !field.Exported() {
			continue
		}
		tag := strct.Tag(i)
		jsonName := jsonFieldName(field.Name(), tag)
		if jsonName == "-" {
			continue
		}
		fieldType, err := g.typeRef(field.Type(), currentNamespace)
		if err != nil {
			return "", err
		}
		lines = append(lines, fmt.Sprintf("%s: %s", tsPropertyName(jsonName), fieldType))
	}
	if len(lines) == 0 {
		return "{}", nil
	}
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for _, line := range lines {
		buf.WriteString("    " + line + "\n")
	}
	buf.WriteString("}")
	return buf.String(), nil
}

func renderPathExpression(path string) string {
	var buf bytes.Buffer
	buf.WriteByte('`')
	parts := strings.SplitSeq(strings.TrimPrefix(path, "/"), "/")
	for part := range parts {
		if part == "" {
			continue
		}
		buf.WriteByte('/')
		switch part[0] {
		case ':':
			name := part[1:]
			buf.WriteString("${encodeURIComponent(String(" + name + "))}")
		case '*':
			name := part[1:]
			buf.WriteString("${encodePathWildcard(String(" + name + "))}")
		default:
			buf.WriteString(part)
		}
	}
	if buf.Len() == 1 {
		buf.WriteByte('/')
	}
	buf.WriteByte('`')
	return buf.String()
}

func preferredHTTPMethod(methods []string) string {
	if slices.Contains(methods, "GET") {
		return "GET"
	}
	if len(methods) > 0 {
		return methods[0]
	}
	return "POST"
}

func queryDefaultMethod(method string) bool {
	switch method {
	case "GET", "HEAD", "DELETE":
		return true
	default:
		return false
	}
}

func tsTypeForParamKind(kind runtimeapi.ParamKind) string {
	switch kind {
	case runtimeapi.ParamBool:
		return "boolean"
	case runtimeapi.ParamString:
		return "string"
	default:
		return "number"
	}
}

func extractRequestFields(typ types.Type, queryDefault bool) ([]requestField, bool) {
	strct, ok := derefStruct(typ)
	if !ok {
		return nil, false
	}
	fields := make([]requestField, 0, strct.NumFields())
	for i := 0; i < strct.NumFields(); i++ {
		field := strct.Field(i)
		if !field.Exported() {
			continue
		}
		tag := strct.Tag(i)
		jsonName := jsonFieldName(field.Name(), tag)
		if header, ok := lookupTag(tag, "header"); ok && header != "" {
			fields = append(fields, requestField{Name: requestFieldName(field.Name(), tag), JSONName: jsonName, Type: field.Type(), Location: requestFieldHeader, Target: header})
			continue
		}
		if query, ok := lookupTag(tag, "query"); ok && query != "" {
			fields = append(fields, requestField{Name: requestFieldName(field.Name(), tag), JSONName: jsonName, Type: field.Type(), Location: requestFieldQuery, Target: query})
			continue
		}
		if query, ok := lookupTag(tag, "qs"); ok && query != "" {
			fields = append(fields, requestField{Name: requestFieldName(field.Name(), tag), JSONName: jsonName, Type: field.Type(), Location: requestFieldQuery, Target: query})
			continue
		}
		if cookie, ok := lookupTag(tag, "cookie"); ok && cookie != "" {
			fields = append(fields, requestField{Name: requestFieldName(field.Name(), tag), JSONName: jsonName, Type: field.Type(), Location: requestFieldCookie, Target: cookie})
			continue
		}
		if jsonName == "-" {
			continue
		}
		location := requestFieldBody
		target := jsonName
		if queryDefault {
			location = requestFieldQuery
		}
		fields = append(fields, requestField{Name: requestFieldName(field.Name(), tag), JSONName: jsonName, Type: field.Type(), Location: location, Target: target})
	}
	return fields, true
}

func canPassPayloadAsJSON(fields []requestField) bool {
	for _, field := range fields {
		if field.Location != requestFieldBody {
			return false
		}
		if field.Target != field.JSONName || field.Target != jsonFieldName(field.Name, "") {
			return false
		}
	}
	return true
}

func renderCallOptions(hasQuery, hasHeaders bool) string {
	switch {
	case hasQuery && hasHeaders:
		return "mergeCallParameters(options, { query, headers })"
	case hasQuery:
		return "mergeCallParameters(options, { query })"
	case hasHeaders:
		return "mergeCallParameters(options, { headers })"
	default:
		return "options"
	}
}

func renderTypedEndpointCall(ep *model.Endpoint, methodName, pathExpr, jsonBodyExpr, optionsExpr string, hasPayload bool, withMeta bool) string {
	payloadExpr := "undefined"
	if hasPayload {
		payloadExpr = "params"
	}
	if jsonBodyExpr == "" {
		jsonBodyExpr = "undefined"
	}
	fields := []string{
		fmt.Sprintf("method: %q", methodName),
		"path: " + pathExpr,
		"payload: " + payloadExpr,
		"jsonBody: " + jsonBodyExpr,
	}
	if optionsExpr != "" {
		fields = append(fields, "params: "+optionsExpr)
	}
	callMethod := "callTypedEndpoint"
	if withMeta {
		callMethod = "callTypedEndpointWithMeta"
	}
	return "const resp = await this.baseClient." + callMethod + "({ " + strings.Join(fields, ", ") + " })"
}

func methodNameFromRendered(method string) string {
	method = strings.TrimSpace(method)
	method = strings.TrimPrefix(method, "public async ")
	name, _, _ := strings.Cut(method, "(")
	return strings.TrimSpace(name)
}

func methodNamesFromRendered(method string) []string {
	matches := regexp.MustCompile(`(?m)^\s*public async\s+([A-Za-z_$][A-Za-z0-9_$]*)\(`).FindAllStringSubmatch(method, -1)
	if len(matches) == 0 {
		if name := methodNameFromRendered(method); name != "" {
			return []string{name}
		}
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 && match[1] != "" {
			names = append(names, match[1])
		}
	}
	return names
}

func namespaceForNamed(obj *types.TypeName) string {
	if obj == nil || obj.Pkg() == nil {
		return "types"
	}
	return obj.Pkg().Name()
}

func namedTypeID(named *types.Named) string {
	if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
		return obj.Pkg().Path() + "." + obj.Name()
	}
	return named.String()
}

func specialTypeRef(named *types.Named) (string, bool) {
	obj := named.Obj()
	if obj == nil || obj.Pkg() == nil {
		return "", false
	}
	switch obj.Pkg().Path() {
	case "time":
		if obj.Name() == "Time" {
			return "string", true
		}
	case "encoding/json":
		if obj.Name() == "RawMessage" || obj.Name() == "Number" {
			return "JSONValue", true
		}
	}
	return "", false
}

func tsPrimitive(basic *types.Basic) string {
	switch basic.Kind() {
	case types.Bool:
		return "boolean"
	case types.String:
		return "string"
	case types.Int, types.Int8, types.Int16, types.Int32, types.Int64,
		types.Uint, types.Uint8, types.Uint16, types.Uint32, types.Uint64,
		types.Float32, types.Float64:
		return "number"
	default:
		return "JSONValue"
	}
}

func isByteSlice(slice *types.Slice) bool {
	basic, ok := slice.Elem().(*types.Basic)
	return ok && basic.Kind() == types.Byte
}

func derefStruct(typ types.Type) (*types.Struct, bool) {
	for {
		switch value := typ.(type) {
		case *types.Pointer:
			typ = value.Elem()
		case *types.Named:
			if special, ok := specialTypeRef(value); ok && special != "" {
				return nil, false
			}
			typ = value.Underlying()
		case *types.Alias:
			typ = types.Unalias(value)
		case *types.Struct:
			return value, true
		default:
			return nil, false
		}
	}
}

func lookupTag(tag, key string) (string, bool) {
	return reflect.StructTag(tag).Lookup(key)
}

func jsonFieldName(name, tag string) string {
	if raw, ok := lookupTag(tag, "json"); ok {
		parts := strings.Split(raw, ",")
		if len(parts) > 0 && parts[0] != "" {
			return parts[0]
		}
	}
	return name
}

func requestFieldName(name, tag string) string {
	if raw, ok := lookupTag(tag, "json"); ok {
		parts := strings.Split(raw, ",")
		if len(parts) > 0 && parts[0] != "" && parts[0] != "-" {
			return parts[0]
		}
	}
	return name
}

func rawPathParamNames(path string) []string {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	names := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" {
			continue
		}
		switch part[0] {
		case ':', '*':
			name := strings.TrimSpace(part[1:])
			if name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

func tsPropertyName(name string) string {
	if tsIdentifier.MatchString(name) {
		return name
	}
	return strconv.Quote(name)
}

func indentBlock(value string, depth int) string {
	prefix := strings.Repeat("    ", depth)
	lines := strings.Split(value, "\n")
	for i, line := range lines {
		if line == "" {
			continue
		}
		lines[i] = prefix + line
	}
	return strings.Join(lines, "\n")
}

func namespaceNamesWithMethods(namespaces map[string]*tsNamespace) []string {
	names := make([]string, 0, len(namespaces))
	for name, ns := range namespaces {
		if len(ns.methods) == 0 {
			continue
		}
		names = append(names, name)
	}
	return names
}
