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
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"golang.org/x/tools/go/packages"

	"scenery.sh/auth"
	"scenery.sh/internal/model"
	"scenery.sh/internal/runtimeapi"
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
	entityConfigs, configErrs := discoverEntityConfigs(app.Packages)
	errs = append(errs, configErrs...)

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
					if dir == nil {
						continue
					}
					foundDirective = true
					switch dir.name {
					case "service":
						ss, err := parseServiceStruct(pkg, file, node)
						if err != nil {
							errs = append(errs, err.Error())
							continue
						}
						if pkg.Service.Struct != nil {
							errs = append(errs, fmt.Sprintf("duplicate scenery:service directive in service %s", pkg.Service.Name))
							continue
						}
						ss.Service = pkg.Service
						pkg.Service.Struct = ss
					case "model":
						entity, modelErrs := parseEntity(pkg, file, node, entityConfigs[entityConfigKey(pkg.ImportPath, modelDirectiveTypeName(node))])
						errs = append(errs, modelErrs...)
						if entity != nil {
							app.Entities = append(app.Entities, entity)
						}
					case "page":
						view, viewErrs := parsePageView(root, pkg, file, node)
						errs = append(errs, viewErrs...)
						if view != nil {
							app.Views = append(app.Views, view)
						}
					}
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
		errs = append(errs, "only one scenery:authhandler is supported per application")
	}
	if !foundDirective {
		errs = append(errs, "no scenery directives found in application")
	}
	if len(authHandlers) == 1 {
		authHandlers[0].Service.AuthHandler = authHandlers[0]
	}
	sortMiddleware(root, middlewares)
	app.Middleware = middlewares
	errs = append(errs, attachGeneratedModelEndpoints(app)...)
	errs = append(errs, validateGeneratedEndpointCollisions(app)...)

	for _, svc := range app.Services {
		if svc.Struct != nil {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil && ep.Receiver.TypeName != svc.Struct.TypeName {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s receiver %s does not match scenery:service struct %s", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Struct.TypeName))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil && svc.AuthHandler.Receiver.TypeName != svc.Struct.TypeName {
				errs = append(errs, fmt.Sprintf("auth handler %s receiver %s does not match scenery:service struct %s", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Struct.TypeName))
			}
			for _, mw := range svc.Middleware {
				if mw.Receiver != nil && mw.Receiver.TypeName != svc.Struct.TypeName {
					errs = append(errs, fmt.Sprintf("middleware %s receiver %s does not match scenery:service struct %s", mw.Name, mw.Receiver.TypeName, svc.Struct.TypeName))
				}
			}
		} else {
			for _, ep := range svc.Endpoints {
				if ep.Receiver != nil {
					errs = append(errs, fmt.Sprintf("endpoint %s.%s uses receiver %s but service %s has no scenery:service struct", svc.Name, ep.Name, ep.Receiver.TypeName, svc.Name))
				}
			}
			if svc.AuthHandler != nil && svc.AuthHandler.Receiver != nil {
				errs = append(errs, fmt.Sprintf("auth handler %s uses receiver %s but service %s has no scenery:service struct", svc.AuthHandler.Name, svc.AuthHandler.Receiver.TypeName, svc.Name))
			}
			for _, mw := range svc.Middleware {
				if mw.Receiver != nil {
					errs = append(errs, fmt.Sprintf("middleware %s uses receiver %s but service %s has no scenery:service struct", mw.Name, mw.Receiver.TypeName, svc.Name))
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
		for _, ep := range svc.Generated {
			key := ep.Name
			if seenNames[key] {
				errs = append(errs, fmt.Sprintf("generated model endpoint %s collides with endpoint name in service %s; declare model.Override or model.Disable for the action", ep.Name, svc.Name))
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
	errs = append(errs, validateViews(app)...)

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
	slices.SortFunc(app.Entities, func(a, b *model.Entity) int {
		if cmp := strings.Compare(a.Package.RelDir, b.Package.RelDir); cmp != 0 {
			return cmp
		}
		return strings.Compare(a.Name, b.Name)
	})
	slices.SortFunc(app.Views, func(a, b *model.View) int {
		if cmp := strings.Compare(a.Package.RelDir, b.Package.RelDir); cmp != 0 {
			return cmp
		}
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
		if svc == nil || (len(svc.Endpoints) == 0 && len(svc.Generated) == 0) {
			continue
		}
		pruned = append(pruned, svc)
	}
	return pruned
}

func attachGeneratedModelEndpoints(app *model.App) []string {
	if app == nil || len(app.Entities) == 0 {
		return nil
	}
	var errs []string
	for _, entity := range app.Entities {
		if entity == nil || len(entity.CRUD.Actions) == 0 {
			continue
		}
		if entity.Package == nil || entity.Package.Service == nil {
			errs = append(errs, fmt.Sprintf("model %s cannot generate CRUD endpoints without a service package", entity.Name))
			continue
		}
		if primaryKeyField(entity) == nil {
			errs = append(errs, sourceDiagnostic(entity.Package, entity.TokenPos, fmt.Sprintf("model %s needs an ID field before CRUD endpoints can be generated", entity.Name)))
			continue
		}
		overrides := make(map[model.EntityCRUDAction]string, len(entity.CRUD.Overrides))
		for _, override := range entity.CRUD.Overrides {
			overrides[override.Action] = override.Endpoint
		}
		for _, action := range entity.CRUD.Actions {
			if overrides[action] != "" {
				continue
			}
			ep, err := buildGeneratedModelEndpoint(entity, action)
			if err != nil {
				errs = append(errs, sourceDiagnostic(entity.Package, entity.TokenPos, err.Error()))
				continue
			}
			entity.Package.Service.Generated = append(entity.Package.Service.Generated, ep)
		}
		for _, override := range entity.CRUD.Overrides {
			if !serviceHasEndpoint(entity.Package.Service, override.Endpoint) {
				errs = append(errs, sourceDiagnostic(entity.Package, entity.TokenPos, fmt.Sprintf("model.Override(%q, %s) references no endpoint in service %s", override.Action, override.Endpoint, entity.Package.Service.Name)))
			}
		}
	}
	return errs
}

func buildGeneratedModelEndpoint(entity *model.Entity, action model.EntityCRUDAction) (*model.GeneratedModelEndpoint, error) {
	idField := primaryKeyField(entity)
	if idField == nil {
		return nil, fmt.Errorf("model %s needs an ID field before CRUD endpoints can be generated", entity.Name)
	}
	routeBase := model.EntityCRUDRouteBase(entity)
	ep := &model.GeneratedModelEndpoint{
		Service:   entity.Package.Service,
		Package:   entity.Package,
		Entity:    entity,
		Action:    action,
		Access:    runtimeapi.Auth,
		Generated: true,
	}
	switch action {
	case model.EntityCRUDList:
		ep.Name = "List" + entity.Name + "s"
		ep.Path = routeBase
		ep.Methods = []string{"GET"}
	case model.EntityCRUDGet:
		ep.Name = "Get" + entity.Name
		ep.Path = routeBase + "/:id"
		ep.Methods = []string{"GET"}
		ep.PathParams = []model.Param{{Name: "id", Kind: generatedIDParamKind(*idField)}}
	case model.EntityCRUDCreate:
		ep.Name = "Create" + entity.Name
		ep.Path = routeBase
		ep.Methods = []string{"POST"}
		ep.HasPayload = true
	case model.EntityCRUDUpdate:
		ep.Name = "Update" + entity.Name
		ep.Path = routeBase + "/:id"
		ep.Methods = []string{"PATCH"}
		ep.PathParams = []model.Param{{Name: "id", Kind: generatedIDParamKind(*idField)}}
		ep.HasPayload = true
	case model.EntityCRUDDelete:
		ep.Name = "Delete" + entity.Name
		ep.Path = routeBase + "/:id"
		ep.Methods = []string{"DELETE"}
		ep.PathParams = []model.Param{{Name: "id", Kind: generatedIDParamKind(*idField)}}
	default:
		return nil, fmt.Errorf("unsupported generated model action %q", action)
	}
	return ep, nil
}

func generatedIDParamKind(field model.EntityField) runtimeapi.ParamKind {
	switch strings.TrimPrefix(strings.TrimSpace(field.TypeExpr), "*") {
	case "bool":
		return runtimeapi.ParamBool
	case "int":
		return runtimeapi.ParamInt
	case "int8":
		return runtimeapi.ParamInt8
	case "int16":
		return runtimeapi.ParamInt16
	case "int32":
		return runtimeapi.ParamInt32
	case "int64":
		return runtimeapi.ParamInt64
	case "uint":
		return runtimeapi.ParamUint
	case "uint8":
		return runtimeapi.ParamUint8
	case "uint16":
		return runtimeapi.ParamUint16
	case "uint32":
		return runtimeapi.ParamUint32
	case "uint64":
		return runtimeapi.ParamUint64
	default:
		return runtimeapi.ParamString
	}
}

func primaryKeyField(entity *model.Entity) *model.EntityField {
	for i := range entity.Fields {
		field := &entity.Fields[i]
		if field.Kind != model.EntityFieldComputed && strings.EqualFold(field.Name, "id") {
			return field
		}
	}
	return nil
}

func validateGeneratedEndpointCollisions(app *model.App) []string {
	if app == nil {
		return nil
	}
	var errs []string
	namesByService := map[string]map[string]string{}
	var routes []routeOwner
	for _, svc := range app.Services {
		if svc == nil {
			continue
		}
		if namesByService[svc.Name] == nil {
			namesByService[svc.Name] = map[string]string{}
		}
		for _, ep := range svc.Endpoints {
			namesByService[svc.Name][ep.Name] = svc.Name + "." + ep.Name
			routes = append(routes, routeOwner{id: svc.Name + "." + ep.Name, path: ep.Path, methods: ep.Methods})
		}
	}
	for _, svc := range app.Services {
		if svc == nil {
			continue
		}
		for _, ep := range svc.Generated {
			if existing := namesByService[svc.Name][ep.Name]; existing != "" {
				errs = append(errs, sourceDiagnostic(ep.Package, ep.Entity.TokenPos, fmt.Sprintf("generated model endpoint %s collides with endpoint name %s in service %s; declare model.Override or model.Disable for the action", ep.Name, existing, svc.Name)))
			}
			for _, existing := range routes {
				if routeOwnersCollide(existing, routeOwner{path: ep.Path, methods: ep.Methods}) {
					errs = append(errs, sourceDiagnostic(ep.Package, ep.Entity.TokenPos, fmt.Sprintf("generated model endpoint %s %s collides with endpoint %s at %s; declare model.Override or model.Disable for the action", strings.Join(ep.Methods, ","), ep.Path, existing.id, existing.path)))
				}
			}
			namesByService[svc.Name][ep.Name] = svc.Name + "." + ep.Name
			routes = append(routes, routeOwner{id: svc.Name + "." + ep.Name, path: ep.Path, methods: ep.Methods})
		}
	}
	return errs
}

type routeOwner struct {
	id      string
	path    string
	methods []string
}

func routeOwnersCollide(left, right routeOwner) bool {
	if normalizeRoutePattern(left.path) != normalizeRoutePattern(right.path) {
		return false
	}
	return routeMethodsCollide(left.methods, right.methods)
}

func routeMethodsCollide(a, b []string) bool {
	if len(a) == 0 || len(b) == 0 {
		return true
	}
	for _, left := range a {
		left = strings.ToUpper(strings.TrimSpace(left))
		for _, right := range b {
			right = strings.ToUpper(strings.TrimSpace(right))
			if left == "" || right == "" || left == "*" || right == "*" || left == right {
				return true
			}
		}
	}
	return false
}

func normalizeRoutePattern(path string) string {
	path = strings.TrimSuffix(strings.TrimSpace(path), "/")
	if path == "" {
		path = "/"
	}
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = ":"
			continue
		}
		if strings.HasPrefix(part, "*") {
			parts[i] = "*"
		}
	}
	if len(parts) == 1 && parts[0] == "" {
		return "/"
	}
	return "/" + strings.Join(parts, "/")
}

func serviceHasEndpoint(svc *model.Service, name string) bool {
	for _, ep := range svc.Endpoints {
		if ep.Name == name {
			return true
		}
	}
	return false
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
		ImplName:     "sceneryInternalImpl" + fn.Name.Name,
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
		return nil, fmt.Errorf("scenery:service must be declared on a single struct type")
	}
	spec, ok := decl.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, fmt.Errorf("scenery:service must annotate a type declaration")
	}
	if _, ok := spec.Type.(*ast.StructType); !ok {
		return nil, fmt.Errorf("scenery:service must annotate a struct type")
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
		GetterName:  "sceneryInternalGet" + typeName,
		InstanceVar: "sceneryInternalService" + typeName,
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
			for sel := range methods.Methods() {
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

type entityConfig struct {
	Table       string
	Fields      map[string]entityFieldConfig
	Seeds       []model.EntitySeedRow
	CRUD        model.EntityCRUD
	GenerateSet bool
}

type entityFieldConfig struct {
	Kind        model.EntityFieldKind
	EnumValues  []string
	Filterable  bool
	RenamedFrom string
}

func discoverEntityConfigs(pkgs []*model.Package) (map[string]entityConfig, []string) {
	configs := make(map[string]entityConfig)
	var errs []string
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			aliases := importAliases(file.AST)
			if len(aliases) == 0 {
				continue
			}
			ast.Inspect(file.AST, func(node ast.Node) bool {
				call, ok := node.(*ast.CallExpr)
				if !ok || !isPackageCall(call.Fun, aliases, "scenery.sh/model", "Entity") {
					return true
				}
				typeName, ok := firstTypeArg(pkg.GoPkg.Fset, call.Fun)
				if !ok {
					errs = append(errs, sourceDiagnostic(pkg, call.Lparen, "model.Entity requires one static type argument"))
					return true
				}
				cfg := entityConfig{Fields: make(map[string]entityFieldConfig)}
				for _, arg := range call.Args {
					if err := parseEntityOption(pkg, aliases, arg, &cfg); err != nil {
						errs = append(errs, sourceDiagnostic(pkg, arg.Pos(), err.Error()))
					}
				}
				configs[entityConfigKey(pkg.ImportPath, typeName)] = cfg
				return true
			})
		}
	}
	return configs, errs
}

func parseEntityOption(pkg *model.Package, aliases map[string]string, expr ast.Expr, cfg *entityConfig) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("model.Entity options must be static model.* calls")
	}
	switch {
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Table"):
		if len(call.Args) != 1 {
			return fmt.Errorf("model.Table requires one string argument")
		}
		value, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.Table requires a constant string argument")
		}
		cfg.Table = strings.TrimSpace(value)
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Field"):
		if len(call.Args) == 0 {
			return fmt.Errorf("model.Field requires a field name")
		}
		fieldName, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.Field requires a constant field-name string")
		}
		fieldName = strings.TrimSpace(fieldName)
		if fieldName == "" {
			return fmt.Errorf("model.Field requires a non-empty field name")
		}
		fieldCfg := cfg.Fields[fieldName]
		for _, opt := range call.Args[1:] {
			if err := parseEntityFieldOption(pkg, aliases, opt, &fieldCfg); err != nil {
				return fmt.Errorf("model.Field(%q): %w", fieldName, err)
			}
		}
		cfg.Fields[fieldName] = fieldCfg
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Generate"):
		actions, err := parseEntityActionArgs(pkg, call.Args)
		if err != nil {
			return fmt.Errorf("model.Generate: %w", err)
		}
		cfg.GenerateSet = true
		cfg.CRUD.Actions = actions
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Seed"):
		for _, arg := range call.Args {
			row, err := parseEntitySeedRow(pkg, aliases, arg)
			if err != nil {
				return fmt.Errorf("model.Seed: %w", err)
			}
			cfg.Seeds = append(cfg.Seeds, row)
		}
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Disable"):
		actions, err := parseEntityActionArgs(pkg, call.Args)
		if err != nil {
			return fmt.Errorf("model.Disable: %w", err)
		}
		cfg.CRUD.Disabled = append(cfg.CRUD.Disabled, actions...)
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Override"):
		if len(call.Args) != 2 {
			return fmt.Errorf("model.Override requires an action and endpoint")
		}
		actions, err := parseEntityActionArgs(pkg, call.Args[:1])
		if err != nil {
			return fmt.Errorf("model.Override: %w", err)
		}
		endpoint := staticEndpointName(pkg, call.Args[1])
		if endpoint == "" {
			return fmt.Errorf("model.Override endpoint must be a static function identifier or selector")
		}
		cfg.CRUD.Overrides = append(cfg.CRUD.Overrides, model.EntityCRUDOverride{Action: actions[0], Endpoint: endpoint})
	default:
		return fmt.Errorf("unsupported model.Entity option; use model.Table, model.Field, model.Generate, model.Seed, model.Disable, or model.Override")
	}
	return nil
}

