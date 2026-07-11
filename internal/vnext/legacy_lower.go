package vnext

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
	"scenery.sh/internal/standardauthmeta"
)

func lowerLegacyResources(root, applicationName string, migration *Migration, declaredResources []Resource) ([]Resource, []Diagnostic) {
	if migration == nil {
		return nil, nil
	}
	cfg := appcfg.Config{Name: applicationName}
	if migration.LegacyConfig != "" {
		_, discovered, err := appcfg.DiscoverRoot(root)
		if err != nil {
			return nil, []Diagnostic{{Code: "SCN5201", Severity: "error", Message: "load legacy config: " + err.Error()}}
		}
		cfg = discovered
	}
	gateway := migration.defaultLegacyGatewayAddress()
	targetServices := map[string][]MigrationService{}
	for _, service := range migration.Services {
		if service.State != "native" {
			targetServices[service.LegacyTarget] = append(targetServices[service.LegacyTarget], service)
		}
	}
	inventory := map[string]MigrationService{}
	for _, service := range migration.Services {
		inventory[service.Name] = service
	}
	resources := lowerLegacySharedResources(cfg, gateway)
	var diagnostics []Diagnostic
	seenServices := map[string]bool{}
	targetNames := make([]string, 0, len(targetServices))
	for name := range targetServices {
		targetNames = append(targetNames, name)
	}
	sort.Strings(targetNames)
	for _, targetReference := range targetNames {
		services := targetServices[targetReference]
		target, err := ResolveGoBuildTarget(&Result{Root: root, Manifest: &Manifest{Resources: declaredResources}}, strings.TrimPrefix(targetReference, "go_target."), "")
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5202", Severity: "error", Message: "resolve legacy Go target " + targetReference + ": " + err.Error()})
			continue
		}
		packageRoots := make([]string, 0, len(services))
		for _, service := range services {
			packageRoots = append(packageRoots, service.Package)
		}
		legacy, err := parse.AppPackagesWithTarget(root, cfg.Name, packageRoots, target.Context)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5202", Severity: "error", Message: "lower legacy frontend for " + targetReference + ": " + err.Error()})
			continue
		}
		for _, service := range legacy.Services {
			seenServices[service.Name] = true
			state, ok := inventory[service.Name]
			if !ok {
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN5203", Severity: "error", Message: "legacy service " + service.Name + " is not listed in scenery.migration.scn"})
				continue
			}
			resources = append(resources, lowerLegacyService(service, state, gateway)...)
			resources = append(resources, lowerLegacySupplemental(legacy, service, state)...)
		}
	}
	for _, service := range migration.Services {
		if service.State != "native" && !seenServices[service.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5204", Severity: "warning", Message: "migration inventory service " + service.Name + " was not discovered by the legacy frontend"})
		}
	}
	return resources, diagnostics
}

