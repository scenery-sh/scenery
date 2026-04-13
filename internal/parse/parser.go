package parse

import (
	"errors"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"

	"pulse.dev/auth"
	"pulse.dev/internal/model"
	pulseruntime "pulse.dev/runtime"
)

type directive struct {
	name    string
	options map[string]bool
	fields  map[string]string
}

func App(root, name string) (*model.App, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName |
			packages.NeedFiles |
			packages.NeedCompiledGoFiles |
			packages.NeedSyntax |
			packages.NeedTypes |
			packages.NeedTypesInfo |
			packages.NeedModule,
		Dir: root,
	}

	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil, err
	}

	app := &model.App{Name: name, Root: root}
	if len(pkgs) > 0 && pkgs[0].Module != nil {
		app.ModulePath = pkgs[0].Module.Path
	}

	var errs []string
	var foundDirective bool
	byRelDir := make(map[string]*model.Package)
	serviceByRoot := make(map[string]*model.Service)
	serviceNames := make(map[string]*model.Service)

	for _, pkg := range pkgs {
		if len(pkg.GoFiles) == 0 {
			continue
		}
		absDir := filepath.Dir(pkg.GoFiles[0])
		relDir, err := filepath.Rel(root, absDir)
		if err != nil {
			errs = append(errs, err.Error())
			continue
		}
		if relDir == "." {
			relDir = "."
		}
		mpkg := &model.Package{
			GoPkg:      pkg,
			ImportPath: pkg.PkgPath,
			Name:       pkg.Name,
			AbsDir:     absDir,
			RelDir:     relDir,
		}
		for i, file := range pkg.Syntax {
			mpkg.Files = append(mpkg.Files, &model.File{Path: pkg.GoFiles[i], AST: file})
		}
		app.Packages = append(app.Packages, mpkg)
		byRelDir[relDir] = mpkg
	}

	slices.SortFunc(app.Packages, func(a, b *model.Package) int {
		return strings.Compare(a.RelDir, b.RelDir)
	})

	var rawEndpoints []*model.Endpoint
	var authHandlers []*model.AuthHandler

	for _, pkg := range app.Packages {
		serviceRoot := serviceRootForDir(pkg.RelDir)
		serviceName := discoverServiceName(pkg, serviceRoot, byRelDir)
		svc := serviceByRoot[serviceRoot]
		if svc == nil {
			if other := serviceNames[serviceName]; other != nil && other.RootRelDir != serviceRoot {
				errs = append(errs, fmt.Sprintf("two services were found with the same name %q", serviceName))
				continue
			}
			svc = &model.Service{
				Name:       serviceName,
				RootRelDir: serviceRoot,
				RootAbsDir: filepath.Join(root, serviceRoot),
			}
			if serviceRoot == "." {
				svc.RootAbsDir = root
			}
			serviceByRoot[serviceRoot] = svc
			serviceNames[serviceName] = svc
			app.Services = append(app.Services, svc)
		}
		pkg.Service = svc
		svc.Packages = append(svc.Packages, pkg)
		if pkg.RelDir == serviceRoot {
			svc.RootPackage = pkg
		}
	}

	for _, pkg := range app.Packages {
		for _, file := range pkg.Files {
			for _, decl := range file.AST.Decls {
				switch node := decl.(type) {
				case *ast.GenDecl:
					dir := parseDirective(node.Doc)
					if dir == nil || dir.name != "service" {
						continue
					}
					foundDirective = true
					ss, err := parseServiceStruct(pkg, file, node)
					if err != nil {
						errs = append(errs, err.Error())
						continue
					}
					if pkg.RelDir != pkg.Service.RootRelDir {
						errs = append(errs, fmt.Sprintf("service struct %s cannot be declared in nested package %s", ss.TypeName, pkg.RelDir))
						continue
					}
					if pkg.Service.Struct != nil {
						errs = append(errs, fmt.Sprintf("duplicate pulse:service directive in service %s", pkg.Service.Name))
						continue
					}
					ss.Service = pkg.Service
					pkg.Service.Struct = ss
				case *ast.FuncDecl:
					dir := parseDirective(node.Doc)
					if dir == nil {
						continue
					}
					foundDirective = true
					switch dir.name {
					case "api":
						ep, err := parseEndpoint(pkg, file, node, dir)
						if err != nil {
							errs = append(errs, err.Error())
							continue
						}
						ep.Service = pkg.Service
						if !ep.PathExplicit {
							ep.Path = "/" + ep.Service.Name + "." + ep.Name
						}
						pkg.Service.Endpoints = append(pkg.Service.Endpoints, ep)
						if ep.Raw {
							rawEndpoints = append(rawEndpoints, ep)
						}
					case "authhandler":
						ah, err := parseAuthHandler(pkg, file, node)
						if err != nil {
							errs = append(errs, err.Error())
							continue
						}
						ah.Service = pkg.Service
						authHandlers = append(authHandlers, ah)
					}
				}
			}
		}
	}

	if len(authHandlers) > 1 {
		errs = append(errs, "only one pulse:authhandler is supported per application")
	}
	if !foundDirective {
		errs = append(errs, "no Pulse or Encore directives found in application")
	}
	if len(authHandlers) == 1 {
		authHandlers[0].Service.AuthHandler = authHandlers[0]
	}

	for _, svc := range app.Services {
		if svc.Struct != nil {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil && ep.Receiver.TypeName != svc.Struct.TypeName {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s receiver %s does not match pulse:service struct %s", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Struct.TypeName))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil && svc.AuthHandler.Receiver.TypeName != svc.Struct.TypeName {
				errs = append(errs, fmt.Sprintf("auth handler %s receiver %s does not match pulse:service struct %s", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Struct.TypeName))
			}
		} else {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s uses receiver %s but service %s has no pulse:service struct", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Name))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil {
				errs = append(errs, fmt.Sprintf("auth handler %s uses receiver %s but service %s has no pulse:service struct", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Name))
			}
		}

		seenNames := make(map[string]bool)
		for _, ep := range svc.Endpoints {
			key := ep.Name
			if seenNames[key] {
				errs = append(errs, fmt.Sprintf("duplicate endpoint name %s in service %s", ep.Name, svc.Name))
			}
			seenNames[key] = true
		}
	}

	rawSet := make(map[types.Object]*model.Endpoint)
	for _, ep := range rawEndpoints {
		rawSet[ep.Object] = ep
	}
	if len(rawSet) > 0 {
		for _, pkg := range app.Packages {
			for _, file := range pkg.Files {
				ast.Inspect(file.AST, func(node ast.Node) bool {
					call, ok := node.(*ast.CallExpr)
					if !ok {
						return true
					}
					if obj := calledObject(pkg.GoPkg, call.Fun); obj != nil {
						if ep := rawSet[obj]; ep != nil {
							errs = append(errs, fmt.Sprintf("raw endpoint calls are not supported for %s.%s", ep.Service.Name, ep.Name))
						}
					}
					return true
				})
			}
		}
	}

	slices.SortFunc(app.Services, func(a, b *model.Service) int {
		return strings.Compare(a.Name, b.Name)
	})
	for _, svc := range app.Services {
		slices.SortFunc(svc.Endpoints, func(a, b *model.Endpoint) int {
			return strings.Compare(a.Name, b.Name)
		})
	}

	if len(errs) > 0 {
		return nil, errors.New(strings.Join(errs, "\n"))
	}
	return app, nil
}

