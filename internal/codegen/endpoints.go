package codegen

import (
	"fmt"
	"go/ast"
	"go/token"
	"strings"

	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
)

func writeEndpoint(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	if !ep.Raw {
		writeInternalHelper(buf, im, ep)
	}
	writePackageWrapper(buf, im, ep, ss)
	if ep.Receiver != nil {
		writeMethodWrapper(buf, im, ep)
	}
}

func writeInternalHelper(buf *strings.Builder, im *imports, ep *model.Endpoint) {
	fmt.Fprintf(buf, "func sceneryInternalCall%s(%s)%s {\n", ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))

	ctxName := generatedFieldName(ep.Params[0], 0)
	pathArgs := "nil"
	if len(ep.PathParams) > 0 {
		var args []string
		for _, path := range ep.PathParams {
			args = append(args, path.Name)
		}
		pathArgs = "[]any{" + strings.Join(args, ", ") + "}"
	}
	payload := "nil"
	if ep.Payload != nil {
		payload = generatedFieldName(*ep.Payload, len(ep.Params)-1)
	}
	if ep.Response == nil {
		fmt.Fprintf(buf, "\t_, err := sceneryruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
		buf.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		buf.WriteString("\treturn nil\n")
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "\tresp, err := sceneryruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
	respType := im.typeExpr(ep.Response.Type)
	fmt.Fprintf(buf, "\tif err != nil {\n\t\tvar zero %s\n\t\treturn zero, err\n\t}\n", respType)
	fmt.Fprintf(buf, "\tif resp == nil {\n\t\tvar zero %s\n\t\treturn zero, nil\n\t}\n", respType)
	fmt.Fprintf(buf, "\treturn resp.(%s), nil\n", respType)
	buf.WriteString("}\n\n")
}

func writePackageWrapper(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "func %s(%s)%s {\n", ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))
	if ep.Raw {
		if ep.Receiver != nil && ss != nil {
			fmt.Fprintf(buf, "\tsvc, err := %s()\n", ss.GetterName)
			buf.WriteString("\tif err != nil {\n\t\tpanic(err)\n\t}\n")
			fmt.Fprintf(buf, "\tsvc.%s(%s)\n", ep.ImplName, joinParamNames(ep.Params))
		} else {
			fmt.Fprintf(buf, "\t%s(%s)\n", ep.ImplName, joinParamNames(ep.Params))
		}
		buf.WriteString("}\n\n")
		return
	}
	call := fmt.Sprintf("sceneryInternalCall%s(%s)", ep.Name, joinParamNames(ep.Params))
	if ep.Response == nil {
		fmt.Fprintf(buf, "\treturn %s\n", call)
	} else {
		fmt.Fprintf(buf, "\treturn %s\n", call)
	}
	buf.WriteString("}\n\n")
}

func writeMethodWrapper(buf *strings.Builder, im *imports, ep *model.Endpoint) {
	fmt.Fprintf(buf, "func (%s %s) %s(%s)%s {\n", ep.Receiver.Name, ep.Receiver.TypeExpr, ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))
	if ep.Raw {
		fmt.Fprintf(buf, "\t%s.%s(%s)\n", ep.Receiver.Name, ep.ImplName, joinParamNames(ep.Params))
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "\treturn sceneryInternalCall%s(%s)\n", ep.Name, joinParamNames(ep.Params))
	buf.WriteString("}\n\n")
}

func writeRegistrations(buf *strings.Builder, im *imports, endpoints []*model.Endpoint, generatedModelEndpoints []*model.GeneratedModelEndpoint, middlewares []*model.Middleware, authHandler *model.AuthHandler, ss *model.ServiceStruct, hasSecrets bool) {
	buf.WriteString("func init() {\n")
	if hasSecrets {
		buf.WriteString("\tsceneryruntime.MustPopulateSecrets(&secrets)\n")
	}
	if ss != nil {
		fmt.Fprintf(buf, "\tsceneryruntime.RegisterServiceInitializer(%q, func() error {\n", ss.Service.Name)
		fmt.Fprintf(buf, "\t\t_, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\treturn err\n")
		buf.WriteString("\t})\n")
	}
	for _, mw := range middlewares {
		writeMiddlewareRegistration(buf, im, mw, ss)
	}
	for _, ep := range endpoints {
		fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpointFunc(%s, %q, %q)\n", ep.Name, ep.Service.Name, ep.Name)
		writeEndpointRegistration(buf, im, ep, ss)
	}
	for _, ep := range generatedModelEndpoints {
		writeGeneratedModelEndpointRegistration(buf, ep)
	}
	if authHandler != nil {
		writeAuthRegistration(buf, im, authHandler, ss)
	}
	buf.WriteString("}\n")
}