func parseEntitySeedRow(pkg *model.Package, aliases map[string]string, expr ast.Expr) (model.EntitySeedRow, error) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return model.EntitySeedRow{}, fmt.Errorf("rows must be static keyed struct literals")
	}
	row := model.EntitySeedRow{TokenPos: lit.Pos()}
	seen := map[string]bool{}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			return model.EntitySeedRow{}, fmt.Errorf("rows must use keyed struct fields")
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok || key.Name == "" {
			return model.EntitySeedRow{}, fmt.Errorf("row field keys must be identifiers")
		}
		if seen[key.Name] {
			return model.EntitySeedRow{}, fmt.Errorf("row field %s is set more than once", key.Name)
		}
		value, err := parseEntitySeedValue(pkg, aliases, kv.Value)
		if err != nil {
			return model.EntitySeedRow{}, fmt.Errorf("%s: %w", key.Name, err)
		}
		value.Field = key.Name
		value.TokenPos = kv.Pos()
		row.Values = append(row.Values, value)
		seen[key.Name] = true
	}
	return row, nil
}

func parseEntitySeedValue(pkg *model.Package, aliases map[string]string, expr ast.Expr) (model.EntitySeedValue, error) {
	if call, ok := expr.(*ast.CallExpr); ok && isPackageCall(call.Fun, aliases, "time", "Date") {
		value, err := parseStaticTimeDate(pkg, aliases, call)
		if err != nil {
			return model.EntitySeedValue{}, err
		}
		return model.EntitySeedValue{Kind: model.EntitySeedTimestamp, Value: value}, nil
	}
	if pkg != nil && pkg.GoPkg != nil {
		if tv, ok := pkg.GoPkg.TypesInfo.Types[expr]; ok && tv.Value != nil {
			switch tv.Value.Kind() {
			case constant.String:
				value, err := strconv.Unquote(tv.Value.ExactString())
				if err != nil {
					return model.EntitySeedValue{}, err
				}
				return model.EntitySeedValue{Kind: model.EntitySeedString, Value: value}, nil
			case constant.Int:
				return model.EntitySeedValue{Kind: model.EntitySeedInteger, Value: tv.Value.ExactString()}, nil
			case constant.Float:
				return model.EntitySeedValue{Kind: model.EntitySeedFloat, Value: tv.Value.ExactString()}, nil
			case constant.Bool:
				return model.EntitySeedValue{Kind: model.EntitySeedBool, Value: tv.Value.ExactString()}, nil
			}
		}
	}
	return model.EntitySeedValue{}, fmt.Errorf("seed values must be compile-time constants or time.Date(...)")
}