func lowerLegacySharedResources(config appcfg.Config, gateway string) []Resource {
	if !config.Auth.Enabled {
		return nil
	}
	module := "app"
	meta := (*MigrationMeta)(nil)
	origin := legacyResourceOrigin(module, "standard_auth", "legacy config standard auth")
	endpoints := standardauthmeta.Endpoints(config.Auth.GoogleOAuth.Enabled)
	resources := make([]Resource, 0, len(endpoints)*3+2)
	seenServices := map[string]bool{}
	for _, endpoint := range endpoints {
		if seenServices[endpoint.Service] {
			continue
		}
		seenServices[endpoint.Service] = true
		serviceOrigin := origin
		serviceOrigin.LegacySymbol = endpoint.Service
		resources = append(resources, legacyResource(module, "service", endpoint.Service, map[string]any{"runtime": "go", "implementation": map[string]any{"adapter": "legacy_standard_auth_v0"}}, serviceOrigin, meta, "legacy_exact", "advisory"))
	}
	for _, endpoint := range endpoints {
		name := snakeName(endpoint.Name)
		operationAddress := resourceAddress(module, "operation", name)
		executionName := name + "_direct"
		endpointOrigin := legacyResourceOrigin(module, endpoint.Name, "standard auth endpoint")
		endpointOrigin.LegacyIdentity = map[string]any{
			"service": endpoint.Service, "path": endpoint.Path, "methods": append([]string(nil), endpoint.Methods...), "access": string(endpoint.Access),
			"file": "scenery.sh/auth", "has_payload": endpoint.HasPayload,
		}
		resources = append(resources,
			legacyResource(module, "operation", name, map[string]any{"service": map[string]any{"$ref": resourceAddress(module, "service", endpoint.Service)}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": endpoint.Name, "adapter": "legacy_standard_auth_v0"}}, endpointOrigin, meta, "legacy_exact", "advisory"),
			legacyResource(module, "execution", executionName, map[string]any{"operation": map[string]any{"$ref": operationAddress}, "mode": "direct", "timeout": "30s"}, endpointOrigin, meta, "legacy_exact", "advisory"),
		)
		for index, method := range endpoint.Methods {
			bindingName := name + "_http"
			if len(endpoint.Methods) > 1 {
				bindingName += fmt.Sprintf("_%d", index+1)
			}
			authentication, authorization := "std.authentication.none", "std.authorization.public"
			if string(endpoint.Access) == "auth" {
				authentication, authorization = "authentication.standard", "authorization.member"
			}
			contract := "advisory"
			if endpoint.Raw {
				contract = "opaque"
			}
			resources = append(resources, legacyResource(module, "binding", bindingName, map[string]any{
				"gateway": map[string]any{"$ref": gateway}, "operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName},
				"protocol": "http", "delivery": "call", "exposure": "internet", "authentication": map[string]any{"$ref": authentication}, "authorization": map[string]any{"$ref": authorization}, "pipeline": map[string]any{"$ref": "pipeline.http_default"},
				"http": map[string]any{"method": method, "path": legacyPathToNative(endpoint.Path), "codec_profile": "scenery.legacy-json/v0", "guarantee": contract},
			}, endpointOrigin, meta, "legacy_exact", contract))
		}
	}
	return resources
}

func lowerLegacySupplemental(appModel *model.App, service *model.Service, state MigrationService) []Resource {
	meta := &MigrationMeta{State: state.State, Active: state.Active}
	module := migrationServiceNamespace(state)
	var resources []Resource
	for _, middleware := range appModel.Middleware {
		if !middleware.Global || !legacyPackageOwnedByService(middleware.Package, service) {
			continue
		}
		name := "global_" + snakeName(middleware.Name)
		origin := legacyResourceOrigin(module, middleware.Name, "scenery:middleware global")
		resources = append(resources, legacyResource(module, "middleware", name, map[string]any{"protocols": []any{"http"}, "phases": []any{"legacy_global"}, "effects": []any{"opaque"}}, origin, meta, "legacy_exact", "advisory"))
	}
	resources = append(resources, lowerLegacyRuntimeDeclarations(appModel, service, module, meta)...)
	resources = append(resources, lowerLegacyEntities(appModel, service, module, meta)...)
	resources = append(resources, lowerLegacyViews(appModel, service, module, meta)...)
	return resources
}

func lowerLegacyRuntimeDeclarations(appModel *model.App, service *model.Service, module string, meta *MigrationMeta) []Resource {
	var resources []Resource
	engineAdded := false
	for _, declaration := range appModel.Runtime {
		if declaration.ServiceName != "" && declaration.ServiceName != service.Name || declaration.ServiceName == "" && !legacyPackageOwnedByService(declaration.Package, service) {
			continue
		}
		name := snakeName(declaration.Name)
		if name == "" {
			name = snakeName(declaration.CallName)
		}
		origin := legacyResourceOrigin(module, declaration.CallName, string(declaration.Kind))
		origin.LegacyIdentity = map[string]any{"input": declaration.InputType, "output": declaration.OutputType}
		if declaration.Package != nil && declaration.Package.Analysis != nil && declaration.Package.Analysis.Fset != nil {
			position := declaration.Package.Analysis.Fset.Position(declaration.TokenPos)
			file := filepath.Base(position.Filename)
			if relative, err := filepath.Rel(appModel.Root, position.Filename); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				file = filepath.ToSlash(relative)
			}
			origin.LegacyIdentity["file"] = file
			origin.LegacyIdentity["line"] = position.Line
		}
		switch declaration.Kind {
		case model.RuntimeDeclarationDurableTask:
			if !engineAdded {
				resources = append(resources, legacyResource(module, "execution_engine", "legacy_durable", map[string]any{"provider": "scenery.legacy.v0", "lifecycle": "application", "config": map[string]any{"task_queue": declaration.TaskQueue}}, origin, meta, "legacy_exact", "advisory"))
				engineAdded = true
			}
			operationName := "durable_" + name
			operationAddress := resourceAddress(module, "operation", operationName)
			operation := map[string]any{"service": map[string]any{"$ref": resourceAddress(module, "service", service.Name)}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": declaration.CallName, "adapter": "legacy_go_v0"}}
			if declaration.OutputType != "" {
				operation["result"] = map[string]any{"name": "success", "type": map[string]any{"$ref": "legacy.type.advisory"}}
			}
			resources = append(resources,
				legacyResource(module, "operation", operationName, operation, origin, meta, "legacy_exact", "advisory"),
				legacyResource(module, "execution", operationName, map[string]any{
					"operation": map[string]any{"$ref": operationAddress}, "mode": "durable", "engine": map[string]any{"$ref": "execution_engine.legacy_durable"}, "revision": 1,
					"timeout": "24h", "lease": "30s", "attempts": 1, "retry": map[string]any{"backoff": "fixed", "initial": "1s", "maximum": "1s", "jitter": "none"}, "retention": map[string]any{"success": "24h", "failure": "168h"}, "external_name": declaration.Name,
				}, origin, meta, "legacy_exact", "advisory"),
			)
		case model.RuntimeDeclarationCronJob:
			operationName := "cron_" + name
			operationAddress := resourceAddress(module, "operation", operationName)
			executionName := operationName + "_direct"
			resources = append(resources,
				legacyResource(module, "operation", operationName, map[string]any{"service": map[string]any{"$ref": resourceAddress(module, "service", service.Name)}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": declaration.CallName, "adapter": "legacy_go_v0"}}, origin, meta, "legacy_exact", "opaque"),
				legacyResource(module, "execution", executionName, map[string]any{"operation": map[string]any{"$ref": operationAddress}, "mode": "direct", "timeout": "24h"}, origin, meta, "legacy_exact", "opaque"),
				legacyResource(module, "schedule", name, map[string]any{
					"trigger": map[string]any{"calendar": "legacy-v0:" + declaration.Name},
					"invoke":  map[string]any{"operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName}, "identity": map[string]any{"$ref": "std.authentication.service_identity"}, "authorization": map[string]any{"$ref": "std.authorization.legacy_v0"}, "pipeline": map[string]any{"$ref": "std.pipeline.legacy_v0"}, "input": map[string]any{"legacy": true}},
					"overlap": "legacy",
				}, origin, meta, "legacy_exact", "opaque"),
			)
		}
	}
	return resources
}

func lowerLegacyEntities(appModel *model.App, service *model.Service, module string, meta *MigrationMeta) []Resource {
	var entities []*model.Entity
	for _, entity := range appModel.Entities {
		if legacyPackageOwnedByService(entity.Package, service) {
			entities = append(entities, entity)
		}
	}
	if len(entities) == 0 {
		return nil
	}
	origin := legacyResourceOrigin(module, "database", "legacy shared database")
	resources := []Resource{
		legacyResource(module, "provider", "legacy_database", map[string]any{"source": "scenery.legacy.v0/database", "version": "1.0.0"}, origin, meta, "legacy_exact", "advisory"),
		legacyResource(module, "data_source", "legacy_database", map[string]any{"provider": map[string]any{"$ref": "provider.legacy_database"}, "lifecycle": "application", "config": map[string]any{}}, origin, meta, "legacy_exact", "advisory"),
	}
	for _, entity := range entities {
		name := snakeName(entity.Name)
		entityOrigin := legacyResourceOrigin(module, entity.Name, "scenery:model")
		var recordFields, mappings []any
		primarySelected := false
		for index, field := range entity.Fields {
			if !model.EntityFieldIsStored(field) {
				continue
			}
			typeExpression, complete := migrationCandidateType(field.Type)
			contract := "verified"
			if !complete {
				contract = "advisory"
			}
			_ = contract
			fieldName := snakeName(field.Name)
			recordFields = append(recordFields, map[string]any{"name": fieldName, "wire_name": field.Column, "type": legacyCandidateTypeValue(typeExpression)})
			primary := strings.EqualFold(field.Column, "id") || strings.EqualFold(field.Name, "id") || !primarySelected && index == 0
			if primary {
				primarySelected = true
			}
			mappings = append(mappings, map[string]any{"name": fieldName, "column": field.Column, "primary_key": primary, "tenant_key": strings.EqualFold(field.Column, "tenant_id"), "immutable": primary || strings.EqualFold(field.Column, "tenant_id")})
		}
		resources = append(resources,
			legacyResource(module, "record", name, map[string]any{"field": recordFields, "unknown_fields": "preserve"}, entityOrigin, meta, "legacy_exact", "advisory"),
			legacyResource(module, "entity", name, map[string]any{"type": map[string]any{"$ref": "record." + name}, "data_source": map[string]any{"$ref": "data_source.legacy_database"}, "mapping": map[string]any{"relation": entity.Table}, "field": mappings}, entityOrigin, meta, "legacy_exact", "advisory"),
		)
		if len(entity.CRUD.Actions) > 0 {
			actions := make([]any, 0, len(entity.CRUD.Actions))
			for _, action := range entity.CRUD.Actions {
				actions = append(actions, string(action))
			}
			resources = append(resources, legacyResource(module, "crud", name, map[string]any{"entity": map[string]any{"$ref": "entity." + name}, "implementation": map[string]any{"$ref": "data_source.legacy_database"}, "actions": actions, "execution": map[string]any{"mode": "direct"}}, entityOrigin, meta, "legacy_exact", "advisory"))
		}
		if len(entity.Seeds) > 0 {
			values := make([]any, 0, len(entity.Seeds))
			for _, seed := range entity.Seeds {
				row := map[string]any{}
				for _, value := range seed.Values {
					row[snakeName(value.Field)] = value.Value
				}
				values = append(values, row)
			}
			resources = append(resources, legacyResource(module, "fixture", name, map[string]any{"entity": map[string]any{"$ref": "entity." + name}, "environments": []any{"development", "test"}, "mode": "insert", "values": values}, entityOrigin, meta, "legacy_exact", "advisory"))
		}
	}
	return resources
}

func lowerLegacyViews(appModel *model.App, service *model.Service, module string, meta *MigrationMeta) []Resource {
	var resources []Resource
	for _, view := range appModel.Views {
		if !legacyPackageOwnedByService(view.Package, service) {
			continue
		}
		name := snakeName(view.Name)
		origin := legacyResourceOrigin(module, view.Name, "scenery:page")
		pageSpec := map[string]any{"path": view.Route, "load": "legacy.page.load"}
		resources = append(resources,
			legacyResource(module, "page", name, pageSpec, origin, meta, "legacy_exact", "advisory"),
			legacyResource(module, "renderer", name, map[string]any{"page": map[string]any{"$ref": "page." + name}, "runtime": "legacy_v0", "module": "legacy:" + view.Name, "config": map[string]any{"kind": view.Kind, "entity": view.Entity, "columns": append([]string(nil), view.Columns...), "title": view.Title, "slots": len(view.Slots)}}, origin, meta, "legacy_exact", "advisory"),
		)
	}
	return resources
}

func legacyPackageOwnedByService(pkg *model.Package, service *model.Service) bool {
	if pkg == nil || service == nil {
		return false
	}
	if pkg.Service != nil {
		return pkg.Service.Name == service.Name
	}
	relative := filepath.ToSlash(filepath.Clean(pkg.RelDir))
	root := filepath.ToSlash(filepath.Clean(service.RootRelDir))
	return relative == root || strings.HasPrefix(relative, root+"/")
}

func validateNativeOnlyLegacyAbsence(root string) []Diagnostic {
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return nil
	}
	legacy, err := parse.App(root, cfg.Name)
	if err != nil {
		return []Diagnostic{{Code: "SCN5202", Severity: "error", Message: "inspect native-only legacy frontend: " + err.Error()}}
	}
	var owners []string
	for _, service := range legacy.Services {
		if len(service.Endpoints) > 0 || len(service.Generated) > 0 || service.AuthHandler != nil || len(service.Middleware) > 0 {
			owners = append(owners, service.Name)
		}
	}
	if len(legacy.Runtime) > 0 {
		owners = append(owners, "runtime_declarations")
	}
	owners = canonicalStrings(owners)
	if len(owners) == 0 {
		return nil
	}
	return []Diagnostic{{Code: "SCN5207", Severity: "error", Message: "native-only project contains legacy ownership; add a bounded scenery.migration.scn inventory", Details: map[string]any{"owners": owners}}}
}

func validateNativeMigrationLegacyAbsence(root, applicationName string, migration *Migration, resources []Resource) []Diagnostic {
	if migration == nil {
		return nil
	}
	nativeServices := make([]MigrationService, 0, len(migration.Services))
	for _, service := range migration.Services {
		if service.State == "native" {
			nativeServices = append(nativeServices, service)
		}
	}
	if len(nativeServices) == 0 {
		return nil
	}
	cfg := appcfg.Config{Name: applicationName}
	if migration.LegacyConfig != "" {
		_, discovered, err := appcfg.DiscoverRoot(root)
		if err != nil {
			return []Diagnostic{{Code: "SCN5208", Severity: "error", Message: "inspect native migration ownership: " + err.Error()}}
		}
		cfg = discovered
	}
	target, err := ResolveGoBuildTarget(&Result{Root: root, Manifest: &Manifest{Resources: resources}}, "", "development")
	if err != nil {
		return []Diagnostic{{Code: "SCN5208", Severity: "error", Message: "inspect native migration ownership: " + err.Error()}}
	}
	moduleRoots := map[string]string{}
	for _, resource := range resources {
		if resource.Kind != "scenery.module/v1" {
			continue
		}
		source := stringValue(resource.Spec["workspace_package_root"])
		if source == "" {
			source = stringValue(resource.Spec["source"])
		}
		moduleRoots[moduleInstancePath(resource)] = source
	}
	var diagnostics []Diagnostic
	for _, service := range nativeServices {
		module := strings.TrimPrefix(service.Module, "module.")
		if module == "" {
			module = service.Name
		}
		packageRoot := moduleRoots[module]
		if packageRoot == "" {
			continue
		}
		legacy, err := parse.InspectPackagesWithTarget(root, cfg.Name, []string{packageRoot}, target.Context)
		if err != nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5208", Severity: "error", Message: "inspect native migration service " + service.Name + ": " + err.Error()})
			continue
		}
		constructs := make([]string, 0)
		if len(legacyRuntimeBuilderReferences(legacy)) > 0 {
			constructs = append(constructs, "runtime_builder_references")
		}
		if len(legacy.Runtime) > 0 {
			constructs = append(constructs, "runtime_declarations")
		}
		if len(legacy.Entities) > 0 {
			constructs = append(constructs, "models")
		}
		if len(legacy.Views) > 0 {
			constructs = append(constructs, "pages")
		}
		constructs = canonicalStrings(constructs)
		if len(constructs) > 0 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5208", Severity: "error", Message: "native migration service " + service.Name + " still contains hidden legacy runtime ownership", Details: map[string]any{"constructs": constructs}})
		}
	}
	return diagnostics
}

func legacyRuntimeBuilderReferences(app *model.App) []string {
	var references []string
	if app == nil {
		return references
	}
	for _, pkg := range app.Packages {
		if pkg == nil || pkg.Analysis == nil || pkg.Analysis.TypesInfo == nil {
			continue
		}
		for _, object := range pkg.Analysis.TypesInfo.Uses {
			function, ok := object.(*types.Func)
			if !ok || function.Pkg() == nil {
				continue
			}
			path, name := function.Pkg().Path(), function.Name()
			if path == "scenery.sh/durable" && name == "NewTask" || path == "scenery.sh/cron" && name == "NewJob" {
				references = append(references, path+"."+name)
			}
		}
	}
	return canonicalStrings(references)
}

func nativeGoPackage(appModel *model.App, resources []Resource, module string) *model.Package {
	implementationImport := ""
	root := "."
	for _, resource := range resources {
		if resource.Kind != "scenery.module/v1" || moduleInstancePath(resource) != module {
			continue
		}
		if contractImport, ok := moduleContractImportPath(resources, module); ok {
			implementationImport = strings.TrimSuffix(contractImport, "/scenerycontract")
		}
		root = stringValue(resource.Spec["workspace_package_root"])
		if root == "" {
			root = stringValue(resource.Spec["source"])
		}
		break
	}
	root = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(root, "./")))
	for _, pkg := range appModel.Packages {
		if implementationImport != "" && pkg.ImportPath == implementationImport {
			return pkg
		}
		if filepath.ToSlash(filepath.Clean(pkg.RelDir)) == root {
			return pkg
		}
	}
	return nil
}

