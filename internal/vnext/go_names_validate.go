package vnext

import "fmt"

func validateGoGeneratedNames(resources []Resource) []Diagnostic {
	goModules := map[string]bool{}
	goModuleResources := map[string]Resource{}
	for _, resource := range resources {
		if resource.Kind != "scenery.module/v1" {
			continue
		}
		metadata, _ := resource.Spec["package"].(map[string]any)
		if goContract, ok := metadata["go_contract"].(map[string]any); ok && stringValue(goContract["import_path"]) != "" {
			instance := moduleInstancePath(resource)
			goModules[instance] = true
			goModuleResources[instance] = resource
		}
	}
	type owner struct{ address, description string }
	symbols := map[string]map[string]owner{}
	var diagnostics []Diagnostic
	add := func(resource Resource, name, description string) {
		if name == "" || !goModules[resource.Module] {
			return
		}
		moduleSymbols := symbols[resource.Module]
		if moduleSymbols == nil {
			moduleSymbols = map[string]owner{}
			symbols[resource.Module] = moduleSymbols
		}
		if previous, exists := moduleSymbols[name]; exists {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SCN4012", Severity: "error", Address: resource.Address,
				Message: fmt.Sprintf("generated Go identifier %s for %s collides with %s from %s", name, description, previous.description, previous.address),
			})
			return
		}
		moduleSymbols[name] = owner{address: resource.Address, description: description}
	}
	for module, resource := range goModuleResources {
		resource.Module = module
		for _, fixed := range []string{"PackageIdentity", "PackageVersion", "PackageContractABIRevision", "PackageImportPath", "GoImplementationABIRange", "RuntimeABIRange", "unmarshalGeneratedContractValue"} {
			add(resource, fixed, "generated package symbol")
		}
	}
	for _, resource := range resources {
		if !goModules[resource.Module] {
			continue
		}
		name := goName(resource.Name)
		switch resource.Kind {
		case "scenery.record/v1":
			add(resource, name, "record "+resource.Name)
			diagnostics = append(diagnostics, validateGoFieldNames(resource, namedChildren(resource.Spec, "field"), resource.Spec["unknown_fields"] == "preserve")...)
		case "scenery.enum/v1":
			add(resource, name, "enum "+resource.Name)
			for _, value := range namedChildren(resource.Spec, "value") {
				add(resource, name+goName(stringValue(value["name"])), "enum value "+stringValue(value["name"]))
			}
		case "scenery.union/v1":
			add(resource, name, "union "+resource.Name)
			add(resource, "Marshal"+name+"JSON", "union marshal function")
			add(resource, "Unmarshal"+name+"JSON", "union unmarshal function")
			for _, variant := range namedChildren(resource.Spec, "variant") {
				add(resource, name+goName(stringValue(variant["name"])), "union variant "+stringValue(variant["name"]))
			}
			if resource.Spec["open"] == true {
				add(resource, name+"Unknown", "open union fallback")
			}
		case "scenery.operation/v1":
			if goType(resource.Spec["input"]) != name+"Input" {
				add(resource, name+"Input", "operation input alias")
			}
			add(resource, name+"Outcome", "operation outcome")
			for _, function := range []string{"Clone" + name + "Input", "Marshal" + name + "Outcome", "Unmarshal" + name + "Outcome", "Clone" + name + "Outcome"} {
				add(resource, function, "operation codec function")
			}
			for _, kind := range []string{"result", "error"} {
				for _, variant := range namedChildren(resource.Spec, kind) {
					add(resource, name+goName(stringValue(variant["name"])), "operation "+kind+" "+stringValue(variant["name"]))
				}
			}
		case "scenery.service/v1":
			for _, suffix := range []string{"Dependencies", "Config", "Clients", "ConstructorInput"} {
				add(resource, name+suffix, "service "+suffix)
			}
			diagnostics = append(diagnostics, validateGoFieldNames(resource, namedChildren(resource.Spec, "config_schema"), false)...)
			if dependencies, err := serviceGoDependencies(resources, resource); err == nil {
				fields := make([]string, 0, len(dependencies))
				for _, dependency := range dependencies {
					fields = append(fields, dependency.Field)
				}
				diagnostics = append(diagnostics, validateGeneratedGoFieldList(resource, fields, "service dependency")...)
			}
			if clients, err := serviceGoClients(resources, resource); err == nil {
				fields := make([]string, 0, len(clients))
				for _, client := range clients {
					add(resource, client.InterfaceName, "internal client interface "+client.Name)
					fields = append(fields, client.Field)
				}
				diagnostics = append(diagnostics, validateGeneratedGoFieldList(resource, fields, "service client")...)
			}
		}
	}
	return diagnostics
}

func validateGeneratedGoFieldList(resource Resource, fields []string, description string) []Diagnostic {
	seen := map[string]bool{}
	var diagnostics []Diagnostic
	for _, field := range fields {
		if seen[field] {
			diagnostics = append(diagnostics, Diagnostic{Code: "SCN4012", Severity: "error", Address: resource.Address, Message: fmt.Sprintf("generated Go field %s collides within %s fields", field, description)})
		}
		seen[field] = true
	}
	return diagnostics
}

func validateGoFieldNames(resource Resource, fields []map[string]any, reserveUnknown bool) []Diagnostic {
	seen := map[string]string{}
	if reserveUnknown {
		seen["UnknownFields"] = "generated unknown-field storage"
	}
	var diagnostics []Diagnostic
	for _, field := range fields {
		source := stringValue(field["name"])
		generated := goName(source)
		if previous := seen[generated]; previous != "" {
			diagnostics = append(diagnostics, Diagnostic{
				Code: "SCN4012", Severity: "error", Address: resource.Address,
				Message: fmt.Sprintf("generated Go field %s for %s collides with %s", generated, source, previous),
			})
			continue
		}
		seen[generated] = source
	}
	return diagnostics
}