func parseStaticTimeDate(pkg *model.Package, aliases map[string]string, call *ast.CallExpr) (string, error) {
	if len(call.Args) != 8 {
		return "", fmt.Errorf("time.Date seed values require eight arguments")
	}
	ints := make([]int, 7)
	for i := 0; i < 7; i++ {
		tv, ok := pkg.GoPkg.TypesInfo.Types[call.Args[i]]
		if !ok || tv.Value == nil || tv.Value.Kind() != constant.Int {
			return "", fmt.Errorf("time.Date argument %d must be a constant integer", i+1)
		}
		value, ok := constant.Int64Val(tv.Value)
		if !ok {
			return "", fmt.Errorf("time.Date argument %d is out of range", i+1)
		}
		ints[i] = int(value)
	}
	if !isTimeUTCSelector(call.Args[7], aliases) {
		return "", fmt.Errorf("time.Date seed values must use time.UTC")
	}
	t := time.Date(ints[0], time.Month(ints[1]), ints[2], ints[3], ints[4], ints[5], ints[6], time.UTC)
	return t.UTC().Format(time.RFC3339Nano), nil
}

func isTimeUTCSelector(expr ast.Expr, aliases map[string]string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "UTC" {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && aliases[ident.Name] == "time"
}

func parseEntityActionArgs(pkg *model.Package, args []ast.Expr) ([]model.EntityCRUDAction, error) {
	if len(args) == 0 {
		return defaultEntityCRUDActions(), nil
	}
	seen := make(map[model.EntityCRUDAction]bool, len(args))
	out := make([]model.EntityCRUDAction, 0, len(args))
	for _, arg := range args {
		value, ok := staticStringValue(pkg, arg)
		if !ok {
			return nil, fmt.Errorf("actions must be constant model.Action/string values")
		}
		action, ok := normalizeEntityCRUDAction(value)
		if !ok {
			return nil, fmt.Errorf("unsupported action %q", value)
		}
		if !seen[action] {
			seen[action] = true
			out = append(out, action)
		}
	}
	return out, nil
}

func normalizeEntityCRUDAction(value string) (model.EntityCRUDAction, bool) {
	switch model.EntityCRUDAction(strings.ToLower(strings.TrimSpace(value))) {
	case model.EntityCRUDList:
		return model.EntityCRUDList, true
	case model.EntityCRUDGet:
		return model.EntityCRUDGet, true
	case model.EntityCRUDCreate:
		return model.EntityCRUDCreate, true
	case model.EntityCRUDUpdate:
		return model.EntityCRUDUpdate, true
	case model.EntityCRUDDelete:
		return model.EntityCRUDDelete, true
	default:
		return "", false
	}
}

func defaultEntityCRUDActions() []model.EntityCRUDAction {
	return []model.EntityCRUDAction{
		model.EntityCRUDList,
		model.EntityCRUDGet,
		model.EntityCRUDCreate,
		model.EntityCRUDUpdate,
		model.EntityCRUDDelete,
	}
}

func staticEndpointName(pkg *model.Package, expr ast.Expr) string {
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name
	case *ast.SelectorExpr:
		return v.Sel.Name
	default:
		return ""
	}
}

