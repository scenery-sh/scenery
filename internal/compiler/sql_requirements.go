package compiler

import (
	"fmt"
	"slices"
	"sort"

	"scenery.sh/internal/postgresname"
)

type SQLRequirementKind string

const (
	SQLDataSource   SQLRequirementKind = "data_source"
	SQLStandardAuth SQLRequirementKind = "standard_auth"
	SQLDurable      SQLRequirementKind = "durable_execution"
)

// SQLRequirement preserves declaration identity independently of the shared
// physical binding. Framework registrations use the reserved scenery schema;
// they do not manufacture an application data-source declaration.
type SQLRequirement struct {
	Kind         SQLRequirementKind `json:"kind"`
	Address      string             `json:"address,omitempty"`
	ConfigPath   string             `json:"config_path,omitempty"`
	Provider     string             `json:"provider,omitempty"`
	Capabilities []string           `json:"capabilities"`
	Name         string             `json:"name"`
	Schema       string             `json:"schema"`
	Lifecycle    string             `json:"lifecycle"`
	Consumers    []string           `json:"consumers"`
	Origin       Origin             `json:"origin"`
}

// SQLRequirements is immutable compilation data, not allocation state or a
// database connection. An environment can supply these bindings externally.
type SQLRequirements []SQLRequirement

type SQLBinding struct {
	Name   string
	Schema string
}

func (requirements SQLRequirements) Binding(name string) (SQLBinding, bool) {
	for _, requirement := range requirements {
		if requirement.Name == name {
			return SQLBinding{Name: requirement.Name, Schema: requirement.Schema}, true
		}
	}
	return SQLBinding{}, false
}

// Bindings returns the distinct local SQL bindings. An explicit remote durable
// endpoint supplies durable execution without a local framework database.
func (requirements SQLRequirements) Bindings(remoteDurable bool) []SQLBinding {
	byName := map[string]SQLBinding{}
	for _, requirement := range requirements {
		if remoteDurable && requirement.Kind == SQLDurable {
			continue
		}
		byName[requirement.Name] = SQLBinding{Name: requirement.Name, Schema: requirement.Schema}
	}
	bindings := make([]SQLBinding, 0, len(byName))
	for _, binding := range byName {
		bindings = append(bindings, binding)
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].Name < bindings[j].Name })
	return bindings
}

// ProjectSQLRequirements uses the same service, dependency and durable
// registration projections as generation. authEnabled is the existing selected
// standard-auth registration flag, not an inferred Go import or SQL file.
func ProjectSQLRequirements(resources []Resource, authEnabled bool) (SQLRequirements, []Diagnostic) {
	byAddress := resourcesByAddress(&Manifest{Resources: resources})
	byRequirement := map[string]*SQLRequirement{}
	var diagnostics []Diagnostic
	for _, service := range RuntimeServices(resources) {
		dependencies, err := ServiceGoDependencies(byAddress, service)
		if err != nil {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2505", err.Error(), service))
			continue
		}
		for _, dependency := range dependencies {
			if dependency.Resolver != "sql" {
				continue
			}
			instance := byAddress[dependency.Address]
			requirement := byRequirement[instance.Address]
			if requirement == nil {
				schema, err := sqlBindingSchema(dependency.RuntimeName)
				if err != nil {
					diagnostics = append(diagnostics, dataDiagnostic("SCN2505", fmt.Sprintf("SQL binding %q has an invalid schema: %v; set data_source config.database to a valid logical name", dependency.RuntimeName, err), instance))
					continue
				}
				requirement = &SQLRequirement{Kind: SQLDataSource, Address: instance.Address, Name: dependency.RuntimeName, Schema: schema,
					Provider:     resolveResourceRef(instance, refString(instance.Spec["provider"]), "provider"),
					Capabilities: sortedSQLRequirementCapabilities(instance), Lifecycle: stringValue(instance.Spec["lifecycle"]), Origin: instance.Origin}
				byRequirement[instance.Address] = requirement
			}
			requirement.Consumers = append(requirement.Consumers, service.Address)
		}
		for _, execution := range DurableExecutionsForOperations(resources, ServiceOperations(resources, service)) {
			engineAddress := resolveResourceRef(execution, refString(execution.Spec["engine"]), "execution_engine")
			engine, ok := byAddress[engineAddress]
			if !ok {
				diagnostics = append(diagnostics, dataDiagnostic("SCN2505", "durable execution requires a resolved engine before SQL binding", execution))
				continue
			}
			requirement := byRequirement[engine.Address]
			if requirement == nil {
				requirement = &SQLRequirement{Kind: SQLDurable, Address: engine.Address, Name: "scenery", Schema: "scenery",
					Provider:     resolveResourceRef(engine, refString(engine.Spec["provider"]), "provider"),
					Capabilities: sortedSQLRequirementCapabilities(engine), Lifecycle: stringValue(engine.Spec["lifecycle"]), Origin: engine.Origin}
				byRequirement[engine.Address] = requirement
			}
			requirement.Consumers = append(requirement.Consumers, execution.Address)
		}
	}
	result := make(SQLRequirements, 0, len(byRequirement)+1)
	for _, requirement := range byRequirement {
		sort.Strings(requirement.Consumers)
		requirement.Consumers = slices.Compact(requirement.Consumers)
		result = append(result, *requirement)
	}
	if authEnabled {
		result = append(result, SQLRequirement{Kind: SQLStandardAuth, ConfigPath: ".scenery.json#auth.enabled", Provider: "std.provider.standard_auth",
			Capabilities: []string{}, Name: "scenery", Schema: "scenery", Lifecycle: "managed", Consumers: []string{}, Origin: Origin{Kind: "framework"}})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Address < result[j].Address
	})
	schemaOwners := map[string]string{}
	for _, requirement := range result {
		if previous := schemaOwners[requirement.Schema]; previous != "" && previous != requirement.Name {
			diagnostics = append(diagnostics, dataDiagnostic("SCN2505", fmt.Sprintf("SQL bindings %q and %q both map to schema %q; use distinct config.database names or one explicit shared binding", previous, requirement.Name, requirement.Schema), byAddress[requirement.Address]))
		}
		schemaOwners[requirement.Schema] = requirement.Name
	}
	return result, diagnostics
}

func sortedSQLRequirementCapabilities(resource Resource) []string {
	capabilities := append([]string{}, stringValues(resource.Spec["require_capabilities"])...)
	sort.Strings(capabilities)
	return capabilities
}

func sqlBindingSchema(name string) (string, error) {
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			continue
		}
		return "", fmt.Errorf("use lowercase letters, numbers, dots, underscores, or dashes")
	}
	schema, err := postgresname.SchemaNameFor(name)
	if err == nil && len(schema) > 63 {
		return "", fmt.Errorf("PostgreSQL schema names must not exceed 63 bytes")
	}
	return schema, err
}
