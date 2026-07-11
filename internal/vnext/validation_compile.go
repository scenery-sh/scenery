package vnext

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
	"github.com/zclconf/go-cty/cty/function/stdlib"
)

func validateRecordValidationExpression(source string, fieldTypes map[string]string) error {
	expression, diagnostics := hclsyntax.ParseExpression([]byte(source), "validation.scn", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return fmt.Errorf("parse validation expression: %s", diagnostics.Error())
	}
	if _, err := tsValidationExpression(expression); err != nil {
		return err
	}
	for _, traversal := range expression.Variables() {
		if len(traversal) < 2 || traversal.RootName() != "value" {
			return fmt.Errorf("validation expressions may read only value.<field>")
		}
		attribute, ok := traversal[1].(hcl.TraverseAttr)
		if !ok || fieldTypes[attribute.Name] == "" {
			return fmt.Errorf("validation expression references unknown field %s", traversal.SourceRange().String())
		}
	}
	fields := make(map[string]cty.Value, len(fieldTypes))
	for name, typeExpression := range fieldTypes {
		fields[name] = cty.UnknownVal(recordValidationCtyType(typeExpression))
	}
	value, diagnostics := expression.Value(recordValidationEvalContext(cty.ObjectVal(fields)))
	if diagnostics.HasErrors() {
		return fmt.Errorf("type-check validation expression: %s", diagnostics.Error())
	}
	if value.Type() != cty.Bool && value.Type() != cty.DynamicPseudoType {
		return fmt.Errorf("validation expression must produce bool")
	}
	return nil
}

func recordValidationEvalContext(value cty.Value) *hcl.EvalContext {
	return &hcl.EvalContext{
		Variables: map[string]cty.Value{"value": value},
		Functions: map[string]function.Function{
			"contains": function.New(&function.Spec{
				Params: []function.Parameter{{Name: "collection", Type: cty.DynamicPseudoType, AllowDynamicType: true, AllowUnknown: true}, {Name: "value", Type: cty.DynamicPseudoType, AllowDynamicType: true, AllowUnknown: true}},
				Type:   function.StaticReturnType(cty.Bool),
				Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
					if !args[0].IsKnown() || !args[1].IsKnown() {
						return cty.UnknownVal(cty.Bool), nil
					}
					if args[0].Type() == cty.String && args[1].Type() == cty.String {
						return cty.BoolVal(strings.Contains(args[0].AsString(), args[1].AsString())), nil
					}
					return stdlib.ContainsFunc.Call(args)
				},
			}),
			"length": function.New(&function.Spec{
				Params: []function.Parameter{{Name: "value", Type: cty.DynamicPseudoType, AllowDynamicType: true, AllowUnknown: true}},
				Type:   function.StaticReturnType(cty.Number),
				Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
					if !args[0].IsKnown() {
						return cty.UnknownVal(cty.Number), nil
					}
					if args[0].Type() == cty.String {
						return cty.NumberIntVal(int64(utf8.RuneCountInString(args[0].AsString()))), nil
					}
					return stdlib.LengthFunc.Call(args)
				},
			}),
			"starts_with": recordValidationAffix(false),
			"ends_with":   recordValidationAffix(true),
			"lower":       stdlib.LowerFunc,
			"upper":       stdlib.UpperFunc,
			"format":      stdlib.FormatFunc,
		},
	}
}

func recordValidationAffix(suffix bool) function.Function {
	return function.New(&function.Spec{
		Params: []function.Parameter{{Name: "value", Type: cty.String, AllowUnknown: true}, {Name: "affix", Type: cty.String, AllowUnknown: true}},
		Type:   function.StaticReturnType(cty.Bool),
		Impl: func(args []cty.Value, _ cty.Type) (cty.Value, error) {
			if !args[0].IsKnown() || !args[1].IsKnown() {
				return cty.UnknownVal(cty.Bool), nil
			}
			if suffix {
				return cty.BoolVal(strings.HasSuffix(args[0].AsString(), args[1].AsString())), nil
			}
			return cty.BoolVal(strings.HasPrefix(args[0].AsString(), args[1].AsString())), nil
		},
	})
}

func recordValidationCtyType(expression string) cty.Type {
	name, arguments, ok := parseTSExpression(expression)
	if ok {
		switch name {
		case "optional", "nullable":
			return recordValidationCtyType(arguments[0])
		case "list":
			return cty.List(recordValidationCtyType(arguments[0]))
		case "set":
			return cty.Set(recordValidationCtyType(arguments[0]))
		case "map":
			return cty.Map(recordValidationCtyType(arguments[0]))
		case "tuple":
			types := make([]cty.Type, len(arguments))
			for index, argument := range arguments {
				types[index] = recordValidationCtyType(argument)
			}
			return cty.Tuple(types)
		}
	}
	switch expression {
	case "bool":
		return cty.Bool
	case "int", "int32", "int64", "uint32", "uint64", "decimal", "float32", "float64", "size", "date", "datetime", "duration":
		return cty.Number
	case "string", "bytes", "uuid", "url", "relative_path":
		return cty.String
	default:
		return cty.DynamicPseudoType
	}
}