func parseEntityFieldOption(pkg *model.Package, aliases map[string]string, expr ast.Expr, cfg *entityFieldConfig) error {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return fmt.Errorf("field options must be static model.* calls")
	}
	switch {
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "EnumValues"):
		if len(call.Args) == 0 {
			return fmt.Errorf("model.EnumValues requires at least one value")
		}
		cfg.EnumValues = nil
		for _, arg := range call.Args {
			value, ok := staticStringValue(pkg, arg)
			if !ok {
				return fmt.Errorf("model.EnumValues requires constant string arguments")
			}
			cfg.EnumValues = append(cfg.EnumValues, value)
		}
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Filterable"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Filterable takes no arguments")
		}
		cfg.Filterable = true
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Computed"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Computed takes no arguments")
		}
		cfg.Kind = model.EntityFieldComputed
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "Relationship"):
		if len(call.Args) != 0 {
			return fmt.Errorf("model.Relationship takes no arguments")
		}
		cfg.Kind = model.EntityFieldRelationship
	case isPackageCall(call.Fun, aliases, "scenery.sh/model", "RenamedFrom"):
		if len(call.Args) != 1 {
			return fmt.Errorf("model.RenamedFrom requires one string argument")
		}
		value, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			return fmt.Errorf("model.RenamedFrom requires a constant string argument")
		}
		cfg.RenamedFrom = strings.TrimSpace(value)
	default:
		return fmt.Errorf("unsupported field option")
	}
	return nil
}

