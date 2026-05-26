package parse

import (
	"errors"
	"fmt"
	"go/ast"
	"go/constant"
	"go/printer"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/pbrazdil/onlava/auth"
	"github.com/pbrazdil/onlava/internal/model"
	"github.com/pbrazdil/onlava/internal/runtimeapi"
)

type directive struct {
	name    string
	options map[string]bool
	fields  map[string]string
	tags    []string
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
	explicitServiceRoots := make(map[string]bool)

	for _, pkg := range pkgs {
		paths := syntaxFilePaths(pkg)
		if len(paths) == 0 {
			continue
		}
		absDir := filepath.Dir(paths[0])
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
			if i >= len(paths) {
				errs = append(errs, fmt.Sprintf("package %s returned %d syntax files but only %d source paths", pkg.PkgPath, len(pkg.Syntax), len(paths)))
				break
			}
			mpkg.Files = append(mpkg.Files, &model.File{Path: paths[i], AST: file})
		}
		app.Packages = append(app.Packages, mpkg)
		byRelDir[relDir] = mpkg
		if packageDeclaresService(mpkg) {
			explicitServiceRoots[relDir] = true
		}
	}

	slices.SortFunc(app.Packages, func(a, b *model.Package) int {
		return strings.Compare(a.RelDir, b.RelDir)
	})
	for _, pkg := range app.Packages {
		pkg.Runtime = discoverRuntimeDeclarations(pkg)
		app.Runtime = append(app.Runtime, pkg.Runtime...)
		errs = append(errs, validateTemporalRuntimeCalls(pkg)...)
	}

	var rawEndpoints []*model.Endpoint
	var authHandlers []*model.AuthHandler
	var middlewares []*model.Middleware

	for _, pkg := range app.Packages {
		serviceRoot := serviceRootForPackage(pkg.RelDir, explicitServiceRoots)
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
					if pkg.Service.Struct != nil {
						errs = append(errs, fmt.Sprintf("duplicate onlava:service directive in service %s", pkg.Service.Name))
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
					case "middleware":
						mw, err := parseMiddleware(pkg, file, node, dir)
						if err != nil {
							errs = append(errs, err.Error())
							continue
						}
						mw.Service = pkg.Service
						middlewares = append(middlewares, mw)
						pkg.Service.Middleware = append(pkg.Service.Middleware, mw)
					}
				}
			}
		}
	}

	if len(authHandlers) > 1 {
		errs = append(errs, "only one onlava:authhandler is supported per application")
	}
	if !foundDirective {
		errs = append(errs, "no onlava directives found in application")
	}
	if len(authHandlers) == 1 {
		authHandlers[0].Service.AuthHandler = authHandlers[0]
	}
	sortMiddleware(root, middlewares)
	app.Middleware = middlewares

	for _, svc := range app.Services {
		if svc.Struct != nil {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil && ep.Receiver.TypeName != svc.Struct.TypeName {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s receiver %s does not match onlava:service struct %s", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Struct.TypeName))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil && svc.AuthHandler.Receiver.TypeName != svc.Struct.TypeName {
				errs = append(errs, fmt.Sprintf("auth handler %s receiver %s does not match onlava:service struct %s", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Struct.TypeName))
			}
			for _, mw := range svc.Middleware {
				if mw.Receiver != nil && mw.Receiver.TypeName != svc.Struct.TypeName {
					errs = append(errs, fmt.Sprintf("middleware %s receiver %s does not match onlava:service struct %s", mw.Name, mw.Receiver.TypeName, svc.Struct.TypeName))
				}
			}
		} else {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s uses receiver %s but service %s has no onlava:service struct", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Name))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil {
				errs = append(errs, fmt.Sprintf("auth handler %s uses receiver %s but service %s has no onlava:service struct", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Name))
			}
			for _, mw := range svc.Middleware {
				if mw.Receiver != nil {
					errs = append(errs, fmt.Sprintf("middleware %s uses receiver %s but service %s has no onlava:service struct", mw.Name, mw.Receiver.TypeName, svc.Name))
				}
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
	for _, mw := range middlewares {
		matched := false
		for _, ep := range candidateEndpoints(app, mw) {
			if middlewareMatchesEndpoint(mw, ep) {
				matched = true
				break
			}
		}
		if !matched {
			errs = append(errs, fmt.Sprintf("middleware %s target matches no endpoints", mw.Name))
		}
	}
	for _, svc := range app.Services {
		for _, ep := range svc.Endpoints {
			for _, mw := range middlewares {
				if middlewareAppliesToService(mw, svc) && middlewareMatchesEndpoint(mw, ep) {
					ep.Middleware = append(ep.Middleware, mw)
				}
			}
		}
	}

	app.Services = pruneEmptyServices(app.Services)

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

func syntaxFilePaths(pkg *packages.Package) []string {
	switch {
	case len(pkg.CompiledGoFiles) == len(pkg.Syntax):
		return pkg.CompiledGoFiles
	case len(pkg.GoFiles) == len(pkg.Syntax):
		return pkg.GoFiles
	case len(pkg.CompiledGoFiles) > 0:
		return pkg.CompiledGoFiles
	default:
		return pkg.GoFiles
	}
}

func pruneEmptyServices(services []*model.Service) []*model.Service {
	if len(services) == 0 {
		return nil
	}
	pruned := make([]*model.Service, 0, len(services))
	for _, svc := range services {
		if svc == nil || len(svc.Endpoints) == 0 {
			continue
		}
		pruned = append(pruned, svc)
	}
	return pruned
}

func packageDeclaresService(pkg *model.Package) bool {
	for _, file := range pkg.Files {
		for _, decl := range file.AST.Decls {
			gen, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			dir := parseDirective(gen.Doc)
			if dir != nil && dir.name == "service" {
				return true
			}
		}
	}
	return false
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
		ImplName:     "onlavaInternalImpl" + fn.Name.Name,
		Decl:         fn,
		Object:       sigObj,
		Access:       runtimeapi.Private,
		Raw:          dir.options["raw"],
		Path:         dir.fields["path"],
		PathExplicit: dir.fields["path"] != "",
		Methods:      parseMethods(dir.fields["method"]),
		Tags:         append([]string(nil), dir.tags...),
		TokenPos:     fn.Pos(),
	}
	if dir.options["public"] {
		ep.Access = runtimeapi.Public
	}
	if dir.options["auth"] {
		ep.Access = runtimeapi.Auth
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
		if ep.Access == runtimeapi.Private {
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

func parseMiddleware(pkg *model.Package, file *model.File, fn *ast.FuncDecl, dir *directive) (*model.Middleware, error) {
	sigObj := pkg.GoPkg.TypesInfo.Defs[fn.Name]
	if sigObj == nil {
		return nil, fmt.Errorf("unable to resolve middleware %s", fn.Name.Name)
	}
	sig, ok := sigObj.Type().(*types.Signature)
	if !ok {
		return nil, fmt.Errorf("%s is not a function", fn.Name.Name)
	}
	if sig.Params().Len() != 2 || sig.Results().Len() != 1 {
		return nil, fmt.Errorf("middleware %s must have signature func(middleware.Request, middleware.Next) middleware.Response", fn.Name.Name)
	}
	if !isMiddlewareNamedType(sig.Params().At(0).Type(), "Request") ||
		!isMiddlewareNamedType(sig.Params().At(1).Type(), "Next") ||
		!isMiddlewareNamedType(sig.Results().At(0).Type(), "Response") {
		return nil, fmt.Errorf("middleware %s must have signature func(middleware.Request, middleware.Next) middleware.Response", fn.Name.Name)
	}

	targets, err := parseMiddlewareTargets(dir.fields["target"])
	if err != nil {
		return nil, fmt.Errorf("middleware %s: %w", fn.Name.Name, err)
	}

	mw := &model.Middleware{
		Package:  pkg,
		File:     file,
		Name:     fn.Name.Name,
		Decl:     fn,
		Global:   dir.options["global"],
		Targets:  targets,
		TokenPos: fn.Pos(),
	}
	if fn.Recv != nil {
		mw.Receiver = receiverFromFieldList(pkg.GoPkg.Fset, fn.Recv)
	}
	return mw, nil
}

func parseServiceStruct(pkg *model.Package, file *model.File, decl *ast.GenDecl) (*model.ServiceStruct, error) {
	if len(decl.Specs) != 1 {
		return nil, fmt.Errorf("onlava:service must be declared on a single struct type")
	}
	spec, ok := decl.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, fmt.Errorf("onlava:service must annotate a type declaration")
	}
	if _, ok := spec.Type.(*ast.StructType); !ok {
		return nil, fmt.Errorf("onlava:service must annotate a struct type")
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
		GetterName:  "onlavaInternalGet" + typeName,
		InstanceVar: "onlavaInternalService" + typeName,
	}
	if initObj := pkg.GoPkg.Types.Scope().Lookup("init" + typeName); initObj != nil {
		if sig, ok := initObj.Type().(*types.Signature); ok && sig.Params().Len() == 0 && sig.Results().Len() == 2 {
			if ptr, ok := sig.Results().At(0).Type().(*types.Pointer); ok && isNamedType(ptr, pkg.ImportPath, typeName) && isErrorType(sig.Results().At(1).Type()) {
				ss.InitFunc = initObj.Name()
			}
		}
	}
	if namedObj := pkg.GoPkg.Types.Scope().Lookup(typeName); namedObj != nil {
		if named, ok := namedObj.Type().(*types.Named); ok {
			methods := types.NewMethodSet(types.NewPointer(named))
			for i := 0; i < methods.Len(); i++ {
				sel := methods.At(i)
				if sel.Obj().Name() != "Shutdown" {
					continue
				}
				sig, ok := sel.Obj().Type().(*types.Signature)
				if !ok || sig.Params().Len() != 1 || sig.Results().Len() != 0 || !isNamedType(sig.Params().At(0).Type(), "context", "Context") {
					return nil, fmt.Errorf("service %s Shutdown method must have signature func(context.Context)", typeName)
				}
				ss.Shutdown = sel.Obj().Name()
				break
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
			if value, ok := strings.CutPrefix(part, "tag:"); ok && value != "" {
				if !slices.Contains(dir.tags, value) {
					dir.tags = append(dir.tags, value)
				}
				continue
			}
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
	if strings.HasPrefix(text, "onlava:") {
		return strings.TrimPrefix(text, "onlava:"), true
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

func serviceRootForPackage(relDir string, explicitRoots map[string]bool) string {
	root := serviceRootForDir(relDir)
	for explicit := range explicitRoots {
		if explicit == "." {
			if relDir == "." {
				return "."
			}
			continue
		}
		if !sameOrDescendantDir(relDir, explicit) {
			continue
		}
		if root == "." || len(explicit) > len(root) {
			root = explicit
		}
	}
	return root
}

func sameOrDescendantDir(relDir, root string) bool {
	if relDir == root {
		return true
	}
	if root == "." {
		return false
	}
	prefix := root + string(os.PathSeparator)
	return strings.HasPrefix(relDir, prefix)
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

func parseMiddlewareTargets(value string) ([]model.Selector, error) {
	if strings.TrimSpace(value) == "" {
		return nil, fmt.Errorf("missing target selector")
	}
	parts := strings.Split(value, ",")
	targets := make([]model.Selector, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("invalid empty target selector")
		}
		switch {
		case part == "all":
			targets = append(targets, model.Selector{Kind: model.SelectorAll})
		case strings.HasPrefix(part, "tag:"):
			tag := strings.TrimSpace(strings.TrimPrefix(part, "tag:"))
			if tag == "" {
				return nil, fmt.Errorf("invalid tag selector %q", part)
			}
			targets = append(targets, model.Selector{Kind: model.SelectorTag, Value: tag})
		default:
			return nil, fmt.Errorf("unsupported target selector %q", part)
		}
	}
	return targets, nil
}

func discoverRuntimeDeclarations(pkg *model.Package) []*model.RuntimeDeclaration {
	if pkg == nil || pkg.GoPkg == nil {
		return nil
	}
	var decls []*model.RuntimeDeclaration
	for _, file := range pkg.Files {
		aliases := runtimeImportAliases(file.AST)
		if len(aliases) == 0 {
			continue
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := selectorFromRuntimeCall(call.Fun)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			importPath, ok := aliases[ident.Name]
			if !ok {
				return true
			}
			kind, nameArg, ok := runtimeDeclarationKind(importPath, sel.Sel.Name)
			if !ok {
				return true
			}
			taskQueue, taskQueueExplicit, taskQueueResolved := runtimeDeclarationTaskQueue(pkg, call, kind, aliases)
			decls = append(decls, &model.RuntimeDeclaration{
				Package:           pkg,
				File:              file,
				Kind:              kind,
				Name:              runtimeDeclarationName(pkg, call, nameArg),
				CallName:          sel.Sel.Name,
				TokenPos:          call.Lparen,
				TaskQueue:         taskQueue,
				TaskQueueExplicit: taskQueueExplicit,
				TaskQueueResolved: taskQueueResolved,
			})
			return true
		})
	}
	slices.SortFunc(decls, func(a, b *model.RuntimeDeclaration) int {
		if cmp := strings.Compare(a.File.Path, b.File.Path); cmp != 0 {
			return cmp
		}
		if a.TokenPos < b.TokenPos {
			return -1
		}
		if a.TokenPos > b.TokenPos {
			return 1
		}
		return 0
	})
	return decls
}

func runtimeDeclarationTaskQueue(pkg *model.Package, call *ast.CallExpr, kind model.RuntimeDeclarationKind, aliases map[string]string) (string, bool, bool) {
	switch kind {
	case model.RuntimeDeclarationTemporalWorkflow:
		if len(call.Args) <= 1 {
			return "", false, false
		}
		return temporalConfigTaskQueue(pkg, call.Args[1], "WorkflowConfig", aliases)
	case model.RuntimeDeclarationTemporalActivity:
		if len(call.Args) <= 1 {
			return "", false, false
		}
		return temporalConfigTaskQueue(pkg, call.Args[1], "ActivityConfig", aliases)
	default:
		return "", false, false
	}
}

func temporalConfigTaskQueue(pkg *model.Package, expr ast.Expr, typeName string, aliases map[string]string) (string, bool, bool) {
	lit, zeroValue, ok := temporalConfigLiteral(pkg, expr, typeName, aliases)
	if !ok {
		return "", false, false
	}
	if zeroValue {
		return "", false, true
	}
	if len(lit.Elts) == 0 {
		return "", false, true
	}
	hasKeyedElements := false
	for _, elt := range lit.Elts {
		if _, ok := elt.(*ast.KeyValueExpr); ok {
			hasKeyedElements = true
			break
		}
	}
	if !hasKeyedElements {
		value, ok := literalStringValue(pkg, lit.Elts[0])
		if !ok {
			return "", true, false
		}
		return strings.TrimSpace(value), true, true
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name != "TaskQueue" {
			continue
		}
		value, ok := literalStringValue(pkg, kv.Value)
		if !ok {
			return "", true, false
		}
		return strings.TrimSpace(value), true, true
	}
	return "", false, true
}

func validateTemporalRuntimeCalls(pkg *model.Package) []string {
	if pkg == nil || pkg.GoPkg == nil {
		return nil
	}
	var errs []string
	for _, file := range pkg.Files {
		aliases := runtimeImportAliases(file.AST)
		if len(aliases) == 0 {
			continue
		}
		ast.Inspect(file.AST, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := selectorFromRuntimeCall(call.Fun)
			if !ok {
				return true
			}
			ident, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			if aliases[ident.Name] != "github.com/pbrazdil/onlava/temporal" {
				return true
			}
			switch sel.Sel.Name {
			case "NewActivity":
				if len(call.Args) > 1 && temporalActivityConfigHasEmptyTaskQueue(pkg, call.Args[1], aliases) {
					errs = append(errs, sourceDiagnostic(pkg, call.Lparen, "temporal.NewActivity requires temporal.ActivityConfig.TaskQueue"))
				}
			case "Start":
				if len(call.Args) == 3 {
					errs = append(errs, sourceDiagnostic(pkg, call.Lparen, "temporal.Start requires a workflow identity argument such as temporal.WorkflowID(id) or temporal.WorkflowIDPrefix(prefix)"))
				}
			}
			return true
		})
	}
	return errs
}

func temporalActivityConfigHasEmptyTaskQueue(pkg *model.Package, expr ast.Expr, aliases map[string]string) bool {
	value, explicit, resolved := temporalConfigTaskQueue(pkg, expr, "ActivityConfig", aliases)
	if !resolved {
		return false
	}
	return !explicit || strings.TrimSpace(value) == ""
}

func temporalConfigLiteral(pkg *model.Package, expr ast.Expr, typeName string, aliases map[string]string) (*ast.CompositeLit, bool, bool) {
	if lit, ok := expr.(*ast.CompositeLit); ok && isTemporalConfigType(lit.Type, typeName, aliases) {
		return lit, false, true
	}
	obj := objectFromExpr(pkg, expr)
	if obj == nil {
		return nil, false, false
	}
	init, zeroValue, ok := objectInitializer(pkg, obj)
	if !ok {
		return nil, false, false
	}
	if zeroValue {
		return nil, true, true
	}
	lit, ok := init.(*ast.CompositeLit)
	if !ok || !isTemporalConfigType(lit.Type, typeName, aliases) {
		return nil, false, false
	}
	return lit, false, true
}

func objectFromExpr(pkg *model.Package, expr ast.Expr) types.Object {
	if pkg == nil || pkg.GoPkg == nil {
		return nil
	}
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return nil
	}
	return pkg.GoPkg.TypesInfo.Uses[ident]
}

func objectInitializer(pkg *model.Package, obj types.Object) (ast.Expr, bool, bool) {
	if pkg == nil || pkg.GoPkg == nil || obj == nil {
		return nil, false, false
	}
	for _, file := range pkg.Files {
		var found ast.Expr
		var zeroValue bool
		ast.Inspect(file.AST, func(node ast.Node) bool {
			if found != nil || zeroValue {
				return false
			}
			switch stmt := node.(type) {
			case *ast.ValueSpec:
				for i, name := range stmt.Names {
					if pkg.GoPkg.TypesInfo.Defs[name] != obj {
						continue
					}
					if len(stmt.Values) == 0 {
						zeroValue = true
						return false
					}
					if i < len(stmt.Values) {
						found = stmt.Values[i]
					} else if len(stmt.Values) == 1 {
						found = stmt.Values[0]
					}
					return false
				}
			case *ast.AssignStmt:
				if stmt.Tok != token.DEFINE {
					return true
				}
				for i, lhs := range stmt.Lhs {
					name, ok := lhs.(*ast.Ident)
					if !ok || pkg.GoPkg.TypesInfo.Defs[name] != obj {
						continue
					}
					if i < len(stmt.Rhs) {
						found = stmt.Rhs[i]
					} else if len(stmt.Rhs) == 1 {
						found = stmt.Rhs[0]
					}
					return false
				}
			}
			return true
		})
		if found != nil || zeroValue {
			return found, zeroValue, true
		}
	}
	return nil, false, false
}

func isTemporalConfigType(expr ast.Expr, typeName string, aliases map[string]string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != typeName {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return aliases[ident.Name] == "github.com/pbrazdil/onlava/temporal"
}

func literalStringValue(pkg *model.Package, expr ast.Expr) (string, bool) {
	if pkg != nil && pkg.GoPkg != nil {
		if tv, ok := pkg.GoPkg.TypesInfo.Types[expr]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
			return constant.StringVal(tv.Value), true
		}
	}
	if lit, ok := expr.(*ast.BasicLit); ok && lit.Kind == token.STRING {
		return strings.Trim(lit.Value, "\"`"), true
	}
	return "", false
}

func sourceDiagnostic(pkg *model.Package, pos token.Pos, message string) string {
	if pkg != nil && pkg.GoPkg != nil && pkg.GoPkg.Fset != nil {
		position := pkg.GoPkg.Fset.Position(pos)
		if position.Filename != "" {
			return fmt.Sprintf("%s:%d:%d: %s", position.Filename, position.Line, position.Column, message)
		}
	}
	return message
}

func runtimeImportAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	if file == nil {
		return aliases
	}
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, "\"")
		defaultAlias := ""
		switch importPath {
		case "github.com/pbrazdil/onlava/temporal":
			defaultAlias = "temporal"
		case "github.com/pbrazdil/onlava/cron":
			defaultAlias = "cron"
		default:
			continue
		}
		if imp.Name != nil && imp.Name.Name != "." {
			aliases[imp.Name.Name] = importPath
			continue
		}
		aliases[defaultAlias] = importPath
	}
	return aliases
}

func runtimeDeclarationKind(importPath, callName string) (model.RuntimeDeclarationKind, int, bool) {
	switch importPath {
	case "github.com/pbrazdil/onlava/temporal":
		switch callName {
		case "NewWorkflow":
			return model.RuntimeDeclarationTemporalWorkflow, 0, true
		case "NewActivity":
			return model.RuntimeDeclarationTemporalActivity, 0, true
		}
	case "github.com/pbrazdil/onlava/cron":
		if callName == "NewJob" {
			return model.RuntimeDeclarationCronJob, 0, true
		}
	}
	return "", 0, false
}

func runtimeDeclarationName(pkg *model.Package, call *ast.CallExpr, arg int) string {
	if pkg == nil || pkg.GoPkg == nil || call == nil || arg < 0 || arg >= len(call.Args) {
		return ""
	}
	tv, ok := pkg.GoPkg.TypesInfo.Types[call.Args[arg]]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.String {
		return ""
	}
	return constant.StringVal(tv.Value)
}

func selectorFromRuntimeCall(expr ast.Expr) (*ast.SelectorExpr, bool) {
	switch v := expr.(type) {
	case *ast.SelectorExpr:
		return v, true
	case *ast.IndexExpr:
		return selectorFromRuntimeCall(v.X)
	case *ast.IndexListExpr:
		return selectorFromRuntimeCall(v.X)
	default:
		return nil, false
	}
}

func sortMiddleware(root string, middlewares []*model.Middleware) {
	slices.SortStableFunc(middlewares, func(a, b *model.Middleware) int {
		if a.Global != b.Global {
			if a.Global {
				return -1
			}
			return 1
		}
		aPath := relativeFilePath(root, a.File.Path)
		bPath := relativeFilePath(root, b.File.Path)
		if cmp := strings.Compare(aPath, bPath); cmp != 0 {
			return cmp
		}
		switch {
		case a.TokenPos < b.TokenPos:
			return -1
		case a.TokenPos > b.TokenPos:
			return 1
		default:
			return 0
		}
	})
}

func relativeFilePath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(rel)
}

func candidateEndpoints(app *model.App, mw *model.Middleware) []*model.Endpoint {
	if mw.Global {
		var endpoints []*model.Endpoint
		for _, svc := range app.Services {
			endpoints = append(endpoints, svc.Endpoints...)
		}
		return endpoints
	}
	if mw.Service == nil {
		return nil
	}
	return mw.Service.Endpoints
}

func middlewareAppliesToService(mw *model.Middleware, svc *model.Service) bool {
	return mw.Global || mw.Service == svc
}

func middlewareMatchesEndpoint(mw *model.Middleware, ep *model.Endpoint) bool {
	for _, target := range mw.Targets {
		switch target.Kind {
		case model.SelectorAll:
			return true
		case model.SelectorTag:
			if slices.Contains(ep.Tags, target.Value) {
				return true
			}
		}
	}
	return false
}

func paramKind(t types.Type) (runtimeapi.ParamKind, bool) {
	switch u := types.Unalias(t).(type) {
	case *types.Basic:
		switch u.Kind() {
		case types.String:
			return runtimeapi.ParamString, true
		case types.Bool:
			return runtimeapi.ParamBool, true
		case types.Int:
			return runtimeapi.ParamInt, true
		case types.Int8:
			return runtimeapi.ParamInt8, true
		case types.Int16:
			return runtimeapi.ParamInt16, true
		case types.Int32:
			return runtimeapi.ParamInt32, true
		case types.Int64:
			return runtimeapi.ParamInt64, true
		case types.Uint:
			return runtimeapi.ParamUint, true
		case types.Uint8:
			return runtimeapi.ParamUint8, true
		case types.Uint16:
			return runtimeapi.ParamUint16, true
		case types.Uint32:
			return runtimeapi.ParamUint32, true
		case types.Uint64:
			return runtimeapi.ParamUint64, true
		}
	case *types.Named:
		if isAuthUIDType(u) {
			return runtimeapi.ParamString, true
		}
	}
	return "", false
}

func isAuthUIDType(t types.Type) bool {
	return isNamedType(t, "github.com/pbrazdil/onlava/auth", "UID")
}

func isMiddlewareNamedType(t types.Type, name string) bool {
	return isNamedType(t, "github.com/pbrazdil/onlava/middleware", name)
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