func writeEndpointRegistration(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ep.Service.Name)
	fmt.Fprintf(buf, "\t\tName: %q,\n", ep.Name)
	fmt.Fprintf(buf, "\t\tAccess: sceneryruntime.%s,\n", exportAccess(ep.Access))
	fmt.Fprintf(buf, "\t\tRaw: %t,\n", ep.Raw)
	fmt.Fprintf(buf, "\t\tPath: %q,\n", ep.Path)
	fmt.Fprintf(buf, "\t\tMethods: %s,\n", renderMethodLiteral(ep.Methods))
	if len(ep.Middleware) > 0 {
		fmt.Fprintf(buf, "\t\tMiddlewareIDs: %s,\n", renderMiddlewareIDs(ep.Middleware))
	}
	fmt.Fprintf(buf, "\t\tPathParams: %s,\n", renderParamSpecs(ep.PathParams))
	if ep.Payload != nil {
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ep.Payload.Type))
	} else {
		buf.WriteString("\t\tPayloadType: nil,\n")
	}
	if ep.Response != nil {
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ep.Response.Type))
	} else {
		buf.WriteString("\t\tResponseType: nil,\n")
	}
	if ep.Raw {
		fmt.Fprintf(buf, "\t\tRawHandler: func(w http.ResponseWriter, req *http.Request) {\n")
		if ep.Receiver != nil && ss != nil {
			fmt.Fprintf(buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
			buf.WriteString("\t\t\tif err != nil {\n\t\t\t\tpanic(err)\n\t\t\t}\n")
			fmt.Fprintf(buf, "\t\t\tsvc.%s(w, req)\n", ep.ImplName)
		} else {
			fmt.Fprintf(buf, "\t\t\t%s(w, req)\n", ep.ImplName)
		}
		buf.WriteString("\t\t},\n")
	} else {
		fmt.Fprintf(buf, "\t\tInvoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {\n")
		call := renderInvokeCall(im, ep, ss)
		buf.WriteString(call)
		buf.WriteString("\t\t},\n")
	}
	buf.WriteString("\t})\n")
}