func parseEntity(pkg *model.Package, file *model.File, decl *ast.GenDecl, cfg entityConfig) (*model.Entity, []string) {
	var errs []string
	if len(decl.Specs) != 1 {
		return nil, []string{"scenery:model must be declared on a single struct type"}
	}
	spec, ok := decl.Specs[0].(*ast.TypeSpec)
	if !ok {
		return nil, []string{"scenery:model must annotate a type declaration"}
	}
	structType, ok := spec.Type.(*ast.StructType)
	if !ok {
		return nil, []string{"scenery:model must annotate a struct type"}
	}
	entity := &model.Entity{
		Package:  pkg,
		File:     file,
		Name:     spec.Name.Name,
		TypeExpr: spec.Name.Name,
		Table:    firstNonEmpty(cfg.Table, defaultTableName(spec.Name.Name)),
		CRUD:     normalizeEntityCRUD(cfg),
		TokenPos: spec.Pos(),
	}
	seenFields := make(map[string]bool)
	fieldByName := make(map[string]model.EntityField)
	for _, field := range structType.Fields.List {
		if len(field.Names) == 0 {
			continue
		}
		typeExpr := renderNode(pkg.GoPkg.Fset, field.Type)
		fieldType := pkg.GoPkg.TypesInfo.TypeOf(field.Type)
		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}
			fieldCfg := cfg.Fields[name.Name]
			kind := fieldCfg.Kind
			if kind == "" {
				kind = model.EntityFieldStored
			}
			item := model.EntityField{
				Name:        name.Name,
				TypeExpr:    typeExpr,
				Type:        fieldType,
				Kind:        kind,
				Column:      fieldColumnName(field, name.Name),
				EnumValues:  append([]string(nil), fieldCfg.EnumValues...),
				Filterable:  fieldCfg.Filterable,
				RenamedFrom: fieldCfg.RenamedFrom,
			}
			entity.Fields = append(entity.Fields, item)
			seenFields[name.Name] = true
			fieldByName[name.Name] = item
		}
	}
	for fieldName := range cfg.Fields {
		if !seenFields[fieldName] {
			errs = append(errs, sourceDiagnostic(pkg, decl.Pos(), fmt.Sprintf("model.Field(%q) does not match a field on %s", fieldName, spec.Name.Name)))
		}
	}
	seedErrs := validateEntitySeedRows(pkg, spec, cfg.Seeds, fieldByName)
	errs = append(errs, seedErrs...)
	if len(seedErrs) == 0 {
		entity.Seeds = append(entity.Seeds, cfg.Seeds...)
	}
	if len(entity.CRUD.Actions) > 0 {
		if tenantField := entity.TenantField(); tenantField != nil && model.GeneratedTenantFieldKind(*tenantField) == "" {
			errs = append(errs, sourceDiagnostic(pkg, spec.Pos(), fmt.Sprintf("model %s tenant field %s has unsupported type %s; generated CRUD supports string, named string, or github.com/google/uuid.UUID tenant fields", entity.Name, tenantField.Name, firstNonEmpty(tenantField.TypeExpr, "<unknown>"))))
		}
	}
	return entity, errs
}