func nativeServiceNamedType(pkg *model.Package, constructor string) *types.Named {
	if pkg == nil || pkg.Analysis == nil {
		return nil
	}
	object := pkg.Analysis.Types.Scope().Lookup(constructor)
	signature, _ := objectTypeSignature(object)
	if signature == nil || signature.Results().Len() == 0 {
		return nil
	}
	pointer, _ := signature.Results().At(0).Type().(*types.Pointer)
	if pointer == nil {
		return nil
	}
	named, _ := pointer.Elem().(*types.Named)
	return named
}

func legacyBridgeServiceNamedType(pkg *model.Package) *types.Named {
	if pkg == nil || pkg.Analysis == nil || pkg.Service == nil || pkg.Service.Struct == nil {
		return nil
	}
	object := pkg.Analysis.Types.Scope().Lookup(pkg.Service.Struct.TypeName)
	if object == nil {
		return nil
	}
	named, _ := types.Unalias(object.Type()).(*types.Named)
	return named
}

func objectTypeSignature(object types.Object) (*types.Signature, bool) {
	if object == nil {
		return nil, false
	}
	signature, ok := object.Type().(*types.Signature)
	return signature, ok
}

func validateNativeGoServices(appModel *model.App, resources []Resource, migration *Migration) []Diagnostic {
	active := map[string]bool{}
	if migration != nil {
		for _, service := range migration.Services {
			active[service.Name] = service.Active == "native"
		}
	} else {
		for _, resource := range resources {
			if resource.Kind == "scenery.service/v1" && resource.Origin.Kind == "authored" {
				active[resource.Module] = true
			}
		}
	}
	var diagnostics []Diagnostic
	for _, resource := range resources {
		if resource.Kind != "scenery.service/v1" || !active[resource.Module] {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		if implementation == nil || implementation["adapter"] == "legacy_go_v0" {
			continue
		}
		constructor, _ := implementation["constructor"].(string)
		if constructor == "" || !token.IsExported(constructor) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6110", Severity: "error", Message: "native Go service requires an exported constructor", Address: resource.Address})
			continue
		}
		pkg := nativeGoPackage(appModel, resources, resource.Module)
		if pkg == nil || pkg.Analysis == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6111", Severity: "error", Message: "native Go service package not found", Address: resource.Address})
			continue
		}
		object := pkg.Analysis.Types.Scope().Lookup(constructor)
		if object == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6112", Severity: "error", Message: "native constructor " + constructor + " not found", Address: resource.Address})
			continue
		}
		signature, ok := object.Type().(*types.Signature)
		if !ok || signature.Params().Len() != 2 || signature.Results().Len() != 2 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6113", Severity: "error", Message: "native constructor must accept context and generated constructor input and return service pointer plus error", Address: resource.Address})
			continue
		}
		input := types.TypeString(signature.Params().At(1).Type(), packageQualifier)
		want := goName(resource.Name) + "ConstructorInput"
		if !strings.HasSuffix(input, "/scenerycontract."+want) || types.TypeString(signature.Results().At(1).Type(), packageQualifier) != "error" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6113", Severity: "error", Message: "native constructor signature does not match generated " + want, Address: resource.Address})
		}
		named := nativeServiceNamedType(pkg, constructor)
		if named == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6113", Severity: "error", Message: "native constructor must return a named service pointer", Address: resource.Address})
			continue
		}
		diagnostics = append(diagnostics, validateLifecycleMethods(resource, named)...)
	}
	return diagnostics
}