func writeGeneratedModelEndpointRegistration(buf *strings.Builder, ep *model.GeneratedModelEndpoint) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterEndpoint(&sceneryruntime.Endpoint{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ep.Service.Name)
	fmt.Fprintf(buf, "\t\tName: %q,\n", ep.Name)
	fmt.Fprintf(buf, "\t\tAccess: sceneryruntime.%s,\n", exportAccess(ep.Access))
	buf.WriteString("\t\tRaw: false,\n")
	fmt.Fprintf(buf, "\t\tPath: %q,\n", ep.Path)
	fmt.Fprintf(buf, "\t\tMethods: %s,\n", renderMethodLiteral(ep.Methods))
	fmt.Fprintf(buf, "\t\tPathParams: %s,\n", renderParamSpecs(ep.PathParams))
	switch ep.Action {
	case model.EntityCRUDList:
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", generatedModelListQueryType(ep.Entity))
	case model.EntityCRUDCreate:
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", generatedModelCreateType(ep.Entity))
	case model.EntityCRUDUpdate:
		fmt.Fprintf(buf, "\t\tPayloadType: sceneryruntime.TypeOf[%s](),\n", generatedModelPatchType(ep.Entity))
	default:
		buf.WriteString("\t\tPayloadType: nil,\n")
	}
	switch ep.Action {
	case model.EntityCRUDDelete:
		buf.WriteString("\t\tResponseType: nil,\n")
	case model.EntityCRUDList:
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[[]%s](),\n", ep.Entity.Name)
	default:
		fmt.Fprintf(buf, "\t\tResponseType: sceneryruntime.TypeOf[%s](),\n", ep.Entity.Name)
	}
	buf.WriteString("\t\tInvoke: func(ctx context.Context, pathArgs []any, payload any) (any, error) {\n")
	switch ep.Action {
	case model.EntityCRUDList:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelList%s(ctx, payload.(%s))\n", ep.Entity.Name, generatedModelListQueryType(ep.Entity))
	case model.EntityCRUDGet:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelGet%s(ctx, pathArgs[0])\n", ep.Entity.Name)
	case model.EntityCRUDCreate:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelCreate%s(ctx, payload.(%s))\n", ep.Entity.Name, generatedModelCreateType(ep.Entity))
	case model.EntityCRUDUpdate:
		fmt.Fprintf(buf, "\t\t\treturn sceneryModelUpdate%s(ctx, pathArgs[0], payload.(%s))\n", ep.Entity.Name, generatedModelPatchType(ep.Entity))
	case model.EntityCRUDDelete:
		fmt.Fprintf(buf, "\t\t\tif err := sceneryModelDelete%s(ctx, pathArgs[0]); err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n\t\t\treturn nil, nil\n", ep.Entity.Name)
	}
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func writeMiddlewareRegistration(buf *strings.Builder, im *imports, mw *model.Middleware, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterMiddleware(&sceneryruntime.Middleware{\n")
	fmt.Fprintf(buf, "\t\tID: %q,\n", middlewareID(mw))
	buf.WriteString("\t\tInvoke: func(req scenerymiddleware.Request, next scenerymiddleware.Next) scenerymiddleware.Response {\n")
	callTarget := mw.Name
	if mw.Receiver != nil && ss != nil {
		fmt.Fprintf(buf, "\t\t\tservice, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn scenerymiddleware.Response{Err: err}\n\t\t\t}\n")
		callTarget = "service." + mw.Name
	}
	fmt.Fprintf(buf, "\t\t\treturn %s(req, next)\n", callTarget)
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func hasSecretsVar(pkg *model.Package) bool {
	for _, file := range pkg.Files {
		for _, decl := range file.AST.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok || gen.Tok != token.VAR {
				continue
			}
			for _, spec := range gen.Specs {
				value, ok := spec.(*ast.ValueSpec)
				if !ok || len(value.Names) != 1 || value.Names[0].Name != "secrets" {
					continue
				}
				if _, ok := value.Type.(*ast.StructType); ok {
					return true
				}
				if len(value.Values) != 1 {
					continue
				}
				lit, ok := value.Values[0].(*ast.CompositeLit)
				if !ok {
					continue
				}
				if _, ok := lit.Type.(*ast.StructType); ok {
					return true
				}
			}
		}
	}
	return false
}

func writeAuthRegistration(buf *strings.Builder, im *imports, ah *model.AuthHandler, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tsceneryruntime.RegisterAuthHandler(&sceneryruntime.AuthHandler{\n")
	fmt.Fprintf(buf, "\t\tName: %q,\n", ah.Name)
	fmt.Fprintf(buf, "\t\tService: %q,\n", ah.Service.Name)
	fmt.Fprintf(buf, "\t\tParamType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ah.Param.Type))
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\tAuthDataType: sceneryruntime.TypeOf[%s](),\n", im.typeExpr(ah.AuthData.Type))
	} else {
		buf.WriteString("\t\tAuthDataType: nil,\n")
	}
	buf.WriteString("\t\tAuthenticate: func(ctx context.Context, param any) (sceneryruntime.AuthInfo, error) {\n")
	callTarget := ah.Name
	if ah.Receiver != nil && ss != nil {
		fmt.Fprintf(buf, "\t\t\tservice, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		callTarget = "service." + ah.Name
	}
	argExpr := "param.(" + im.typeExpr(ah.Param.Type) + ")"
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\t\tuid, data, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn sceneryruntime.AuthInfo{UID: string(uid), Data: data}, nil\n")
	} else {
		fmt.Fprintf(buf, "\t\t\tuid, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn sceneryruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn sceneryruntime.AuthInfo{UID: string(uid)}, nil\n")
	}
	buf.WriteString("\t\t},\n")
	buf.WriteString("\t})\n")
}

func renderInvokeCall(im *imports, ep *model.Endpoint, ss *model.ServiceStruct) string {
	var buf strings.Builder
	target := ep.ImplName
	if ep.Receiver != nil && ss != nil {
		fmt.Fprintf(&buf, "\t\t\tsvc, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		target = "svc." + ep.ImplName
	}

	args := []string{"ctx"}
	for i, path := range ep.PathParams {
		_ = path
		field := ep.Params[i+1]
		args = append(args, fmt.Sprintf("pathArgs[%d].(%s)", i, im.typeExpr(field.Type)))
	}
	if ep.Payload != nil {
		args = append(args, fmt.Sprintf("payload.(%s)", im.typeExpr(ep.Payload.Type)))
	}

	if ep.Response != nil {
		fmt.Fprintf(&buf, "\t\t\tresp, err := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn nil, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn resp, nil\n")
	} else {
		fmt.Fprintf(&buf, "\t\t\tcallErr := %s(%s)\n", target, strings.Join(args, ", "))
		buf.WriteString("\t\t\tif callErr != nil {\n\t\t\t\treturn nil, callErr\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn nil, nil\n")
	}
	return buf.String()
}

func renderParams(im *imports, fields []model.Field) string {
	parts := make([]string, 0, len(fields))
	for i, field := range fields {
		parts = append(parts, generatedFieldName(field, i)+" "+im.typeExpr(field.Type))
	}
	return strings.Join(parts, ", ")
}

func renderResults(im *imports, fields []model.Field) string {
	if len(fields) == 0 {
		return ""
	}
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		parts = append(parts, im.typeExpr(field.Type))
	}
	if len(parts) == 1 {
		return " " + parts[0]
	}
	return " (" + strings.Join(parts, ", ") + ")"
}

func joinParamNames(fields []model.Field) string {
	names := make([]string, 0, len(fields))
	for i, field := range fields {
		names = append(names, generatedFieldName(field, i))
	}
	return strings.Join(names, ", ")
}

func generatedFieldName(field model.Field, index int) string {
	if field.Name == "" || field.Name == "_" {
		return fmt.Sprintf("sceneryArg%d", index)
	}
	return field.Name
}

func exportAccess(access runtimeapi.Access) string {
	switch access {
	case runtimeapi.Public:
		return "Public"
	case runtimeapi.Auth:
		return "Auth"
	default:
		return "Private"
	}
}

func renderMethodLiteral(methods []string) string {
	if len(methods) == 0 {
		return "nil"
	}
	quoted := make([]string, 0, len(methods))
	for _, method := range methods {
		quoted = append(quoted, fmt.Sprintf("%q", method))
	}
	return "[]string{" + strings.Join(quoted, ", ") + "}"
}

func renderMiddlewareIDs(middlewares []*model.Middleware) string {
	if len(middlewares) == 0 {
		return "nil"
	}
	ids := make([]string, 0, len(middlewares))
	for _, mw := range middlewares {
		ids = append(ids, fmt.Sprintf("%q", middlewareID(mw)))
	}
	return "[]string{" + strings.Join(ids, ", ") + "}"
}

func middlewareID(mw *model.Middleware) string {
	return mw.Package.ImportPath + "." + mw.Name
}

func renderParamSpecs(params []model.Param) string {
	if len(params) == 0 {
		return "nil"
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, fmt.Sprintf("sceneryruntime.ParamSpec{Name: %q, Kind: sceneryruntime.%s}", param.Name, exportParamKind(param.Kind)))
	}
	return "[]sceneryruntime.ParamSpec{" + strings.Join(parts, ", ") + "}"
}

func exportParamKind(kind runtimeapi.ParamKind) string {
	switch kind {
	case runtimeapi.ParamString:
		return "ParamString"
	case runtimeapi.ParamBool:
		return "ParamBool"
	case runtimeapi.ParamInt:
		return "ParamInt"
	case runtimeapi.ParamInt8:
		return "ParamInt8"
	case runtimeapi.ParamInt16:
		return "ParamInt16"
	case runtimeapi.ParamInt32:
		return "ParamInt32"
	case runtimeapi.ParamInt64:
		return "ParamInt64"
	case runtimeapi.ParamUint:
		return "ParamUint"
	case runtimeapi.ParamUint8:
		return "ParamUint8"
	case runtimeapi.ParamUint16:
		return "ParamUint16"
	case runtimeapi.ParamUint32:
		return "ParamUint32"
	case runtimeapi.ParamUint64:
		return "ParamUint64"
	default:
		return "ParamString"
	}
}
