// Package appconfig owns environment configuration: the typed input catalog
// derived from a compiled application, pure resolution of one environment's
// configured values, and the authoritative per-application environment store.
package appconfig

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"scenery.sh/internal/compiler"
	"scenery.sh/internal/graph"
)

// SecretType is the declared type of every sensitive configuration input.
const SecretType = `resource_ref("secret")`

// FrameworkConsumer identifies framework-owned consumers that every
// application process hosts, such as standard authentication.
const FrameworkConsumer = "framework"

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)

// Consumer is one generated constructor field (or framework owner) that
// receives an input.
type Consumer struct {
	Service string `json:"service"`
	Field   string `json:"field,omitempty"`
}

// Input is one environment-configurable value.
type Input struct {
	Key       string `json:"key"`
	Owner     string `json:"owner"`
	Type      string `json:"type"`
	Sensitive bool   `json:"sensitive"`
	// Optional inputs may stay absent; an absent optional value reaches its
	// consumer as an unset Optional.
	Optional bool `json:"optional"`
	// Default is the declared default as contract wire JSON; nil means none.
	Default     json.RawMessage `json:"default,omitempty"`
	Constraints map[string]any  `json:"constraints,omitempty"`
	// RequiredWhenDeployable marks framework inputs that local development
	// may leave unset but a deployable environment must configure.
	RequiredWhenDeployable bool       `json:"required_when_deployable,omitempty"`
	Consumers              []Consumer `json:"consumers"`
}

// Required reports whether a candidate for the environment must configure the
// input because it has neither a default nor optional absence.
func (input Input) Required(deployable bool) bool {
	if input.RequiredWhenDeployable {
		return deployable
	}
	return !input.Optional && input.Default == nil
}

// Catalog is the complete, sorted set of configurable inputs of one compiled
// application. It carries no values beyond declared defaults.
type Catalog struct {
	Revision string  `json:"revision"`
	Inputs   []Input `json:"inputs"`
}

// Lookup returns the input with key.
func (c Catalog) Lookup(key string) (Input, bool) {
	index := sort.Search(len(c.Inputs), func(index int) bool { return c.Inputs[index].Key >= key })
	if index < len(c.Inputs) && c.Inputs[index].Key == key {
		return c.Inputs[index], true
	}
	return Input{}, false
}

// FrameworkOptions selects the framework-owned inputs an application uses.
type FrameworkOptions struct {
	StandardAuth bool
	GoogleOAuth  bool
}

// BuildCatalog derives the configuration catalog from a compiled manifest. It
// needs no secrets, services or network.
func BuildCatalog(manifest *graph.Manifest, framework FrameworkOptions) (Catalog, error) {
	var inputs []Input
	if manifest != nil {
		consumers := map[string][]Consumer{}
		for _, resource := range manifest.Resources {
			if resource.Kind != "scenery.service" {
				continue
			}
			schema, _ := resource.Spec["config_schema"].([]any)
			for _, raw := range schema {
				field, _ := raw.(map[string]any)
				name, input := stringValue(field["name"]), stringValue(field["input"])
				if input == "" || !compiler.ConfigurableDeploymentInput(stringValue(field["phase"]), stringValue(field["type"]), field["sensitive"] == true) {
					continue
				}
				key := compiler.ConfigurationKey(resource.Module, input)
				consumers[key] = append(consumers[key], Consumer{Service: resource.Address, Field: name})
			}
		}
		for _, resource := range manifest.Resources {
			if resource.Kind != "scenery.module" {
				continue
			}
			moduleInputs, err := moduleCatalogInputs(resource, consumers)
			if err != nil {
				return Catalog{}, err
			}
			inputs = append(inputs, moduleInputs...)
		}
	}
	inputs = append(inputs, frameworkInputs(framework)...)
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].Key < inputs[j].Key })
	for index := 1; index < len(inputs); index++ {
		if inputs[index].Key == inputs[index-1].Key {
			return Catalog{}, fmt.Errorf("configuration key %s is declared twice", inputs[index].Key)
		}
	}
	for index := range inputs {
		sort.Slice(inputs[index].Consumers, func(i, j int) bool {
			left, right := inputs[index].Consumers[i], inputs[index].Consumers[j]
			return left.Service+"\x00"+left.Field < right.Service+"\x00"+right.Field
		})
		if inputs[index].Consumers == nil {
			inputs[index].Consumers = []Consumer{}
		}
	}
	catalog := Catalog{Inputs: inputs}
	encoded, err := json.Marshal(inputs)
	if err != nil {
		return Catalog{}, err
	}
	sum := sha256.Sum256(encoded)
	catalog.Revision = "sha256:" + hex.EncodeToString(sum[:])
	return catalog, nil
}

