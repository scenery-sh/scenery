package codegen

import (
	"bytes"
	"fmt"
	"go/format"
	"go/printer"
	"go/token"
	"go/types"
	"path/filepath"
	"slices"
	"strings"

	"pulse.dev/internal/model"
	pulseruntime "pulse.dev/runtime"
)

type Output struct {
	Rewritten map[string][]byte
	Generated map[string][]byte
}

func Generate(app *model.App) (*Output, error) {
	out := &Output{
		Rewritten: make(map[string][]byte),
		Generated: make(map[string][]byte),
	}

	rewriteEndpointDecls(app)
	for _, pkg := range app.Packages {
		for _, file := range pkg.Files {
			rel, err := filepath.Rel(app.Root, file.Path)
			if err != nil {
				return nil, err
			}
			if changed := fileChanged(pkg, file); changed {
				data, err := renderFile(pkg.GoPkg.Fset, file.AST)
				if err != nil {
					return nil, err
				}
				out.Rewritten[filepath.ToSlash(rel)] = data
			}
		}
	}

	for _, pkg := range app.Packages {
		data, err := generatePackageFile(pkg)
		if err != nil {
			return nil, err
		}
		if len(data) > 0 {
			rel := filepath.ToSlash(filepath.Join(pkg.RelDir, "pulse.gen.go"))
			if pkg.RelDir == "." {
				rel = "pulse.gen.go"
			}
			out.Generated[rel] = data
		}
	}

	mainFile, err := generateMain(app)
	if err != nil {
		return nil, err
	}
	out.Generated["pulse_internal_main/main.go"] = mainFile
	return out, nil
}

func rewriteEndpointDecls(app *model.App) {
	for _, svc := range app.Services {
		for _, ep := range svc.Endpoints {
			ep.Decl.Name.Name = ep.ImplName
		}
	}
}

func fileChanged(pkg *model.Package, file *model.File) bool {
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg && ep.File == file {
			return true
		}
	}
	return false
}

func renderFile(fset *token.FileSet, file any) ([]byte, error) {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, file); err != nil {
		return nil, err
	}
	return format.Source(buf.Bytes())
}

func generatePackageFile(pkg *model.Package) ([]byte, error) {
	var pkgEndpoints []*model.Endpoint
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg {
			pkgEndpoints = append(pkgEndpoints, ep)
		}
	}
	authHandler := pkg.Service.AuthHandler
	if authHandler != nil && authHandler.Package != pkg {
		authHandler = nil
	}
	serviceStruct := pkg.Service.Struct
	if serviceStruct != nil && serviceStruct.Package != pkg {
		serviceStruct = nil
	}
	if len(pkgEndpoints) == 0 && authHandler == nil && serviceStruct == nil {
		return nil, nil
	}

	slices.SortFunc(pkgEndpoints, func(a, b *model.Endpoint) int {
		return strings.Compare(a.Name, b.Name)
	})

	im := newImports(pkg.ImportPath)
	im.use("pulseruntime", "pulse.dev/runtime")
	if needsContextImport(pkgEndpoints, authHandler) {
		im.use("context", "context")
	}
	if serviceStruct != nil {
		im.use("sync", "sync")
	}
	if hasRaw(pkgEndpoints) {
		im.use("http", "net/http")
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "package %s\n\n", pkg.Name)

	if serviceStruct != nil {
		writeServiceStruct(&buf, im, serviceStruct)
	}
	for _, ep := range pkgEndpoints {
		writeEndpoint(&buf, im, ep, serviceStruct)
	}
	writeRegistrations(&buf, im, pkgEndpoints, authHandler, serviceStruct)
	writeImports(&buf, im)

	return format.Source([]byte(buf.String()))
}

func generateMain(app *model.App) ([]byte, error) {
	var buf strings.Builder
	buf.WriteString("package main\n\n")
	buf.WriteString("import (\n")
	buf.WriteString("\tpulseruntime \"pulse.dev/runtime\"\n")
	for _, pkg := range app.Packages {
		if hasResources(pkg) {
			fmt.Fprintf(&buf, "\t_ %q\n", pkg.ImportPath)
		}
	}
	buf.WriteString(")\n\n")
	buf.WriteString("func main() {\n")
	fmt.Fprintf(&buf, "\tif err := pulseruntime.Main(pulseruntime.AppConfig{Name: %q, ListenAddr: pulseruntime.ListenAddrFromEnv()}); err != nil {\n", app.Name)
	buf.WriteString("\t\tpanic(err)\n")
	buf.WriteString("\t}\n")
	buf.WriteString("}\n")
	return format.Source([]byte(buf.String()))
}

func hasResources(pkg *model.Package) bool {
	if pkg.Service.Struct != nil && pkg.Service.Struct.Package == pkg {
		return true
	}
	if pkg.Service.AuthHandler != nil && pkg.Service.AuthHandler.Package == pkg {
		return true
	}
	for _, ep := range pkg.Service.Endpoints {
		if ep.Package == pkg {
			return true
		}
	}
	return false
}