func validateLifecycleMethods(resource Resource, named *types.Named) []Diagnostic {
	lifecycle, _ := resource.Spec["lifecycle"].(map[string]any)
	if lifecycle == nil || named == nil {
		return nil
	}
	var diagnostics []Diagnostic
	for _, phase := range []string{"start", "stop"} {
		method, _ := lifecycle[phase].(string)
		if method == "" {
			continue
		}
		selection := types.NewMethodSet(types.NewPointer(named)).Lookup(nil, method)
		if selection == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6114", Severity: "error", Message: "lifecycle method " + method + " not found", Address: resource.Address})
			continue
		}
		signature, ok := selection.Obj().Type().(*types.Signature)
		if !ok || signature.Params().Len() != 1 || signature.Results().Len() != 1 || types.TypeString(signature.Results().At(0).Type(), packageQualifier) != "error" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6115", Severity: "error", Message: "lifecycle method " + method + " must accept context and return error", Address: resource.Address})
		}
	}
	return diagnostics
}

func validateNativeGoHandlers(appModel *model.App, resources []Resource, migration *Migration) []Diagnostic {
	activeNative := map[string]bool{}
	if migration != nil {
		for _, service := range migration.Services {
			if service.Active == "native" {
				activeNative[service.Name] = true
			}
		}
	} else {
		for _, resource := range resources {
			if resource.Kind == "scenery.service/v1" && resource.Origin.Kind == "authored" {
				activeNative[resource.Module] = true
			}
		}
	}
	var diagnostics []Diagnostic
	for _, operation := range resources {
		if operation.Kind != "scenery.operation/v1" || !activeNative[operation.Module] {
			continue
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		if handler == nil {
			continue
		}
		if adapter, _ := handler["adapter"].(string); adapter == "legacy_go_v0" {
			continue
		}
		methodName, _ := handler["method"].(string)
		if methodName == "" {
			continue
		}
		var serviceResource Resource
		for _, candidate := range resources {
			if candidate.Kind == "scenery.service/v1" && candidate.Module == operation.Module && candidate.Origin.Kind != "legacy_v0" {
				serviceResource = candidate
				break
			}
		}
		implementation, _ := serviceResource.Spec["implementation"].(map[string]any)
		constructor := stringValue(implementation["constructor"])
		pkg := nativeGoPackage(appModel, resources, operation.Module)
		named := nativeServiceNamedType(pkg, constructor)
		if stringValue(implementation["adapter"]) == "legacy_go_v0" {
			named = legacyBridgeServiceNamedType(pkg)
		}
		if pkg == nil || named == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6101", Severity: "error", Message: "native operation " + operation.Address + " has no Go service type", Address: operation.Address})
			continue
		}
		selection := types.NewMethodSet(types.NewPointer(named)).Lookup(nil, methodName)
		if selection == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6103", Severity: "error", Message: "native handler method " + methodName + " not found", Address: operation.Address})
			continue
		}
		signature, ok := selection.Obj().Type().(*types.Signature)
		if !ok || signature.Params().Len() != 2 || signature.Results().Len() != 2 {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6104", Severity: "error", Message: "native handler " + methodName + " must accept context and generated input and return generated outcome plus error", Address: operation.Address})
			continue
		}
		inputWant := goName(operation.Name) + "Input"
		outcomeWant := goName(operation.Name) + "Outcome"
		inputGot := types.TypeString(signature.Params().At(1).Type(), packageQualifier)
		outcomeGot := types.TypeString(signature.Results().At(0).Type(), packageQualifier)
		errorGot := types.TypeString(signature.Results().At(1).Type(), packageQualifier)
		if !strings.HasSuffix(inputGot, "/scenerycontract."+inputWant) || !strings.HasSuffix(outcomeGot, "/scenerycontract."+outcomeWant) || errorGot != "error" {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6105", Severity: "error", Message: fmt.Sprintf("native handler %s has (%s) (%s, %s), want scenerycontract.%s -> scenerycontract.%s, error", methodName, inputGot, outcomeGot, errorGot, inputWant, outcomeWant), Address: operation.Address})
		}
	}
	return diagnostics
}