func moduleCatalogInputs(module graph.Resource, consumers map[string][]Consumer) ([]Input, error) {
	instance := module.Name
	if module.Module != "" && module.Module != "app" {
		instance = module.Module + "/" + module.Name
	}
	declarations, _ := module.Spec["interface_inputs"].(map[string]any)
	bound, _ := module.Spec["inputs"].(map[string]any)
	names := make([]string, 0, len(declarations))
	for name := range declarations {
		names = append(names, name)
	}
	sort.Strings(names)
	var inputs []Input
	for _, name := range names {
		declaration, _ := declarations[name].(map[string]any)
		typeExpression := typeText(declaration["type"])
		sensitive := declaration["sensitive"] == true
		if !compiler.ConfigurableDeploymentInput(stringValue(declaration["phase"]), typeExpression, sensitive) {
			continue
		}
		value, hasValue := bound[name]
		if !hasValue {
			value, hasValue = declaration["default"], declaration["default"] != nil
		}
		if typeExpression == SecretType && hasValue {
			// A secret bound in source to a secret resource is resolved by its
			// secret store, not supplied by the environment.
			continue
		}
		key := compiler.ConfigurationKey(instance, name)
		if !keyPattern.MatchString(key) {
			return nil, fmt.Errorf("configuration key %s is not a dotted lower_snake_case name", key)
		}
		input := Input{
			Key: key, Owner: module.Address, Type: typeExpression, Sensitive: sensitive,
			Optional:  declaration["optional"] == true || strings.HasPrefix(typeExpression, "optional("),
			Consumers: consumers[key],
		}
		if hasValue && typeExpression != SecretType {
			wire, err := compiler.ConfigWireJSON(value, typeExpression)
			if err != nil {
				return nil, fmt.Errorf("configuration key %s default: %w", key, err)
			}
			canonical, err := CanonicalValue(input, wire)
			if err != nil {
				return nil, fmt.Errorf("configuration key %s default: %w", key, err)
			}
			input.Default = canonical
		}
		input.Constraints = inputConstraints(declaration)
		inputs = append(inputs, input)
	}
	return inputs, nil
}

func inputConstraints(declaration map[string]any) map[string]any {
	constraints := map[string]any{}
	for _, name := range []string{"minimum", "maximum", "min_length", "max_length", "pattern", "format", "min_items", "max_items", "unique_items"} {
		value, ok := declaration[name]
		if !ok || value == nil {
			continue
		}
		if scalar, ok := value.(map[string]any); ok && scalar["value"] != nil {
			value = scalar["value"]
		}
		constraints[name] = value
	}
	if len(constraints) == 0 {
		return nil
	}
	return constraints
}

// frameworkInputs are the framework-owned configuration inputs. Standard
// authentication keeps local development usable without them; deployable
// environments must configure the secrets it needs.
func frameworkInputs(options FrameworkOptions) []Input {
	if !options.StandardAuth {
		return nil
	}
	consumer := []Consumer{{Service: FrameworkConsumer + "/auth"}}
	owner := FrameworkConsumer + "/auth"
	inputs := []Input{
		{Key: "auth.jwt_secret", Owner: owner, Type: SecretType, Sensitive: true, Optional: true, RequiredWhenDeployable: true, Consumers: consumer},
		{Key: "auth.cookie_domain", Owner: owner, Type: "string", Optional: true, Consumers: consumer},
		{Key: "auth.email_from", Owner: owner, Type: "string", Optional: true, Consumers: consumer},
	}
	if options.GoogleOAuth {
		inputs = append(inputs,
			Input{Key: "auth.google_client_id", Owner: owner, Type: "string", Optional: true, RequiredWhenDeployable: true, Consumers: consumer},
			Input{Key: "auth.google_client_secret", Owner: owner, Type: SecretType, Sensitive: true, Optional: true, RequiredWhenDeployable: true, Consumers: consumer},
			Input{Key: "auth.token_cipher_key", Owner: owner, Type: SecretType, Sensitive: true, Optional: true, RequiredWhenDeployable: true, Consumers: consumer},
		)
	}
	return inputs
}

func typeText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		if ref, ok := typed["$ref"].(string); ok {
			return ref
		}
		if expression, ok := typed["$expression"].(string); ok {
			return strings.TrimSpace(expression)
		}
	}
	return ""
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}