func hasRaw(endpoints []*model.Endpoint) bool {
	for _, ep := range endpoints {
		if ep.Raw {
			return true
		}
	}
	return false
}

func needsContextImport(endpoints []*model.Endpoint, authHandler *model.AuthHandler) bool {
	if authHandler != nil {
		return true
	}
	for _, ep := range endpoints {
		if !ep.Raw {
			return true
		}
	}
	return false
}

func writeImports(buf *strings.Builder, im *imports) {
	if len(im.entries) == 0 {
		return
	}

	// Rebuild the file with imports first.
	body := buf.String()
	buf.Reset()
	parts := strings.SplitN(body, "\n\n", 2)
	buf.WriteString(parts[0])
	buf.WriteString("\n\nimport (\n")
	for _, entry := range im.sorted() {
		if entry.alias == pathBase(entry.path) {
			fmt.Fprintf(buf, "\t%q\n", entry.path)
		} else {
			fmt.Fprintf(buf, "\t%s %q\n", entry.alias, entry.path)
		}
	}
	buf.WriteString(")\n\n")
	if len(parts) > 1 {
		buf.WriteString(parts[1])
	}
}

func writeServiceStruct(buf *strings.Builder, im *imports, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "var %s struct {\n\tonce sync.Once\n\tsvc *%s\n\terr error\n}\n\n", ss.InstanceVar, ss.TypeName)
	fmt.Fprintf(buf, "func %s() (*%s, error) {\n", ss.GetterName, ss.TypeName)
	fmt.Fprintf(buf, "\t%s.once.Do(func() {\n", ss.InstanceVar)
	if ss.InitFunc != "" {
		fmt.Fprintf(buf, "\t\t%s.svc, %s.err = %s()\n", ss.InstanceVar, ss.InstanceVar, ss.InitFunc)
	} else {
		fmt.Fprintf(buf, "\t\t%s.svc = &%s{}\n", ss.InstanceVar, ss.TypeName)
	}
	buf.WriteString("\t})\n")
	fmt.Fprintf(buf, "\treturn %s.svc, %s.err\n", ss.InstanceVar, ss.InstanceVar)
	buf.WriteString("}\n\n")
}

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
	fmt.Fprintf(buf, "func pulseInternalCall%s(%s)%s {\n", ep.Name, renderParams(im, ep.Params), renderResults(im, ep.Results))

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
		fmt.Fprintf(buf, "\t_, err := pulseruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
		buf.WriteString("\tif err != nil {\n\t\treturn err\n\t}\n")
		buf.WriteString("\treturn nil\n")
		buf.WriteString("}\n\n")
		return
	}
	fmt.Fprintf(buf, "\tresp, err := pulseruntime.CallEndpoint(%s, %q, %q, %s, %s)\n", ctxName, ep.Service.Name, ep.Name, pathArgs, payload)
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
	call := fmt.Sprintf("pulseInternalCall%s(%s)", ep.Name, joinParamNames(ep.Params))
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
	fmt.Fprintf(buf, "\treturn pulseInternalCall%s(%s)\n", ep.Name, joinParamNames(ep.Params))
	buf.WriteString("}\n\n")
}

func writeRegistrations(buf *strings.Builder, im *imports, endpoints []*model.Endpoint, authHandler *model.AuthHandler, ss *model.ServiceStruct) {
	buf.WriteString("func init() {\n")
	for _, ep := range endpoints {
		writeEndpointRegistration(buf, im, ep, ss)
	}
	if authHandler != nil {
		writeAuthRegistration(buf, im, authHandler, ss)
	}
	buf.WriteString("}\n")
}