func packageQualifier(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}

func lowerLegacyService(service *model.Service, state MigrationService, gateway string) []Resource {
	meta := &MigrationMeta{State: state.State, Active: state.Active}
	module := migrationServiceNamespace(state)
	serviceOrigin := legacyResourceOrigin(module, service.Name, "scenery:service")
	serviceSpec := map[string]any{"runtime": "go", "implementation": map[string]any{"adapter": "legacy_go_v0", "root": service.RootRelDir}}
	if service.Struct != nil {
		lifecycle := map[string]any{}
		if service.Struct.InitFunc != "" {
			lifecycle["start"] = service.Struct.InitFunc
		}
		if service.Struct.Shutdown != "" {
			lifecycle["stop"] = service.Struct.Shutdown
		}
		if len(lifecycle) > 0 {
			serviceSpec["lifecycle"] = lifecycle
		}
	}
	resources := []Resource{legacyResource(module, "service", service.Name, serviceSpec, serviceOrigin, meta, "legacy_exact", "verified")}
	typed, candidateDiagnostics := migrationCandidateOperations(service)
	incompleteOperations := map[string]bool{}
	for _, diagnostic := range candidateDiagnostics {
		if diagnostic.Code == "SCN5402" || diagnostic.Code == "SCN5404" {
			incompleteOperations[diagnostic.Address] = true
		}
	}
	for _, operation := range typed {
		contract := "verified"
		if incompleteOperations[resourceAddress(service.Name, "operation", operation.Name)] || len(service.Middleware) > 0 {
			contract = "advisory"
		}
		resources = append(resources, lowerLegacyTypedOperation(module, service.Name, operation, meta, contract, gateway)...)
	}
	for _, endpoint := range service.Endpoints {
		if !endpoint.Raw && !strings.Contains(endpoint.Path, "*") {
			continue
		}
		origin := legacyResourceOrigin(module, endpoint.Name, "scenery:api raw")
		origin.LegacyIdentity = legacyEndpointIdentity(endpoint)
		resources = append(resources, lowerLegacyEndpoint(module, service.Name, endpoint.Name, endpoint.Path, endpoint.Methods, string(endpoint.Access), true, origin, meta, gateway)...)
	}
	for _, endpoint := range service.Generated {
		origin := legacyResourceOrigin(module, endpoint.Name, "generated model endpoint")
		origin.LegacyIdentity = map[string]any{
			"path": endpoint.Path, "methods": append([]string(nil), endpoint.Methods...), "access": string(endpoint.Access),
			"has_payload": endpoint.HasPayload, "generated": endpoint.Generated,
		}
		if endpoint.Package != nil && endpoint.Entity != nil {
			origin.LegacyIdentity["file"] = legacyModelFile(endpoint.Package, endpoint.Entity.File)
		}
		resources = append(resources, lowerLegacyEndpoint(module, service.Name, endpoint.Name, endpoint.Path, endpoint.Methods, string(endpoint.Access), false, origin, meta, gateway)...)
	}
	if service.AuthHandler != nil {
		origin := legacyResourceOrigin(module, service.AuthHandler.Name, "scenery:authhandler")
		resources = append(resources, legacyResource(module, "authentication", "legacy_auth", map[string]any{"provider": "scenery.legacy.v0", "scheme": "legacy_authhandler", "config": map[string]any{"handler": service.AuthHandler.Name}}, origin, meta, "legacy_exact", "verified"))
	}
	for _, middleware := range service.Middleware {
		name := snakeName(middleware.Name)
		origin := legacyResourceOrigin(module, middleware.Name, "scenery:middleware")
		resources = append(resources, legacyResource(module, "middleware", name, map[string]any{
			"protocols": []any{"http"}, "phases": []any{"legacy"}, "effects": []any{"opaque"},
		}, origin, meta, "legacy_exact", "advisory"))
	}
	return resources
}