func validateEntitySeedRows(pkg *model.Package, spec *ast.TypeSpec, rows []model.EntitySeedRow, fields map[string]model.EntityField) []string {
	var errs []string
	for _, row := range rows {
		hasID := false
		for _, value := range row.Values {
			field, ok := fields[value.Field]
			if !ok {
				errs = append(errs, sourceDiagnostic(pkg, value.TokenPos, fmt.Sprintf("model.Seed field %q does not match a field on %s", value.Field, spec.Name.Name)))
				continue
			}
			if field.Kind == model.EntityFieldComputed {
				errs = append(errs, sourceDiagnostic(pkg, value.TokenPos, fmt.Sprintf("model.Seed field %q is computed and cannot be seeded", value.Field)))
				continue
			}
			if strings.EqualFold(field.Name, "id") {
				hasID = true
			}
			if len(field.EnumValues) > 0 && value.Kind == model.EntitySeedString && !slices.Contains(field.EnumValues, value.Value) {
				errs = append(errs, sourceDiagnostic(pkg, value.TokenPos, fmt.Sprintf("model.Seed field %q value %q is not in enum values", value.Field, value.Value)))
			}
		}
		if !hasID {
			errs = append(errs, sourceDiagnostic(pkg, row.TokenPos, fmt.Sprintf("model.Seed for %s requires an ID field value", spec.Name.Name)))
		}
	}
	return errs
}

func normalizeEntityCRUD(cfg entityConfig) model.EntityCRUD {
	if !cfg.GenerateSet {
		return model.EntityCRUD{}
	}
	actions := cfg.CRUD.Actions
	if len(actions) == 0 {
		actions = defaultEntityCRUDActions()
	}
	disabled := make(map[model.EntityCRUDAction]bool, len(cfg.CRUD.Disabled))
	for _, action := range cfg.CRUD.Disabled {
		disabled[action] = true
	}
	out := model.EntityCRUD{
		Disabled:  append([]model.EntityCRUDAction(nil), cfg.CRUD.Disabled...),
		Overrides: append([]model.EntityCRUDOverride(nil), cfg.CRUD.Overrides...),
	}
	for _, action := range actions {
		if !disabled[action] {
			out.Actions = append(out.Actions, action)
		}
	}
	return out
}