func parseEndpoint(pkg *model.Package, file *model.File, fn *ast.FuncDecl, dir *directive) (*model.Endpoint, error) {
	sigObj := pkg.GoPkg.TypesInfo.Defs[fn.Name]
	if sigObj == nil {
		return nil, fmt.Errorf("unable to resolve %s", fn.Name.Name)
	}
	sig, ok := sigObj.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("%s is not a function", fn.Name.Name)
	}

	ep := &model.Endpoint{
		Package:      pkg,
		File:         file,
		Name:         fn.Name.Name,
		ImplName:     "pulseInternalImpl" + fn.Name.Name,
		Decl:         fn,
		Object:       sigObj,
		Access:       pulseruntime.Private,
		Raw:          dir.options["raw"],
		Path:         dir.fields["path"],
		PathExplicit: dir.fields["path"] != "",
		Methods:      parseMethods(dir.fields["method"]),
		TokenPos:     fn.Pos(),
	}
	if dir.options["public"] {
		ep.Access = pulseruntime.Public
	}
	if dir.options["auth"] {
		ep.Access = pulseruntime.Auth
	}

	if fn.Recv != nil {
		ep.Receiver = receiverFromFieldList(pkg.GoPkg.Fset, fn.Recv)
	}
	ep.Params = expandFields(pkg.GoPkg.Fset, fn.Type.Params, sig.Params(), "arg")
	ep.Results = expandFields(pkg.GoPkg.Fset, fn.Type.Results, sig.Results(), "ret")

	if ep.Raw {
		if len(ep.Params) != 2 {
			return nil, fmt.Errorf("raw endpoint %s must have signature func(http.ResponseWriter, *http.Request)", ep.Name)
		}
		if len(ep.Results) > 0 {
			return nil, fmt.Errorf("raw endpoint %s cannot return values", ep.Name)
		}
		if ep.Access == pulseruntime.Private {
			return nil, fmt.Errorf("raw endpoint %s cannot be private", ep.Name)
		}
		if len(ep.Methods) == 0 {
			ep.Methods = []string{"*"}
		}
		return ep, nil
	}

	if len(ep.Params) == 0 {
		return nil, fmt.Errorf("endpoint %s must accept context.Context", ep.Name)
	}
	if !isNamedType(sig.Params().At(0).Type(), "context", "Context") {
		return nil, fmt.Errorf("endpoint %s first parameter must be context.Context", ep.Name)
	}
	if len(ep.Results) == 0 || len(ep.Results) > 2 || !isErrorType(sig.Results().At(sig.Results().Len()-1).Type()) {
		return nil, fmt.Errorf("endpoint %s must return error or (resp, error)", ep.Name)
	}
	if len(ep.Results) == 2 {
		ep.Response = &ep.Results[0]
	}

	pathParams, err := parsePath(ep.Path)
	if err != nil {
		return nil, fmt.Errorf("endpoint %s: %w", ep.Name, err)
	}
	afterContext := ep.Params[1:]
	if len(afterContext) < len(pathParams) || len(afterContext) > len(pathParams)+1 {
		return nil, fmt.Errorf("endpoint %s has wrong number of parameters for path %s", ep.Name, ep.Path)
	}

	for i, spec := range pathParams {
		field := afterContext[i]
		if field.Name != spec.Name {
			return nil, fmt.Errorf("endpoint %s path param %s must match function param %s", ep.Name, spec.Name, field.Name)
		}
		kind, ok := paramKind(field.Type)
		if !ok {
			return nil, fmt.Errorf("endpoint %s path param %s has unsupported type %s", ep.Name, field.Name, field.TypeExpr)
		}
		ep.PathParams = append(ep.PathParams, model.Param{Name: field.Name, Kind: kind})
	}
	if len(afterContext) == len(pathParams)+1 {
		payload := afterContext[len(afterContext)-1]
		ep.Payload = &payload
	}
	if len(ep.Methods) == 0 {
		if ep.Payload == nil {
			ep.Methods = []string{"GET", "POST"}
		} else {
			ep.Methods = []string{"POST"}
		}
	}
	return ep, nil
}