func lowerLegacyTypedOperation(module, service string, operation migrationCandidateOperation, meta *MigrationMeta, contract, gateway string) []Resource {
	origin := legacyResourceOrigin(module, operation.Method, "scenery:api")
	origin.LegacyIdentity = map[string]any{
		"path": operation.LegacyPath, "methods": append([]string(nil), operation.Methods...), "access": operation.Access,
		"file": operation.File, "receiver": operation.Receiver, "tags": append([]string(nil), operation.Tags...), "has_payload": operation.HasPayload,
	}
	inputName, resultName := operation.Name+"_input", operation.Name+"_result"
	inputFields := make([]any, 0, len(operation.Input))
	for _, field := range operation.Input {
		inputFields = append(inputFields, legacyCandidateFieldSpec(field))
	}
	resultFields := make([]any, 0, len(operation.Output))
	for _, field := range operation.Output {
		resultFields = append(resultFields, legacyCandidateFieldSpec(field))
	}
	operationAddress := resourceAddress(module, "operation", operation.Name)
	executionName := operation.Name + "_direct"
	resources := []Resource{
		legacyResource(module, "record", inputName, map[string]any{"field": inputFields, "unknown_fields": "preserve"}, origin, meta, "legacy_exact", contract),
		legacyResource(module, "record", resultName, map[string]any{"field": resultFields, "unknown_fields": "preserve"}, origin, meta, "legacy_exact", contract),
		legacyResource(module, "operation", operation.Name, map[string]any{
			"service": map[string]any{"$ref": resourceAddress(module, "service", service)},
			"input":   map[string]any{"$ref": "record." + inputName},
			"handler": map[string]any{"method": operation.Method, "adapter": "legacy_go_v0"},
			"result":  map[string]any{"name": "success", "type": map[string]any{"$ref": "record." + resultName}},
			"error":   map[string]any{"name": "legacy_error", "type": map[string]any{"$ref": "std.type.problem"}},
		}, origin, meta, "legacy_exact", contract),
		legacyResource(module, "execution", executionName, map[string]any{"operation": map[string]any{"$ref": operationAddress}, "mode": "direct", "timeout": "30s"}, origin, meta, "legacy_exact", contract),
	}
	if operation.Access == "private" {
		resources = append(resources, legacyResource(module, "binding", operation.Name+"_internal", map[string]any{
			"operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName},
			"protocol": "internal", "delivery": "call", "exposure": "local",
			"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.legacy_v0"}, "pipeline": map[string]any{"$ref": "std.pipeline.legacy_v0"},
			"internal": map[string]any{"visibility": "package", "principal": "inherit"},
		}, origin, meta, "legacy_exact", contract))
		return resources
	}
	for index, method := range operation.Methods {
		bindingName := operation.Name + "_http"
		if len(operation.Methods) > 1 {
			bindingName += fmt.Sprintf("_%d", index+1)
		}
		resources = append(resources, legacyResource(module, "binding", bindingName, legacyHTTPBindingSpec(operation, operationAddress, executionName, method, gateway), origin, meta, "legacy_exact", contract))
	}
	return resources
}

