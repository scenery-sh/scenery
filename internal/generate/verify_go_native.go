package generate

import (
	"fmt"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"

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
	for _, operation := range resources {
		if operation.Kind != "scenery.operation" || operation.Origin.Kind != "authored" {
			continue
		}
		handler, _ := operation.Spec["handler"].(map[string]any)
		methodName := stringValue(handler["method"])
		if methodName == "" {
			continue
		}
		var serviceResource Resource
		for _, candidate := range resources {
			if candidate.Kind == "scenery.service" && candidate.Module == operation.Module && candidate.Origin.Kind == "authored" {
				serviceResource = candidate
				break
			}
		}
		implementation, _ := serviceResource.Spec["implementation"].(map[string]any)
		pkg := nativeGoPackage(appModel, resources, operation.Module)
		named := nativeServiceNamedType(pkg, stringValue(implementation["constructor"]))
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