func parseAuthHandler(pkg *model.Package, file *model.File, fn *ast.FuncDecl) (*model.AuthHandler, error) {
	sigObj := pkg.GoPkg.TypesInfo.Defs[fn.Name]
	if sigObj == nil {
		return nil, fmt.Errorf("unable to resolve auth handler %s", fn.Name.Name)
	}
	sig, ok := sigObj.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("%s is not a function", fn.Name.Name)
	}
	params := expandFields(pkg.GoPkg.Fset, fn.Type.Params, sig.Params(), "arg")
	results := expandFields(pkg.GoPkg.Fset, fn.Type.Results, sig.Results(), "ret")
	if len(params) != 2 || !isNamedType(sig.Params().At(0).Type(), "context", "Context") {
		return nil, fmt.Errorf("auth handler %s must have signature func(context.Context, ...)", fn.Name.Name)
	}
	if len(results) < 2 || len(results) > 3 || !isErrorType(sig.Results().At(sig.Results().Len()-1).Type()) {
		return nil, fmt.Errorf("auth handler %s must return (auth.UID, error) or (auth.UID, data, error)", fn.Name.Name)
	}
	if !isAuthUIDType(sig.Results().At(0).Type()) {
		return nil, fmt.Errorf("auth handler %s first return must be auth.UID", fn.Name.Name)
	}
	if !isStringOrStruct(sig.Params().At(1).Type()) {
		return nil, fmt.Errorf("auth handler %s parameter must be string or struct", fn.Name.Name)
	}

	ah := &model.AuthHandler{
		Package:  pkg,
		File:     file,
		Name:     fn.Name.Name,
		Decl:     fn,
		Object:   sigObj,
		Param:    params[1],
		TokenPos: fn.Pos(),
	}
	if fn.Recv != nil {
		ah.Receiver = receiverFromFieldList(pkg.GoPkg.Fset, fn.Recv)
	}
	if len(results) == 3 {
		data := results[1]
		ah.AuthData = &data
	}
	return ah, nil
}

