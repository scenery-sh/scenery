package compiler

import (
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	scenery "scenery.sh"
)

func validateFixtureFieldValue(value any, field map[string]any, module string, resources map[string]Resource) error {
	if err := validateFixtureValue(value, typeExpressionText(field["type"]), module, resources); err != nil {
		return err
	}
	if text, ok := value.(string); ok {
		length := int64(utf8.RuneCountInString(text))
		if minimum, ok := integerValue(field["min_length"]); ok && length < int64(minimum) {
			return fmt.Errorf("string length is less than %d", minimum)
		}
		if maximum, ok := integerValue(field["max_length"]); ok && length > int64(maximum) {
			return fmt.Errorf("string length exceeds %d", maximum)
		}
		if pattern := stringValue(field["pattern"]); pattern != "" {
			compiled, err := regexp.Compile(pattern)
			if err != nil || !compiled.MatchString(text) {
				return fmt.Errorf("string does not match pattern %q", pattern)
			}
		}
		if format := stringValue(field["format"]); format != "" {
			var err error
			switch format {
			case "uuid":
				_, err = scenery.ParseUUID(text)
			case "date":
				_, err = scenery.ParseDate(text)
			case "datetime":
				_, err = scenery.ParseDateTime(text)
			case "duration":
				_, err = scenery.ParseDuration(text)
			case "url":
				_, err = scenery.ParseURL(text)
			case "relative_path":
				_, err = scenery.ParseRelativePath(text)
			}
			if err != nil {
				return fmt.Errorf("string does not match format %q: %w", format, err)
			}
		}
	}
	if items, ok := value.([]any); ok {
		if minimum, ok := integerValue(field["min_items"]); ok && len(items) < minimum {
			return fmt.Errorf("item count is less than %d", minimum)
		}
		if maximum, ok := integerValue(field["max_items"]); ok && len(items) > maximum {
			return fmt.Errorf("item count exceeds %d", maximum)
		}
		if field["unique_items"] == true {
			seen := map[string]bool{}
			for _, item := range items {
				canonical, err := MarshalCanonical(item)
				if err != nil {
					return err
				}
				key := string(canonical)
				if seen[key] {
					return fmt.Errorf("items must be unique")
				}
				seen[key] = true
			}
		}
	}
	if number, ok := fixtureNumber(value); ok {
		if minimum, ok := fixtureNumber(field["minimum"]); ok && number.Cmp(minimum) < 0 {
			return fmt.Errorf("number is below its minimum")
		}
		if maximum, ok := fixtureNumber(field["maximum"]); ok && number.Cmp(maximum) > 0 {
			return fmt.Errorf("number exceeds its maximum")
		}
	}
	return nil
}