func parsePageView(root string, pkg *model.Package, file *model.File, decl *ast.GenDecl) (*model.View, []string) {
	var errs []string
	if decl.Tok != token.VAR || len(decl.Specs) != 1 {
		return nil, []string{"scenery:page must be declared on a single var declaration"}
	}
	spec, ok := decl.Specs[0].(*ast.ValueSpec)
	if !ok || len(spec.Names) != 1 || len(spec.Values) != 1 {
		return nil, []string{"scenery:page must annotate one initialized page variable"}
	}
	lit, ok := spec.Values[0].(*ast.CompositeLit)
	if !ok || !isPageCollectionType(lit.Type, importAliases(file.AST)) {
		return nil, []string{"scenery:page currently supports page.Collection[T] composite literals"}
	}
	entityName, ok := firstTypeArg(pkg.GoPkg.Fset, lit.Type)
	if !ok {
		return nil, []string{"page.Collection requires one static type argument"}
	}
	view := &model.View{
		Package:  pkg,
		File:     file,
		Name:     spec.Names[0].Name,
		Kind:     "collection",
		Entity:   entityName,
		TokenPos: spec.Names[0].Pos(),
	}
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, elt.Pos(), "page.Collection fields must use keyed literals"))
			continue
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		switch key.Name {
		case "Route":
			value, ok := staticStringValue(pkg, kv.Value)
			if !ok {
				errs = append(errs, sourceDiagnostic(pkg, kv.Value.Pos(), "page.Collection.Route must be a constant string"))
				continue
			}
			view.Route = value
		case "Title":
			value, ok := staticStringValue(pkg, kv.Value)
			if !ok {
				errs = append(errs, sourceDiagnostic(pkg, kv.Value.Pos(), "page.Collection.Title must be a constant string"))
				continue
			}
			view.Title = value
		case "Columns":
			values, ok := literalStringSlice(pkg, kv.Value)
			if !ok {
				errs = append(errs, sourceDiagnostic(pkg, kv.Value.Pos(), "page.Collection.Columns must be a static string slice"))
				continue
			}
			view.Columns = values
		case "Slots":
			slots, slotErrs := parsePageSlots(root, pkg, importAliases(file.AST), kv.Value)
			errs = append(errs, slotErrs...)
			view.Slots = slots
		}
	}
	return view, errs
}

func parsePageSlots(root string, pkg *model.Package, aliases map[string]string, expr ast.Expr) ([]model.ViewSlot, []string) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, []string{sourceDiagnostic(pkg, expr.Pos(), "page.Collection.Slots must be a static page.Component slice")}
	}
	var out []model.ViewSlot
	var errs []string
	for _, elt := range lit.Elts {
		call, ok := elt.(*ast.CallExpr)
		if !ok || !isPackageCall(call.Fun, aliases, "scenery.sh/page", "Component") || len(call.Args) != 1 {
			errs = append(errs, sourceDiagnostic(pkg, elt.Pos(), "page slots must use page.Component(\"Name\")"))
			continue
		}
		name, ok := staticStringValue(pkg, call.Args[0])
		if !ok {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), "page.Component requires a constant string argument"))
			continue
		}
		name = strings.TrimSpace(name)
		if name == "" {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), "page.Component requires a non-empty name"))
			continue
		}
		if !componentFileExists(root, name) {
			errs = append(errs, sourceDiagnostic(pkg, call.Args[0].Pos(), fmt.Sprintf("page.Component(%q) did not resolve to a TypeScript component file", name)))
		}
		out = append(out, model.ViewSlot{Name: name})
	}
	return out, errs
}

func validateViews(app *model.App) []string {
	entities := make(map[string]bool)
	for _, entity := range app.Entities {
		entities[entity.Name] = true
	}
	var errs []string
	for _, view := range app.Views {
		if !entities[view.Entity] {
			errs = append(errs, sourceDiagnostic(view.Package, view.TokenPos, fmt.Sprintf("page %s references unknown model %s", view.Name, view.Entity)))
		}
	}
	return errs
}

func modelDirectiveTypeName(decl *ast.GenDecl) string {
	if decl == nil || len(decl.Specs) != 1 {
		return ""
	}
	spec, ok := decl.Specs[0].(*ast.TypeSpec)
	if !ok || spec.Name == nil {
		return ""
	}
	return spec.Name.Name
}

func entityConfigKey(importPath, typeName string) string {
	return importPath + ":" + typeName
}

func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	if file == nil {
		return aliases
	}
	for _, imp := range file.Imports {
		importPath := strings.Trim(imp.Path.Value, "\"")
		alias := filepath.Base(importPath)
		if imp.Name != nil {
			if imp.Name.Name == "." || imp.Name.Name == "_" {
				continue
			}
			alias = imp.Name.Name
		}
		aliases[alias] = importPath
	}
	return aliases
}

func isPackageCall(expr ast.Expr, aliases map[string]string, importPath, name string) bool {
	sel, ok := selectorFromRuntimeCall(expr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	if !ok {
		return false
	}
	return aliases[ident.Name] == importPath
}

func firstTypeArg(fset *token.FileSet, expr ast.Expr) (string, bool) {
	switch v := expr.(type) {
	case *ast.IndexExpr:
		return shortTypeName(renderNode(fset, v.Index)), true
	case *ast.IndexListExpr:
		if len(v.Indices) != 1 {
			return "", false
		}
		return shortTypeName(renderNode(fset, v.Indices[0])), true
	default:
		return "", false
	}
}

func shortTypeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	value = strings.TrimPrefix(value, "*")
	if before, _, ok := strings.Cut(value, "["); ok {
		value = before
	}
	if strings.Contains(value, ".") {
		parts := strings.Split(value, ".")
		value = parts[len(parts)-1]
	}
	return strings.TrimSpace(value)
}