func parseServiceStruct(pkg *model.Package, file *model.File, decl *ast.GenDecl) (*model.ServiceStruct, error) {
	if len(decl.Specs) != 1 {
		return nil, fmt.Errorf("pulse:service must be declared on a single struct type")
	}
	spec, ok := decl.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, fmt.Errorf("pulse:service must annotate a type declaration")
	}
	if _, ok := spec.Type.(*ast.StructType); !ok {
		return nil, fmt.Errorf("pulse:service must annotate a struct type")
	}
	typeName := spec.Name.Name
	ss := &model.ServiceStruct{
		Package:  pkg,
		File:     file,
		TypeName: typeName,
		TypeExpr: typeName,
		Receiver: model.Receiver{
			Name:     "s",
			TypeName: typeName,
			TypeExpr: "*" + typeName,
			Pointer:  true,
		},
		Decl:        decl,
		TypeSpec:    spec,
		GetterName:  "pulseInternalGet" + typeName,
		InstanceVar: "pulseInternalService" + typeName,
	}
	if initObj := pkg.GoPkg.Types.Scope().Lookup("init" + typeName); initObj != nil {
		if sig, ok := initObj.Type().(*types.Signature); ok && sig.Params().Len() == 0 && sig.Results().Len() == 2 {
			if ptr, ok := sig.Results().At(0).Type().(*types.Pointer); ok && isNamedType(ptr, pkg.ImportPath, typeName) && isErrorType(sig.Results().At(1).Type()) {
				ss.InitFunc = initObj.Name()
			}
		}
	}
	return ss, nil
}

func parseDirective(group *ast.CommentGroup) *directive {
	if group == nil {
		return nil
	}
	for _, comment := range group.List {
		body, ok := directiveBody(comment.Text)
		if !ok {
			continue
		}
		parts := strings.Fields(body)
		if len(parts) == 0 {
			return nil
		}
		dir := &directive{
			name:    parts[0],
			options: make(map[string]bool),
			fields:  make(map[string]string),
		}
		for _, part := range parts[1:] {
			if key, value, ok := strings.Cut(part, "="); ok {
				dir.fields[key] = value
				continue
			}
			dir.options[part] = true
		}
		return dir
	}
	return nil
}

func directiveBody(comment string) (string, bool) {
	text := strings.TrimSpace(strings.TrimPrefix(comment, "//"))
	for _, prefix := range []string{"pulse:", "encore:"} {
		if strings.HasPrefix(text, prefix) {
			return strings.TrimPrefix(text, prefix), true
		}
	}
	return "", false
}

func serviceRootForDir(relDir string) string {
	if relDir == "." {
		return "."
	}
	first, _, _ := strings.Cut(relDir, string(os.PathSeparator))
	return first
}

func discoverServiceName(pkg *model.Package, root string, byRelDir map[string]*model.Package) string {
	if root == "." {
		if rootPkg := byRelDir["."]; rootPkg != nil {
			return rootPkg.Name
		}
		return pkg.Name
	}
	if rootPkg := byRelDir[root]; rootPkg != nil {
		return rootPkg.Name
	}
	return filepath.Base(root)
}

func parsePath(path string) ([]model.Param, error) {
	if path == "" {
		return nil, nil
	}
	var params []model.Param
	for _, segment := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if segment == "" {
			continue
		}
		switch segment[0] {
		case ':', '*':
			name := segment[1:]
			if name == "" {
				return nil, fmt.Errorf("invalid path segment %q", segment)
			}
			params = append(params, model.Param{Name: name})
		case '!':
			return nil, fmt.Errorf("fallback paths are not supported in phase 1")
		}
	}
	return params, nil
}

func expandFields(fset *token.FileSet, list *ast.FieldList, tuple *types.Tuple, prefix string) []model.Field {
	if list == nil || tuple == nil {
		return nil
	}
	var fields []model.Field
	index := 0
	for _, field := range list.List {
		typeExpr := renderNode(fset, field.Type)
		if len(field.Names) == 0 {
			name := fmt.Sprintf("%s%d", prefix, index)
			fields = append(fields, model.Field{Name: name, TypeExpr: typeExpr, Type: tuple.At(index).Type()})
			index++
			continue
		}
		for _, name := range field.Names {
			fields = append(fields, model.Field{Name: name.Name, TypeExpr: typeExpr, Type: tuple.At(index).Type()})
			index++
		}
	}
	return fields
}

