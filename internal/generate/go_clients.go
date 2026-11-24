package generate

import (
	"fmt"
	"maps"
	"sort"
	"strings"
)

type goClientBinding struct {
	Name          string
	Field         string
	InterfaceName string
	Projected     bool
	Binding       Resource
	Operation     Resource
	Delivery      string
}

func serviceGoClients(idx *resourceIndex, service Resource) ([]goClientBinding, error) {
	byAddress := idx.byAddress
	var result []goClientBinding
	for _, client := range namedChildren(service.Spec, "client") {
		name := stringValue(client["name"])
		bindingRef := refString(client["binding"])
		bindingAddress := resolveResourceRef(service, bindingRef, "binding")
		binding, ok := byAddress[bindingAddress]
		if name == "" || !ok || binding.Kind != "scenery.binding" {
			return nil, fmt.Errorf("service %s client %q references unavailable binding %q", service.Address, name, bindingRef)
		}
		if stringValue(binding.Spec["protocol"]) != "internal" {
			return nil, fmt.Errorf("service %s client %s requires an internal binding", service.Address, name)
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		operation, ok := byAddress[operationAddress]
		if !ok || operation.Kind != "scenery.operation" {
			return nil, fmt.Errorf("service %s client %s references unavailable operation", service.Address, name)
		}
		delivery := stringValue(binding.Spec["delivery"])
		if delivery != "call" && delivery != "wait" && delivery != "enqueue" {
			return nil, fmt.Errorf("service %s client %s has unsupported delivery %q", service.Address, name, delivery)
		}
		result = append(result, goClientBinding{
			Name: name, Field: goName(name), InterfaceName: goName(name) + "InternalClient", Projected: operation.Module != service.Module,
			Binding: binding, Operation: operation, Delivery: delivery,
		})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
	return result, nil
}

func moduleContractImportPath(resources []Resource, module string) (string, bool) {
	for _, resource := range resources {
		if resource.Kind != "scenery.module" || moduleInstancePath(resource) != module {
			continue
		}
		metadata, _ := resource.Spec["package"].(map[string]any)
		goContract, _ := metadata["go_contract"].(map[string]any)
		importPath := strings.TrimSpace(stringValue(goContract["import_path"]))
		if importPath != "" {
			return strings.TrimSuffix(importPath, "/") + "/scenerycontract", true
		}
	}
	return "", false
}

func (client goClientBinding) operationName() string {
	if client.Projected {
		return client.Field + "Client"
	}
	return goName(client.Operation.Name)
}

func projectedGoClientOperations(resources []Resource, idx *resourceIndex) ([]Resource, error) {
	var operations []Resource
	for _, service := range resources {
		if service.Kind != "scenery.service" {
			continue
		}
		clients, err := serviceGoClients(idx, service)
		if err != nil {
			return nil, err
		}
		for _, client := range clients {
			if !client.Projected {
				continue
			}
			op := client.Operation
			op.Name = client.operationName()
			op.Spec = qualifyProjectedGoTypes(op)
			operations = append(operations, op)
		}
	}
	return operations, nil
}

// Projected declarations retain their authored type owner when emitted into
// the caller's package. The canonical graph uses relative refs for local types.
func qualifyProjectedGoTypes(resource Resource) map[string]any {
	spec := maps.Clone(resource.Spec)
	if resource.Kind == "scenery.operation" {
		spec["input"] = qualifyProjectedGoType(spec["input"], resource.Module)
	}
	for _, kind := range []string{"field", "variant", "result", "error"} {
		switch children := spec[kind].(type) {
		case map[string]any:
			child := maps.Clone(children)
			child["type"] = qualifyProjectedGoType(child["type"], resource.Module)
			spec[kind] = child
		case []any:
			projected := make([]any, 0, len(children))
			for _, value := range children {
				child, ok := value.(map[string]any)
				if ok {
					child = maps.Clone(child)
					child["type"] = qualifyProjectedGoType(child["type"], resource.Module)
					value = child
				}
				projected = append(projected, value)
			}
			spec[kind] = projected
		}
	}
	return spec
}

func qualifyProjectedGoType(value any, module string) any {
	switch value := value.(type) {
	case string:
		return qualifyProjectedGoExpression(value, module)
	case map[string]any:
		for _, key := range []string{"$ref", "$expression"} {
			if expression, ok := value[key].(string); ok {
				result := maps.Clone(value)
				result[key] = qualifyProjectedGoExpression(expression, module)
				return result
			}
		}
	}
	return value
}

func qualifyProjectedGoExpression(expression, module string) string {
	expression = strings.TrimSpace(expression)
	if open := strings.IndexByte(expression, '('); open >= 0 && strings.HasSuffix(expression, ")") {
		arguments := splitTypeArguments(expression[open+1 : len(expression)-1])
		for index, argument := range arguments {
			arguments[index] = qualifyProjectedGoExpression(argument, module)
		}
		return expression[:open] + "(" + strings.Join(arguments, ", ") + ")"
	}
	kind, name, found := strings.Cut(expression, ".")
	if found && (kind == "record" || kind == "enum" || kind == "union") {
		return resourceAddress(module, kind, name)
	}
	return expression
}

func renderProjectedGoClient(b *strings.Builder, client goClientBinding, caller string) {
	name := client.operationName()
	clientType := semanticPathName(client.Name) + "InternalClient"
	fmt.Fprintf(b, "type %s struct{}\n", clientType)
	method := "Invoke"
	resultType, zero := "contract."+name+"Outcome", "nil"
	if client.Delivery == "enqueue" {
		method = "Enqueue"
		resultType, zero = "scenery.ExecutionReceipt", "scenery.ExecutionReceipt{}"
	}
	fmt.Fprintf(b, "func (%s) %s(ctx context.Context, invocation scenery.Invocation, input contract.%sInput) (%s, error) {\n", clientType, method, name, resultType)
	fmt.Fprintf(b, "\traw, err := scenery.MarshalContractValue(input, %q)\n", goWireTypeExpression(client.Operation.Spec["input"]))
	fmt.Fprintf(b, "\tif err != nil { return %s, fmt.Errorf(\"encode internal client input: %%w\", err) }\n", zero)
	fmt.Fprintf(b, "\toutput, err := sceneryruntime.InvokeContractBindingWire(ctx, %q, %q, invocation, raw)\n", client.Binding.Address, caller)
	fmt.Fprintf(b, "\tif err != nil { return %s, err }\n", zero)
	if client.Delivery == "enqueue" {
		b.WriteString("\tvar receipt scenery.ExecutionReceipt\n")
		b.WriteString("\tif err := scenery.UnmarshalContractValue(output, &receipt, \"std.type.execution_receipt\"); err != nil { return scenery.ExecutionReceipt{}, sceneryruntime.ContractSystemError(err) }\n")
		b.WriteString("\treturn receipt, nil\n")
	} else {
		fmt.Fprintf(b, "\toutcome, err := contract.Unmarshal%sOutcome(output)\n", name)
		b.WriteString("\tif err != nil { return nil, sceneryruntime.ContractSystemError(err) }\n")
		b.WriteString("\treturn outcome, nil\n")
	}
	b.WriteString("}\n\n")
}

func internalBindingsForOperations(resources []Resource, operations []Resource) []Resource {
	operationAddresses := map[string]bool{}
	for _, operation := range operations {
		operationAddresses[operation.Address] = true
	}
	var bindings []Resource
	for _, binding := range resources {
		if binding.Kind != "scenery.binding" || stringValue(binding.Spec["protocol"]) != "internal" {
			continue
		}
		operationAddress := resolveResourceRef(binding, refString(binding.Spec["operation"]), "operation")
		if operationAddresses[operationAddress] {
			bindings = append(bindings, binding)
		}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Address < bindings[j].Address })
	return bindings
}