func validateFixtureValue(value any, typeExpression, module string, resources map[string]Resource) error {
	typeExpression = strings.TrimSpace(typeExpression)
	if inner, ok := wrappedFixtureType(typeExpression, "optional"); ok {
		if value == nil {
			return fmt.Errorf("optional values are omitted rather than null")
		}
		return validateFixtureValue(value, inner, module, resources)
	}
	if inner, ok := wrappedFixtureType(typeExpression, "nullable"); ok {
		if value == nil {
			return nil
		}
		return validateFixtureValue(value, inner, module, resources)
	}
	for _, wrapper := range []string{"list", "set"} {
		if inner, ok := wrappedFixtureType(typeExpression, wrapper); ok {
			items, isList := value.([]any)
			if !isList {
				return fmt.Errorf("%s value must be a list", wrapper)
			}
			for index, item := range items {
				if err := validateFixtureValue(item, inner, module, resources); err != nil {
					return fmt.Errorf("item %d: %w", index, err)
				}
			}
			return nil
		}
	}
	if inner, ok := wrappedFixtureType(typeExpression, "map"); ok {
		object, isObject := value.(map[string]any)
		if !isObject {
			return fmt.Errorf("map value must be an object")
		}
		for name, item := range object {
			if err := validateFixtureValue(item, inner, module, resources); err != nil {
				return fmt.Errorf("map member %s: %w", name, err)
			}
		}
		return nil
	}
	if strings.HasPrefix(typeExpression, "tuple(") && strings.HasSuffix(typeExpression, ")") {
		items, isList := value.([]any)
		arguments := splitTypeArguments(typeExpression[len("tuple(") : len(typeExpression)-1])
		if !isList || len(items) != len(arguments) {
			return fmt.Errorf("tuple value has the wrong arity")
		}
		for index := range items {
			if err := validateFixtureValue(items[index], arguments[index], module, resources); err != nil {
				return fmt.Errorf("tuple item %d: %w", index, err)
			}
		}
		return nil
	}
	address := namedFixtureTypeAddress(typeExpression, module)
	if resource := resources[address]; resource.Address != "" {
		switch resource.Kind {
		case "scenery.record":
			object, ok := value.(map[string]any)
			if !ok {
				return fmt.Errorf("record value must be an object")
			}
			fields := map[string]map[string]any{}
			for _, field := range namedChildren(resource.Spec, "field") {
				fields[stringValue(field["name"])] = field
			}
			for name := range object {
				if fields[name] == nil {
					return fmt.Errorf("record contains unknown field %s", name)
				}
			}
			for name, field := range fields {
				item, present := object[name]
				if !present {
					if !isOptionalType(field["type"]) && field["default"] == nil {
						return fmt.Errorf("record is missing required field %s", name)
					}
					continue
				}
				if err := validateFixtureFieldValue(item, field, resource.Module, resources); err != nil {
					return fmt.Errorf("field %s: %w", name, err)
				}
			}
			return nil
		case "scenery.enum":
			return validateFixtureEnumValue(value, resource)
		case "scenery.union":
			object, ok := value.(map[string]any)
			if !ok || stringValue(object[stringValue(resource.Spec["discriminator"])]) == "" {
				return fmt.Errorf("union value requires discriminator %s", stringValue(resource.Spec["discriminator"]))
			}
			return nil
		}
	}
	switch typeExpression {
	case "std.type.unit":
		object, ok := value.(map[string]any)
		if !ok || len(object) != 0 {
			return fmt.Errorf("unit value must be an empty object")
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("value must be a string")
		}
	case "bool":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("value must be a boolean")
		}
	case "int", "int32", "int64", "uint32", "uint64", "decimal", "float32", "float64":
		if _, ok := fixtureNumber(value); !ok {
			return fmt.Errorf("value must be numeric")
		}
	case "uuid", "date", "datetime", "duration", "size", "url", "relative_path", "bytes":
		scalar, _ := value.(map[string]any)
		if stringValue(scalar["$scalar"]) != typeExpression {
			return fmt.Errorf("value must be a %s", typeExpression)
		}
	case "json":
		if refString(value) != "" {
			return fmt.Errorf("JSON fixture value cannot be a resource reference")
		}
	default:
		return fmt.Errorf("unsupported fixture type %s", typeExpression)
	}
	return nil
}

func validateFixtureEnumValue(value any, enum Resource) error {
	candidate := ""
	if reference := refString(value); reference != "" {
		candidate = lastRef(reference)
	} else {
		candidate = stringValue(value)
	}
	for _, item := range namedChildren(enum.Spec, "value") {
		name := stringValue(item["name"])
		if candidate == name || candidate == wireName(item, name) {
			return nil
		}
	}
	return fmt.Errorf("value is not declared by enum %s", enum.Address)
}

func namedFixtureTypeAddress(typeExpression, module string) string {
	if strings.Contains(typeExpression, "/") {
		return typeExpression
	}
	parts := strings.Split(typeExpression, ".")
	if len(parts) != 2 {
		return ""
	}
	return resourceAddress(module, parts[0], parts[1])
}

func wrappedFixtureType(typeExpression, wrapper string) (string, bool) {
	prefix := wrapper + "("
	if !strings.HasPrefix(typeExpression, prefix) || !strings.HasSuffix(typeExpression, ")") {
		return "", false
	}
	inner := strings.TrimSpace(typeExpression[len(prefix) : len(typeExpression)-1])
	return inner, inner != ""
}

func fixtureNumber(value any) (*big.Rat, bool) {
	text := ""
	switch typed := value.(type) {
	case map[string]any:
		if kind := stringValue(typed["$scalar"]); kind == "int" || kind == "decimal" {
			text = fmt.Sprint(typed["value"])
		}
	case int:
		text = strconv.Itoa(typed)
	case int64:
		text = strconv.FormatInt(typed, 10)
	case float64:
		text = strconv.FormatFloat(typed, 'g', -1, 64)
	case string:
		text = typed
	}
	if text == "" {
		return nil, false
	}
	valueNumber, ok := new(big.Rat).SetString(text)
	return valueNumber, ok
}