func receiverFromFieldList(fset *token.FileSet, list *ast.FieldList) *model.Receiver {
	if list == nil || len(list.List) == 0 {
		return nil
	}
	field := list.List[0]
	name := "receiver"
	if len(field.Names) > 0 {
		name = field.Names[0].Name
	}
	typeExpr := renderNode(fset, field.Type)
	recv := &model.Receiver{
		Name:     name,
		TypeExpr: typeExpr,
	}
	switch expr := field.Type.(type) {
	case *ast.StarExpr:
		recv.Pointer = true
		recv.TypeName = renderNode(fset, expr.X)
	default:
		recv.TypeName = renderNode(fset, expr)
	}
	return recv
}

func renderNode(fset *token.FileSet, node any) string {
	var b strings.Builder
	_ = printer.Fprint(&b, fset, node)
	return b.String()
}

func isNamedType(t types.Type, pkgPath, name string) bool {
	named, ok := unwrapNamed(t)
	if !ok {
		return false
	}
	obj := named.Obj()
	if obj.Pkg() == nil {
		return obj.Name() == name && pkgPath == ""
	}
	return obj.Pkg().Path() == pkgPath && obj.Name() == name
}

func isErrorType(t types.Type) bool {
	return types.Identical(t, types.Universe.Lookup("error").Type())
}

func isStringOrStruct(t types.Type) bool {
	switch u := t.(type) {
	case *types.Basic:
		return u.Kind() == types.String
	case *types.Pointer:
		_, ok := unwrapNamed(u.Elem())
		return ok || isStruct(u.Elem())
	default:
		return isStruct(t)
	}
}

func unwrapNamed(t types.Type) (*types.Named, bool) {
	switch u := t.(type) {
	case *types.Named:
		return u, true
	case *types.Pointer:
		if named, ok := u.Elem().(*types.Named); ok {
			return named, true
		}
	}
	return nil, false
}

func isStruct(t types.Type) bool {
	switch u := t.(type) {
	case *types.Named:
		_, ok := u.Underlying().(*types.Struct)
		return ok
	case *types.Pointer:
		return isStruct(u.Elem())
	default:
		_, ok := t.Underlying().(*types.Struct)
		return ok
	}
}

func parseMethods(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	methods := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(strings.ToUpper(part))
		if part != "" {
			methods = append(methods, part)
		}
	}
	return methods
}

func paramKind(t types.Type) (pulseruntime.ParamKind, bool) {
	switch u := types.Unalias(t).(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.String:
			return pulseruntime.ParamString, true
		case types.Bool:
			return pulseruntime.ParamBool, true
		case types.Int:
			return pulseruntime.ParamInt, true
		case types.Int8:
			return pulseruntime.ParamInt8, true
		case types.Int16:
			return pulseruntime.ParamInt16, true
		case types.Int32:
			return pulseruntime.ParamInt32, true
		case types.Int64:
			return pulseruntime.ParamInt64, true
		case types.Uint:
			return pulseruntime.ParamUint, true
		case types.Uint8:
			return pulseruntime.ParamUint8, true
		case types.Uint16:
			return pulseruntime.ParamUint16, true
		case types.Uint32:
			return pulseruntime.ParamUint32, true
		case types.Uint64:
			return pulseruntime.ParamUint64, true
		}
	case *types.Named:
		if isAuthUIDType(u) {
			return pulseruntime.ParamString, true
		}
	}
	return "", false
}

func isAuthUIDType(t types.Type) bool {
	for _, pkgPath := range []string{"pulse.dev/auth", "pulse.dev/beta/auth", "encore.dev/beta/auth"} {
		if isNamedType(t, pkgPath, "UID") {
			return true
		}
	}
	return false
}

func calledObject(pkg *packages.Package, fun ast.Expr) types.Object {
	switch expr := fun.(type) {
	case *ast.Ident:
		return pkg.TypesInfo.Uses[expr]
	case *ast.SelectorExpr:
		if sel := pkg.TypesInfo.Selections[expr]; sel != nil {
			return sel.Obj()
		}
		return pkg.TypesInfo.Uses[expr.Sel]
	}
	return nil
}

var _ auth.UID
