package vnext

import (
	"fmt"
	"go/types"
	"sort"
	"strings"

	appcfg "scenery.sh/internal/app"
	"scenery.sh/internal/model"
	"scenery.sh/internal/parse"
)

func lowerLegacyResources(root string, migration *Migration, native []Resource) ([]Resource, []Diagnostic) {
	if migration == nil {
		return nil, nil
	}
	_, cfg, err := appcfg.DiscoverRoot(root)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN5201", Severity: "error", Message: "load legacy config: " + err.Error()}}
	}
	legacy, err := parse.App(root, cfg.Name)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN5202", Severity: "error", Message: "lower legacy frontend: " + err.Error()}}
	}
	inventory := map[string]MigrationService{}
	for _, service := range migration.Services {
		inventory[service.Name] = service
	}
	var resources []Resource
	var diagnostics []Diagnostic
	nativeRoutes := routesByModule(native)
	seenServices := map[string]bool{}
	for _, service := range legacy.Services {
		seenServices[service.Name] = true
		state, ok := inventory[service.Name]
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5203", Severity: "error", Message: "legacy service " + service.Name + " is not listed in scenery.migration.scn"})
			continue
		}
		if state.Active == "native" {
			diagnostics = append(diagnostics, compareNativeServiceRoutes(service, nativeRoutes[service.Name])...)
			continue
		}
		resources = append(resources, lowerLegacyService(service, state)...)
	}
	for _, service := range migration.Services {
		if !seenServices[service.Name] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5204", Severity: "warning", Message: "migration inventory service " + service.Name + " was not discovered by the legacy frontend"})
		}
	}
	diagnostics = append(diagnostics, validateNativeGoHandlers(legacy, native, migration)...)
	return resources, diagnostics
}

func validateNativeGoHandlers(appModel *model.App, resources []Resource, migration *Migration) []Diagnostic {
	activeNative := map[string]bool{}
	for _, service := range migration.Services {
		if service.Active == "native" {
			activeNative[service.Name] = true
		}
	}
	services := map[string]*model.Service{}
	for _, service := range appModel.Services {
		services[service.Name] = service
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
		service := services[operation.Module]
		if service == nil || service.Struct == nil || service.RootPackage == nil || service.RootPackage.Analysis == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6101", Severity: "error", Message: "native operation " + operation.Address + " has no Go service type", Address: operation.Address})
			continue
		}
		object := service.RootPackage.Analysis.Types.Scope().Lookup(service.Struct.TypeName)
		if object == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6102", Severity: "error", Message: "native service type " + service.Struct.TypeName + " was not found", Address: operation.Address})
			continue
		}
		named, ok := object.Type().(*types.Named)
		if !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6102", Severity: "error", Message: "native service type is not named", Address: operation.Address})
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

func lowerLegacyService(service *model.Service, state MigrationService) []Resource {
	meta := &MigrationMeta{State: state.State, Active: state.Active}
	origin := Origin{Kind: "legacy_v0", Frontend: "scenery.legacy.v0"}
	resources := []Resource{{Address: resourceAddress(service.Name, "service", service.Name), Kind: "scenery.service/v1", Name: service.Name, Module: service.Name, Spec: map[string]any{"runtime": "go", "implementation": map[string]any{"adapter": "legacy_go_v0", "root": service.RootRelDir}}, Origin: origin, Migration: meta}}
	for _, endpoint := range service.Endpoints {
		resources = append(resources, lowerLegacyEndpoint(service.Name, endpoint.Name, endpoint.Path, endpoint.Methods, string(endpoint.Access), endpoint.Raw, origin, meta)...)
	}
	for _, endpoint := range service.Generated {
		resources = append(resources, lowerLegacyEndpoint(service.Name, endpoint.Name, endpoint.Path, endpoint.Methods, string(endpoint.Access), false, origin, meta)...)
	}
	return resources
}

func lowerLegacyEndpoint(service, name, path string, methods []string, access string, raw bool, origin Origin, meta *MigrationMeta) []Resource {
	semantic := snakeName(name)
	operationAddress := resourceAddress(service, "operation", semantic)
	resources := []Resource{{Address: operationAddress, Kind: "scenery.operation/v1", Name: semantic, Module: service, Spec: map[string]any{"service": map[string]any{"$ref": resourceAddress(service, "service", service)}, "input": map[string]any{"$ref": "legacy.type.advisory"}, "handler": map[string]any{"method": name, "adapter": "legacy_go_v0"}}, Origin: origin, Migration: meta}}
	for i, method := range methods {
		bindingName := semantic + "_http"
		if len(methods) > 1 {
			bindingName += fmt.Sprintf("_%d", i+1)
		}
		resources = append(resources, Resource{Address: resourceAddress(service, "binding", bindingName), Kind: "scenery.binding/v1", Name: bindingName, Module: service, Spec: map[string]any{"gateway": map[string]any{"$ref": "app/http_gateway/public_api"}, "operation": map[string]any{"$ref": operationAddress}, "protocol": "http", "delivery": "call", "authentication": access, "http": map[string]any{"method": method, "path": legacyPathToNative(path), "codec_profile": map[bool]string{true: "scenery.legacy-raw/v0", false: "scenery.legacy-json/v0"}[raw], "guarantee": map[bool]string{true: "opaque", false: "legacy_exact"}[raw]}}, Origin: origin, Migration: meta})
	}
	return resources
}

func compareNativeServiceRoutes(service *model.Service, native map[string]string) []Diagnostic {
	legacy := map[string]string{}
	for _, endpoint := range service.Endpoints {
		for _, method := range endpoint.Methods {
			legacy[routeShape(method, endpoint.Path)] = endpoint.Name
		}
	}
	var diagnostics []Diagnostic
	for key, name := range legacy {
		if _, ok := native[key]; !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5205", Severity: "error", Message: fmt.Sprintf("native service %s does not own legacy route %s (%s)", service.Name, key, name)})
		}
	}
	for key, address := range native {
		if _, ok := legacy[key]; !ok {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN5206", Severity: "error", Message: fmt.Sprintf("native service %s adds unmatched route %s", service.Name, key), Address: address})
		}
	}
	sort.Slice(diagnostics, func(i, j int) bool { return diagnostics[i].Message < diagnostics[j].Message })
	return diagnostics
}

func routesByModule(resources []Resource) map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, r := range resources {
		if r.Kind != "scenery.binding/v1" {
			continue
		}
		httpSpec, _ := r.Spec["http"].(map[string]any)
		if httpSpec == nil {
			continue
		}
		method, _ := httpSpec["method"].(string)
		path, _ := httpSpec["path"].(string)
		if method == "" || path == "" {
			continue
		}
		if out[r.Module] == nil {
			out[r.Module] = map[string]string{}
		}
		out[r.Module][routeShape(method, path)] = r.Address
	}
	return out
}
func routeShape(method, path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, ":") || (strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
			parts[i] = "{}"
		}
	}
	return strings.ToUpper(method) + " " + strings.Join(parts, "/")
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
	var b strings.Builder
	for i, r := range value {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
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
