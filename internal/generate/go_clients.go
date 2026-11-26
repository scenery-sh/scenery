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
	OperationName string
	Projected     bool
	Binding       Resource
	Operation     Resource
	Delivery      string
}

func serviceGoClients(idx *resourceIndex, service Resource) ([]goClientBinding, error) {
	byService, err := moduleGoClients(idx, service)
	if err != nil {
		return nil, err
	}
	return byService[service.Address], nil
}

func generatedTypeResourceSymbols(res Resource) []string {
	name := goName(res.Name)
	switch res.Kind {
	case "scenery.record":
		return []string{name}
	case "scenery.enum":
		symbols := []string{name}
		for _, value := range namedChildren(res.Spec, "value") {
			symbols = append(symbols, name+goName(stringValue(value["name"])))
		}
		return symbols
	case "scenery.union":
		symbols := []string{
			name,
			"Marshal" + name + "JSON",
			"Unmarshal" + name + "JSON",
			"marshal" + name + "Variant",
		}
		for _, variant := range namedChildren(res.Spec, "variant") {
			symbols = append(symbols, name+goName(stringValue(variant["name"])))
		}
		if res.Spec["open"] == true {
			symbols = append(symbols, name+"Unknown")
		}
		return symbols
	}
	return nil
}

func reserveGeneratedTypeResource(used map[string]bool, res Resource) {
	for _, symbol := range generatedTypeResourceSymbols(res) {
		used[symbol] = true
	}
}

func moduleReservedPackageSymbols(localResources []Resource) map[string]bool {
	used := map[string]bool{
		"PackageIdentity":                 true,
		"PackageContractABIRevision":      true,
		"PackageImportPath":               true,
		"GoImplementationABIRange":        true,
		"RuntimeABIRange":                 true,
		"unmarshalGeneratedContractValue": true,
	}
	for _, res := range localResources {
		name := goName(res.Name)
		switch res.Kind {
		case "scenery.service":
			used[name] = true
			for _, suffix := range []string{"Dependencies", "Config", "Clients", "ConstructorInput"} {
				used[name+suffix] = true
			}
		case "scenery.record", "scenery.enum", "scenery.union":
			reserveGeneratedTypeResource(used, res)
		case "scenery.operation":
			for _, symbol := range generatedOperationSymbols(name, res) {
				used[symbol] = true
			}
		}
	}
	return used
}

func moduleGoClients(idx *resourceIndex, targetService Resource) (map[string][]goClientBinding, error) {
	byAddress := idx.byAddress
	module := targetService.Module
	localResources := idx.moduleResources(module)
	used := moduleReservedPackageSymbols(localResources)
	reserveService := func(res Resource) {
		name := goName(res.Name)
		used[name] = true
		for _, suffix := range []string{"Dependencies", "Config", "Clients", "ConstructorInput"} {
			used[name+suffix] = true
		}
	}
	var services []Resource
	seenTarget := false
	for _, res := range localResources {
		if res.Kind == "scenery.service" {
			services = append(services, res)
			if res.Address == targetService.Address {
				seenTarget = true
			}
		}
	}
	if !seenTarget && targetService.Address != "" {
		reserveService(targetService)
		services = append(services, targetService)
		sort.Slice(services, func(i, j int) bool { return services[i].Address < services[j].Address })
	}
	byService := make(map[string][]goClientBinding, len(services))
	var rawProjectedOps []Resource
	for _, service := range services {
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
			projected := operation.Module != service.Module
			result = append(result, goClientBinding{
				Name: name, Field: goName(name), Projected: projected,
				Binding: binding, Operation: operation, Delivery: delivery,
			})
			if projected {
				rawOp := operation
				rawOp.Spec = qualifyProjectedGoTypes(rawOp)
				rawProjectedOps = append(rawProjectedOps, rawOp)
			}
		}
		sort.Slice(result, func(i, j int) bool { return result[i].Name < result[j].Name })
		byService[service.Address] = result
	}
	if len(rawProjectedOps) > 0 || len(localResources) > 0 {
		allResources := idx.all
		if len(allResources) == 0 {
			allResources = localResources
		}
		closureResources, _, err := goContractTypeClosure(module, localResources, allResources, rawProjectedOps)
		if err != nil {
			return nil, err
		}
		for _, res := range closureResources {
			reserveGeneratedTypeResource(used, res)
		}
	}
	// Assign and reserve every service's client interface name before choosing
	// projected operation names so sibling services and variant wrappers never collide.
	for _, service := range services {
		servicePrefix := goName(service.Name)
		result := byService[service.Address]
		for i := range result {
			base := result[i].Field + "InternalClient"
			iface := base
			if used[iface] {
				iface = servicePrefix + base
				for suffix := 2; used[iface]; suffix++ {
					iface = fmt.Sprintf("%s%s%dInternalClient", servicePrefix, result[i].Field, suffix)
				}
			}
			used[iface] = true
			result[i].InterfaceName = iface
		}
	}
	for _, service := range services {
		result := byService[service.Address]
		for i := range result {
			if !result[i].Projected {
				result[i].OperationName = goName(result[i].Operation.Name)
				continue
			}
			base := result[i].Field + "Client"
			candidate := base
			if !symbolsAvailable(used, generatedOperationSymbols(candidate, result[i].Operation)) {
				candidate = "Projected" + base
				for suffix := 2; !symbolsAvailable(used, generatedOperationSymbols(candidate, result[i].Operation)); suffix++ {
					candidate = fmt.Sprintf("Projected%d%s", suffix, base)
				}
			}
			for _, symbol := range generatedOperationSymbols(candidate, result[i].Operation) {
				used[symbol] = true
			}
			result[i].OperationName = candidate
		}
	}
	return byService, nil
}