func writeEndpointRegistration(buf *strings.Builder, im *imports, ep *model.Endpoint, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tpulseruntime.RegisterEndpoint(&pulseruntime.Endpoint{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ep.Service.Name)
	fmt.Fprintf(buf, "\t\tName: %q,\n", ep.Name)
	fmt.Fprintf(buf, "\t\tAccess: pulseruntime.%s,\n", exportAccess(ep.Access))
	fmt.Fprintf(buf, "\t\tRaw: %t,\n", ep.Raw)
	fmt.Fprintf(buf, "\t\tPath: %q,\n", ep.Path)
	fmt.Fprintf(buf, "\t\tMethods: %s,\n", renderMethodLiteral(ep.Methods))
	fmt.Fprintf(buf, "\t\tPathParams: %s,\n", renderParamSpecs(ep.PathParams))
	if ep.Payload != nil {
		fmt.Fprintf(buf, "\t\tPayloadType: pulseruntime.TypeOf[%s](),\n", im.typeExpr(ep.Payload.Type))
	} else {
		buf.WriteString("\t\tPayloadType: nil,\n")
	}
	if ep.Response != nil {
		fmt.Fprintf(buf, "\t\tResponseType: pulseruntime.TypeOf[%s](),\n", im.typeExpr(ep.Response.Type))
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

func writeAuthRegistration(buf *strings.Builder, im *imports, ah *model.AuthHandler, ss *model.ServiceStruct) {
	fmt.Fprintf(buf, "\tpulseruntime.RegisterAuthHandler(&pulseruntime.AuthHandler{\n")
	fmt.Fprintf(buf, "\t\tService: %q,\n", ah.Service.Name)
	fmt.Fprintf(buf, "\t\tParamType: pulseruntime.TypeOf[%s](),\n", im.typeExpr(ah.Param.Type))
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\tAuthDataType: pulseruntime.TypeOf[%s](),\n", im.typeExpr(ah.AuthData.Type))
	} else {
		buf.WriteString("\t\tAuthDataType: nil,\n")
	}
	buf.WriteString("\t\tAuthenticate: func(ctx context.Context, param any) (pulseruntime.AuthInfo, error) {\n")
	callTarget := ah.Name
	if ah.Receiver != nil && ss != nil {
		fmt.Fprintf(buf, "\t\t\tservice, err := %s()\n", ss.GetterName)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn pulseruntime.AuthInfo{}, err\n\t\t\t}\n")
		callTarget = "service." + ah.Name
	}
	argExpr := "param.(" + im.typeExpr(ah.Param.Type) + ")"
	if ah.AuthData != nil {
		fmt.Fprintf(buf, "\t\t\tuid, data, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn pulseruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn pulseruntime.AuthInfo{UID: string(uid), Data: data}, nil\n")
	} else {
		fmt.Fprintf(buf, "\t\t\tuid, err := %s(ctx, %s)\n", callTarget, argExpr)
		buf.WriteString("\t\t\tif err != nil {\n\t\t\t\treturn pulseruntime.AuthInfo{}, err\n\t\t\t}\n")
		buf.WriteString("\t\t\treturn pulseruntime.AuthInfo{UID: string(uid)}, nil\n")
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
		return fmt.Sprintf("pulseArg%d", index)
	}
	return field.Name
}

func exportAccess(access pulseruntime.Access) string {
	switch access {
	case pulseruntime.Public:
		return "Public"
	case pulseruntime.Auth:
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

func renderParamSpecs(params []model.Param) string {
	if len(params) == 0 {
		return "nil"
	}
	parts := make([]string, 0, len(params))
	for _, param := range params {
		parts = append(parts, fmt.Sprintf("pulseruntime.ParamSpec{Name: %q, Kind: pulseruntime.%s}", param.Name, exportParamKind(param.Kind)))
	}
	return "[]pulseruntime.ParamSpec{" + strings.Join(parts, ", ") + "}"
}

func exportParamKind(kind pulseruntime.ParamKind) string {
	switch kind {
	case pulseruntime.ParamString:
		return "ParamString"
	case pulseruntime.ParamBool:
		return "ParamBool"
	case pulseruntime.ParamInt:
		return "ParamInt"
	case pulseruntime.ParamInt8:
		return "ParamInt8"
	case pulseruntime.ParamInt16:
		return "ParamInt16"
	case pulseruntime.ParamInt32:
		return "ParamInt32"
	case pulseruntime.ParamInt64:
		return "ParamInt64"
	case pulseruntime.ParamUint:
		return "ParamUint"
	case pulseruntime.ParamUint8:
		return "ParamUint8"
	case pulseruntime.ParamUint16:
		return "ParamUint16"
	case pulseruntime.ParamUint32:
		return "ParamUint32"
	case pulseruntime.ParamUint64:
		return "ParamUint64"
	default:
		return "ParamString"
	}
}

type imports struct {
	current string
	entries map[string]string
	aliases map[string]string
}

type importEntry struct {
	alias string
	path  string
}

func newImports(current string) *imports {
	return &imports{
		current: current,
		entries: make(map[string]string),
		aliases: make(map[string]string),
	}
}

func (im *imports) use(alias, path string) string {
	if existing, ok := im.entries[alias]; ok && existing == path {
		return alias
	}
	if existing, ok := im.aliases[path]; ok {
		return existing
	}
	base := alias
	if base == "" {
		base = pathBase(path)
	}
	final := base
	for i := 2; ; i++ {
		if existing, ok := im.entries[final]; !ok || existing == path {
			break
		}
		final = fmt.Sprintf("%s%d", base, i)
	}
	im.entries[final] = path
	im.aliases[path] = final
	return final
}

func (im *imports) typeExpr(t types.Type) string {
	return types.TypeString(t, func(pkg *types.Package) string {
		if pkg == nil || pkg.Path() == im.current {
			return ""
		}
		return im.use(pkg.Name(), pkg.Path())
	})
}

func (im *imports) sorted() []importEntry {
	items := make([]importEntry, 0, len(im.entries))
	for alias, path := range im.entries {
		items = append(items, importEntry{alias: alias, path: path})
	}
	slices.SortFunc(items, func(a, b importEntry) int {
		return strings.Compare(a.path, b.path)
	})
	return items
}

func pathBase(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx >= 0 {
		return path[idx+1:]
	}
	return path
}