func isPageCollectionType(expr ast.Expr, aliases map[string]string) bool {
	return isPackageCall(expr, aliases, "scenery.sh/page", "Collection")
}

func literalStringSlice(pkg *model.Package, expr ast.Expr) ([]string, bool) {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	values := make([]string, 0, len(lit.Elts))
	for _, elt := range lit.Elts {
		value, ok := staticStringValue(pkg, elt)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func componentFileExists(root, name string) bool {
	want := map[string]bool{
		name + ".ts":  true,
		name + ".tsx": true,
	}
	found := false
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || found {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", ".scenery", "node_modules", "vendor":
				return filepath.SkipDir
			}
			return nil
		}
		if want[d.Name()] {
			found = true
		}
		return nil
	})
	return found
}

func fieldColumnName(field *ast.Field, fieldName string) string {
	if field != nil && field.Tag != nil {
		if tag, err := strconv.Unquote(field.Tag.Value); err == nil {
			if value := tagSetting(tag, "scenery", "column"); value != "" {
				return value
			}
			if value := firstTagValue(tag, "db"); value != "" {
				return value
			}
		}
	}
	return toSnake(fieldName)
}

func tagSetting(tag, key, setting string) string {
	value := firstTagValue(tag, key)
	if value == "" {
		return ""
	}
	for _, part := range strings.Split(value, ",") {
		name, val, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok && name == setting && val != "" {
			return val
		}
	}
	return ""
}

func firstTagValue(tag, key string) string {
	value := reflect.StructTag(tag).Get(key)
	if first, _, _ := strings.Cut(value, ","); first == "-" {
		return ""
	}
	return value
}

func staticStringValue(pkg *model.Package, expr ast.Expr) (string, bool) {
	if ident, ok := expr.(*ast.Ident); ok && pkg != nil && pkg.GoPkg != nil {
		if _, ok := pkg.GoPkg.TypesInfo.Uses[ident].(*types.Const); !ok {
			return "", false
		}
	}
	return literalStringValue(pkg, expr)
}

func defaultTableName(typeName string) string {
	return toSnake(typeName) + "s"
}

func toSnake(value string) string {
	var b strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
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
	if after, ok := strings.CutPrefix(text, "scenery:"); ok {
		return after, true
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
	for segment := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
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
			inputType, outputType := runtimeDeclarationTypeArgs(pkg, call)
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
				InputType:         inputType,
				OutputType:        outputType,
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
	case model.RuntimeDeclarationTemporalActivity, model.RuntimeDeclarationTemporalExternalActivity:
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
			if aliases[ident.Name] != "scenery.sh/temporal" {
				return true
			}
			switch sel.Sel.Name {
			case "NewActivity", "NewExternalActivity":
				if len(call.Args) > 1 && temporalActivityConfigHasEmptyTaskQueue(pkg, call.Args[1], aliases) {
					errs = append(errs, sourceDiagnostic(pkg, call.Lparen, "temporal."+sel.Sel.Name+" requires temporal.ActivityConfig.TaskQueue"))
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
	return aliases[ident.Name] == "scenery.sh/temporal"
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
		case "scenery.sh/temporal":
			defaultAlias = "temporal"
		case "scenery.sh/cron":
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
	case "scenery.sh/temporal":
		switch callName {
		case "NewWorkflow":
			return model.RuntimeDeclarationTemporalWorkflow, 0, true
		case "NewActivity":
			return model.RuntimeDeclarationTemporalActivity, 0, true
		case "NewExternalActivity":
			return model.RuntimeDeclarationTemporalExternalActivity, 0, true
		}
	case "scenery.sh/cron":
		if callName == "NewJob" {
			return model.RuntimeDeclarationCronJob, 0, true
		}
	}
	return "", 0, false
}

func runtimeDeclarationTypeArgs(pkg *model.Package, call *ast.CallExpr) (string, string) {
	if pkg == nil || pkg.GoPkg == nil || call == nil {
		return "", ""
	}
	switch fun := call.Fun.(type) {
	case *ast.IndexListExpr:
		if len(fun.Indices) >= 2 {
			return renderNode(pkg.GoPkg.Fset, fun.Indices[0]), renderNode(pkg.GoPkg.Fset, fun.Indices[1])
		}
	case *ast.IndexExpr:
		return renderNode(pkg.GoPkg.Fset, fun.Index), ""
	case *ast.SelectorExpr:
		return "", ""
	}
	return "", ""
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
	return isNamedType(t, "scenery.sh/auth", "UID")
}

func isMiddlewareNamedType(t types.Type, name string) bool {
	return isNamedType(t, "scenery.sh/middleware", name)
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