func legacyHTTPBindingSpec(operation migrationCandidateOperation, operationAddress, executionName, method, gateway string) map[string]any {
	authentication, authorization := "std.authentication.none", "std.authorization.public"
	if operation.Access == "auth" {
		authentication, authorization = "std.authentication.legacy_v0", "std.authorization.legacy_v0"
	}
	httpSpec := map[string]any{"method": strings.ToUpper(method), "path": operation.Path, "codec_profile": map[string]any{"$ref": "std.codec.http_json_v1"}, "guarantee": "advisory"}
	var bodyFields []any
	for _, field := range operation.Input {
		mapping := map[string]any{"name": field.SourceName, "to": map[string]any{"$ref": "operation." + operation.Name + ".input." + field.Name}}
		switch field.Source {
		case "path":
			httpSpec["path_parameter"] = appendNamedValue(httpSpec["path_parameter"], mapping)
		case "query":
			httpSpec["query_parameter"] = appendNamedValue(httpSpec["query_parameter"], mapping)
		case "header":
			httpSpec["header"] = appendNamedValue(httpSpec["header"], mapping)
		case "cookie":
			httpSpec["cookie"] = appendNamedValue(httpSpec["cookie"], mapping)
		default:
			bodyFields = append(bodyFields, map[string]any{"$ref": "operation." + operation.Name + ".input." + field.Name})
		}
	}
	if len(bodyFields) > 0 {
		body := map[string]any{"codec": "json", "to": map[string]any{"$ref": "operation." + operation.Name + ".input"}}
		if len(bodyFields) != len(operation.Input) {
			body["include"] = bodyFields
		}
		httpSpec["body"] = body
	}
	success := map[string]any{"name": "success", "when": map[string]any{"$ref": "result.success"}, "status": 204}
	if operation.HasOutput {
		success["status"] = 200
		success["body"] = map[string]any{"codec": "json", "from": map[string]any{"$ref": "result.success"}}
	}
	failure := map[string]any{"name": "legacy_error", "when": map[string]any{"$ref": "error.legacy_error"}, "status": 500, "body": map[string]any{"codec": "problem_json", "from": map[string]any{"$ref": "error.legacy_error"}}}
	httpSpec["response"] = []any{success, failure}
	return map[string]any{
		"gateway": map[string]any{"$ref": gateway}, "operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName},
		"protocol": "http", "delivery": "call", "exposure": "internet", "authentication": map[string]any{"$ref": authentication}, "authorization": map[string]any{"$ref": authorization}, "pipeline": map[string]any{"$ref": "std.pipeline.legacy_v0"}, "http": httpSpec,
	}
}

func appendNamedValue(current any, value map[string]any) any {
	if current == nil {
		return value
	}
	if values, ok := current.([]any); ok {
		return append(values, value)
	}
	return []any{current, value}
}

func legacyCandidateFieldSpec(field migrationCandidateField) map[string]any {
	spec := map[string]any{"name": field.Name, "type": legacyCandidateTypeValue(field.Type)}
	if field.WireName != "" && field.WireName != field.Name {
		spec["wire_name"] = field.WireName
	}
	return spec
}

func legacyCandidateTypeValue(value string) any {
	if strings.Contains(value, "(") {
		return map[string]any{"$expression": value}
	}
	return map[string]any{"$ref": value}
}

