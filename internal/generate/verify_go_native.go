package generate

import (
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/model"
)

func nativeGoPackage(appModel *model.App, resources []Resource, module string) *model.Package {
	implementationImport := ""
	root := "."
	for _, resource := range resources {
		if resource.Kind != "scenery.module" || moduleInstancePath(resource) != module {
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

func objectTypeSignature(object types.Object) (*types.Signature, bool) {
	if object == nil {
		return nil, false
	}
	signature, ok := object.Type().(*types.Signature)
	return signature, ok
}

func validateNativeGoServices(appModel *model.App, resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	for _, resource := range resources {
		if resource.Kind != "scenery.service" || resource.Origin.Kind != "authored" || stringValue(resource.Spec["runtime"]) != "go" {
			continue
		}
		implementation, _ := resource.Spec["implementation"].(map[string]any)
		constructor := stringValue(implementation["constructor"])
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
		named := nativeServiceNamedType(pkg, constructor)
		receiver := "Service"
		if named != nil {
			receiver = named.Obj().Name()
		}
		expected := expectedSignature(goConstructorSignature(constructor, resource.Name, receiver))
		if object == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6112", Severity: "error", Message: "native constructor " + constructor + " not found", Address: resource.Address, Suggestions: []string{expected}})
			continue
		}
		signature, ok := object.Type().(*types.Signature)
		want := goName(resource.Name) + "ConstructorInput"
		if !ok || signature.Params().Len() != 2 || signature.Results().Len() != 2 ||
			!isContextParameter(signature) || !contractTypeMatches(signature.Params().At(1).Type(), want) ||
			types.TypeString(signature.Results().At(1).Type(), packageQualifier) != "error" || named == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6113", Severity: "error", Message: "native constructor " + constructor + " has type " + types.TypeString(object.Type(), packageQualifier), Address: resource.Address, Suggestions: []string{expected}})
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
		method := stringValue(lifecycle[phase])
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

func validateNativeGoHandlers(appModel *model.App, resources []Resource) []Diagnostic {
	var diagnostics []Diagnostic
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	for _, operation := range resources {
		if operation.Kind != "scenery.operation" || operation.Origin.Kind != "authored" {
			continue
		}
		if refString(operation.Spec["library"]) != "" {
			continue
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		methodName := stringValue(handler["method"])
		if methodName == "" {
			continue
		}
		serviceResource := byAddress[resolveResourceRef(operation, refString(operation.Spec["service"]), "service")]
		if compiler.IsExternalWorkerRuntime(stringValue(serviceResource.Spec["runtime"])) {
			continue
		}
		implementation, _ := serviceResource.Spec["implementation"].(map[string]any)
		pkg := nativeGoPackage(appModel, resources, operation.Module)
		named := nativeServiceNamedType(pkg, stringValue(implementation["constructor"]))
		if pkg == nil || named == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6101", Severity: "error", Message: "native operation " + operation.Address + " has no Go service type", Address: operation.Address})
			continue
		}
		streaming := operationUsesHTTPStream(operation, resources)
		expected := expectedSignature(goHandlerSignature(methodName, operation.Name, named.Obj().Name(), streaming))
		selection := types.NewMethodSet(types.NewPointer(named)).Lookup(nil, methodName)
		if selection == nil {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6103", Severity: "error", Message: "native handler method " + methodName + " not found", Address: operation.Address, Suggestions: []string{expected}})
			continue
		}
		wantResults := 2
		if streaming {
			wantResults = 3
		}
		signature, ok := selection.Obj().Type().(*types.Signature)
		observed := "native handler " + methodName + " has type " + types.TypeString(selection.Obj().Type(), packageQualifier)
		if !ok || signature.Params().Len() != 2 || signature.Results().Len() != wantResults || !isContextParameter(signature) {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6104", Severity: "error", Message: observed, Address: operation.Address, Suggestions: []string{expected}})
			continue
		}
		name := goName(operation.Name)
		valid := contractTypeMatches(signature.Params().At(1).Type(), name+"Input") &&
			contractTypeMatches(signature.Results().At(0).Type(), name+"Outcome") &&
			types.TypeString(signature.Results().At(wantResults-1).Type(), packageQualifier) == "error"
		if streaming {
			stream := types.TypeString(signature.Results().At(1).Type(), packageQualifier)
			valid = valid && (stream == "scenery.sh.ByteStream" || strings.HasSuffix(stream, "/runtime.ContractByteStream"))
		}
		if !valid {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN6105", Severity: "error", Message: observed, Address: operation.Address, Suggestions: []string{expected}})
		}
	}
	return diagnostics
}

func isContextParameter(signature *types.Signature) bool {
	return types.TypeString(types.Unalias(signature.Params().At(0).Type()), packageQualifier) == "context.Context"
}

// contractTypeMatches accepts the generated contract type named want or any
// type identical to it. Generated contracts declare an operation's input as an
// alias when it reuses a shared record (`type FinishInput = SessionRef`); a
// handler may spell either name because Go treats them as the same type.
func contractTypeMatches(got types.Type, want string) bool {
	if strings.HasSuffix(types.TypeString(got, packageQualifier), "/scenerycontract."+want) {
		return true
	}
	named, ok := types.Unalias(got).(*types.Named)
	if !ok || named.Obj().Pkg() == nil || !strings.HasSuffix(named.Obj().Pkg().Path(), "/scenerycontract") {
		return false
	}
	expected, ok := named.Obj().Pkg().Scope().Lookup(want).(*types.TypeName)
	return ok && types.Identical(types.Unalias(expected.Type()), types.Unalias(got))
}

func packageQualifier(pkg *types.Package) string {
	if pkg == nil {
		return ""
	}
	return pkg.Path()
}