type operationVariantWrapper struct {
	Kind        string
	VariantName string
	WrapperName string
	Spec        map[string]any
}

func operationVariantWrappers(opName string, op Resource) []operationVariantWrapper {
	localUsed := map[string]bool{
		opName:                           true,
		opName + "Input":                 true,
		"Unmarshal" + opName + "Input":   true,
		"Clone" + opName + "Input":       true,
		opName + "Outcome":               true,
		"Marshal" + opName + "Outcome":   true,
		"Unmarshal" + opName + "Outcome": true,
		"Clone" + opName + "Outcome":     true,
	}
	var wrappers []operationVariantWrapper
	for _, kind := range []string{"result", "error"} {
		for _, variant := range namedChildren(op.Spec, kind) {
			variantName := stringValue(variant["name"])
			wrapper := opName + goName(variantName)
			if localUsed[wrapper] {
				base := opName + goName(kind) + "Variant" + goName(variantName)
				wrapper = base
				for suffix := 2; localUsed[wrapper]; suffix++ {
					wrapper = fmt.Sprintf("%s%d", base, suffix)
				}
			}
			localUsed[wrapper] = true
			wrappers = append(wrappers, operationVariantWrapper{
				Kind:        kind,
				VariantName: variantName,
				WrapperName: wrapper,
				Spec:        variant,
			})
		}
	}
	return wrappers
}

func operationVariantWrapperMap(opName string, op Resource) map[string]string {
	entries := operationVariantWrappers(opName, op)
	byOutcome := make(map[string]string, len(entries))
	for _, entry := range entries {
		byOutcome[entry.Kind+"."+entry.VariantName] = entry.WrapperName
	}
	return byOutcome
}

// generatedOperationSymbols returns all package-level identifiers emitted by
// renderOperationAPI for an operation named name.
func generatedOperationSymbols(name string, op Resource) []string {
	symbols := []string{
		name,
		name + "Input",
		"Unmarshal" + name + "Input",
		"Clone" + name + "Input",
		name + "Outcome",
		"Marshal" + name + "Outcome",
		"Unmarshal" + name + "Outcome",
		"Clone" + name + "Outcome",
	}
	for _, entry := range operationVariantWrappers(name, op) {
		symbols = append(symbols, entry.WrapperName)
	}
	return symbols
}

func symbolsAvailable(used map[string]bool, symbols []string) bool {
	for _, symbol := range symbols {
		if used[symbol] {
			return false
		}
	}
	return true
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
	if client.OperationName != "" {
		return client.OperationName
	}
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

func renderWireGoClient(b *strings.Builder, client goClientBinding) {
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
	fmt.Fprintf(b, "\toutput, err := sceneryruntime.InvokeWorkerBinding(ctx, invocation, %q, %t, raw)\n", client.Name, client.Delivery == "enqueue")
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