func legacyResource(module, kind, name string, spec map[string]any, origin Origin, meta *MigrationMeta, semantics, contract string) Resource {
	// Static lowering can verify shape, but only executed behavioral fixtures
	// can establish exact migration equivalence.
	if semantics == "legacy_exact" {
		semantics = "advisory"
	}
	disposition := "advisory"
	if contract == "opaque" {
		disposition = "opaque"
	} else if contract == "unsupported" {
		disposition = "unsupported"
	}
	return Resource{
		Address: resourceAddress(module, kind, name), Kind: "scenery." + strings.ReplaceAll(kind, "_", "-") + "/v1", Name: name, Module: module, Spec: spec, Origin: origin, Migration: meta,
		Compatibility: &LegacyCompatibility{Semantics: semantics, Contract: contract, MigrationDisposition: disposition},
	}
}

func legacyResourceOrigin(module, symbol, construct string) Origin {
	return Origin{Kind: "legacy_v0", Frontend: "scenery.legacy.v0", SourceID: "src_legacy_" + shortStableID(module+"\x00"+symbol+"\x00"+construct), LegacySymbol: symbol, LegacyConstruct: construct}
}

func legacyEndpointIdentity(endpoint *model.Endpoint) map[string]any {
	identity := map[string]any{
		"path": endpoint.Path, "methods": append([]string(nil), endpoint.Methods...), "access": string(endpoint.Access),
		"file": legacyModelFile(endpoint.Package, endpoint.File), "tags": append([]string(nil), endpoint.Tags...), "has_payload": endpoint.Payload != nil,
	}
	if endpoint.Receiver != nil {
		identity["receiver"] = endpoint.Receiver.TypeName
	}
	return identity
}

func legacyModelFile(pkg *model.Package, file *model.File) string {
	if file == nil {
		return ""
	}
	name := filepath.Base(file.Path)
	if pkg == nil || pkg.RelDir == "" || pkg.RelDir == "." {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(pkg.RelDir, name))
}

func shortStableID(value string) string {
	digest := strings.TrimPrefix(revisionHash("scenery.legacy-source.v1\x00", value), "sha256:")
	if len(digest) > 16 {
		return digest[:16]
	}
	return digest
}

func lowerLegacyEndpoint(module, service, name, path string, methods []string, access string, raw bool, origin Origin, meta *MigrationMeta, gateway string) []Resource {
	semantic := snakeName(name)
	operationAddress := resourceAddress(module, "operation", semantic)
	executionName := semantic + "_direct"
	resources := []Resource{
		legacyResource(module, "operation", semantic, map[string]any{"service": map[string]any{"$ref": resourceAddress(module, "service", service)}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": name, "adapter": "legacy_go_v0"}}, origin, meta, "legacy_exact", map[bool]string{true: "opaque", false: "advisory"}[raw]),
		legacyResource(module, "execution", executionName, map[string]any{"operation": map[string]any{"$ref": operationAddress}, "mode": "direct", "timeout": "30s"}, origin, meta, "legacy_exact", "advisory"),
	}
	if access == "private" {
		resources = append(resources, legacyResource(module, "binding", semantic+"_internal", map[string]any{
			"operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName}, "protocol": "internal", "delivery": "call", "exposure": "local",
			"authentication": map[string]any{"$ref": "std.authentication.inherit"}, "authorization": map[string]any{"$ref": "std.authorization.legacy_v0"}, "pipeline": map[string]any{"$ref": "std.pipeline.legacy_v0"}, "internal": map[string]any{"visibility": "package", "principal": "inherit"},
		}, origin, meta, "legacy_exact", "advisory"))
		return resources
	}
	for i, method := range methods {
		bindingName := semantic + "_http"
		if len(methods) > 1 {
			bindingName += fmt.Sprintf("_%d", i+1)
		}
		resources = append(resources, legacyResource(module, "binding", bindingName, map[string]any{"gateway": map[string]any{"$ref": gateway}, "operation": map[string]any{"$ref": operationAddress}, "execution": map[string]any{"$ref": "execution." + executionName}, "protocol": "http", "delivery": "call", "exposure": "internet", "authentication": access, "authorization": map[string]any{"$ref": "std.authorization.legacy_v0"}, "pipeline": map[string]any{"$ref": "std.pipeline.legacy_v0"}, "http": map[string]any{"method": method, "path": legacyPathToNative(path), "codec_profile": map[bool]string{true: "scenery.legacy-raw/v0", false: "scenery.legacy-json/v0"}[raw], "guarantee": map[bool]string{true: "opaque", false: "advisory"}[raw]}}, origin, meta, "legacy_exact", map[bool]string{true: "opaque", false: "advisory"}[raw]))
	}
	return resources
}

func migrationServiceNamespace(service MigrationService) string {
	if service.Namespace != "" {
		return service.Namespace
	}
	return service.Name
}

func legacyPathToNative(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") {
			parts[i] = "{" + snakeName(strings.TrimPrefix(part, ":")) + "}"
		}
	}
	return strings.Join(parts, "/")
}
func snakeName(value string) string {
	// Keep conventional mixed-case initialisms together before applying the
	// general word-boundary rule. OAuth is the common spelling where an initial
	// one-letter capital would otherwise look like its own word.
	value = strings.ReplaceAll(value, "OAuth", "Oauth")
	runes := []rune(value)
	var b strings.Builder
	for i, r := range runes {
		if r >= 'A' && r <= 'Z' {
			previousLowerOrDigit := i > 0 && ((runes[i-1] >= 'a' && runes[i-1] <= 'z') || (runes[i-1] >= '0' && runes[i-1] <= '9'))
			acronymWordBoundary := i > 0 && runes[i-1] >= 'A' && runes[i-1] <= 'Z' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z'
			if b.Len() > 0 && (previousLowerOrDigit || acronymWordBoundary) {
				b.WriteByte('_')
			}
			b.WriteRune(r - 'A' + 'a')
		} else if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}
