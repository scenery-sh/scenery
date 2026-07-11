package vnext

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

func tsValidationDescriptors(resource Resource) []any {
	validations := namedChildren(resource.Spec, "validation")
	result := make([]any, 0, len(validations))
	for _, validation := range validations {
		source := expressionText(validation["when"])
		expression, diagnostics := hclsyntax.ParseExpression([]byte(source), "validation.scn", hcl.InitialPos)
		if diagnostics.HasErrors() {
			continue
		}
		compiled, err := tsValidationExpression(expression)
		if err != nil {
			continue
		}
		result = append(result, map[string]any{
			"name":       stringValue(validation["name"]),
			"source":     source,
			"expression": compiled,
			"code":       stringValue(validation["code"]),
			"message":    stringValue(validation["message"]),
			"path":       refString(validation["path"]),
		})
	}
	return result
}

func validationProgramJSON(source string) string {
	expression, diagnostics := hclsyntax.ParseExpression([]byte(source), "validation.scn", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return "{}"
	}
	compiled, err := tsValidationExpression(expression)
	if err != nil {
		return "{}"
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(map[string]any{"source": source, "expression": compiled})
	return strings.TrimSpace(encoded.String())
}

func tsValidationExpression(expression hclsyntax.Expression) (any, error) {
	switch typed := expression.(type) {
	case *hclsyntax.ParenthesesExpr:
		return tsValidationExpression(typed.Expression)
	case *hclsyntax.LiteralValueExpr:
		return tsValidationLiteral(typed.Val)
	case *hclsyntax.ScopeTraversalExpr:
		return tsValidationTraversal(map[string]any{"kind": "root"}, typed.Traversal)
	case *hclsyntax.RelativeTraversalExpr:
		source, err := tsValidationExpression(typed.Source)
		if err != nil {
			return nil, err
		}
		return tsValidationTraversal(source, typed.Traversal)
	case *hclsyntax.FunctionCallExpr:
		arguments := make([]any, 0, len(typed.Args))
		for _, argument := range typed.Args {
			compiled, err := tsValidationExpression(argument)
			if err != nil {
				return nil, err
			}
			arguments = append(arguments, compiled)
		}
		return map[string]any{"kind": "call", "name": strings.ToLower(typed.Name), "arguments": arguments}, nil
	case *hclsyntax.BinaryOpExpr:
		operator, ok := tsValidationBinaryOperator(typed.Op)
		if !ok {
			return nil, fmt.Errorf("unsupported validation binary operator")
		}
		left, err := tsValidationExpression(typed.LHS)
		if err != nil {
			return nil, err
		}
		right, err := tsValidationExpression(typed.RHS)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "binary", "operator": operator, "left": left, "right": right}, nil
	case *hclsyntax.UnaryOpExpr:
		operator := ""
		switch typed.Op {
		case hclsyntax.OpLogicalNot:
			operator = "!"
		case hclsyntax.OpNegate:
			operator = "-"
		}
		if operator == "" {
			return nil, fmt.Errorf("unsupported validation unary operator")
		}
		value, err := tsValidationExpression(typed.Val)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "unary", "operator": operator, "value": value}, nil
	case *hclsyntax.ConditionalExpr:
		condition, err := tsValidationExpression(typed.Condition)
		if err != nil {
			return nil, err
		}
		trueResult, err := tsValidationExpression(typed.TrueResult)
		if err != nil {
			return nil, err
		}
		falseResult, err := tsValidationExpression(typed.FalseResult)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "conditional", "condition": condition, "true_result": trueResult, "false_result": falseResult}, nil
	case *hclsyntax.TupleConsExpr:
		values := make([]any, 0, len(typed.Exprs))
		for _, value := range typed.Exprs {
			compiled, err := tsValidationExpression(value)
			if err != nil {
				return nil, err
			}
			values = append(values, compiled)
		}
		return map[string]any{"kind": "tuple", "values": values}, nil
	case *hclsyntax.ObjectConsExpr:
		entries := make([]any, 0, len(typed.Items))
		for _, item := range typed.Items {
			key, err := tsValidationExpression(item.KeyExpr)
			if err != nil {
				return nil, err
			}
			value, err := tsValidationExpression(item.ValueExpr)
			if err != nil {
				return nil, err
			}
			entries = append(entries, map[string]any{"key": key, "value": value})
		}
		return map[string]any{"kind": "object", "entries": entries}, nil
	case *hclsyntax.IndexExpr:
		collection, err := tsValidationExpression(typed.Collection)
		if err != nil {
			return nil, err
		}
		key, err := tsValidationExpression(typed.Key)
		if err != nil {
			return nil, err
		}
		return map[string]any{"kind": "index", "collection": collection, "key": key}, nil
	case *hclsyntax.TemplateExpr:
		parts := make([]any, 0, len(typed.Parts))
		for _, part := range typed.Parts {
			compiled, err := tsValidationExpression(part)
			if err != nil {
				return nil, err
			}
			parts = append(parts, compiled)
		}
		return map[string]any{"kind": "template", "parts": parts}, nil
	case *hclsyntax.TemplateWrapExpr:
		return tsValidationExpression(typed.Wrapped)
	default:
		return nil, fmt.Errorf("unsupported validation expression %T", expression)
	}
}

func tsValidationTraversal(source any, traversal hcl.Traversal) (any, error) {
	current := source
	for _, step := range traversal {
		switch typed := step.(type) {
		case hcl.TraverseRoot:
			if typed.Name != "value" {
				return nil, fmt.Errorf("unsupported validation root %q", typed.Name)
			}
			current = map[string]any{"kind": "value"}
		case hcl.TraverseAttr:
			current = map[string]any{"kind": "attribute", "source": current, "name": typed.Name}
		case hcl.TraverseIndex:
			key, err := tsValidationLiteral(typed.Key)
			if err != nil {
				return nil, err
			}
			current = map[string]any{"kind": "index", "collection": current, "key": key}
		default:
			return nil, fmt.Errorf("unsupported validation traversal %T", step)
		}
	}
	return current, nil
}

func tsValidationLiteral(value cty.Value) (any, error) {
	if value.IsNull() {
		return map[string]any{"kind": "literal", "type": "null"}, nil
	}
	switch value.Type() {
	case cty.Bool:
		return map[string]any{"kind": "literal", "type": "bool", "value": value.True()}, nil
	case cty.String:
		return map[string]any{"kind": "literal", "type": "string", "value": value.AsString()}, nil
	case cty.Number:
		number := value.AsBigFloat()
		return map[string]any{"kind": "literal", "type": "number", "value": number.Text('g', -1)}, nil
	default:
		return nil, fmt.Errorf("unsupported validation literal type %s", value.Type().FriendlyName())
	}
}

func tsValidationBinaryOperator(operation *hclsyntax.Operation) (string, bool) {
	operators := map[*hclsyntax.Operation]string{
		hclsyntax.OpLogicalOr:          "||",
		hclsyntax.OpLogicalAnd:         "&&",
		hclsyntax.OpEqual:              "==",
		hclsyntax.OpNotEqual:           "!=",
		hclsyntax.OpGreaterThan:        ">",
		hclsyntax.OpGreaterThanOrEqual: ">=",
		hclsyntax.OpLessThan:           "<",
		hclsyntax.OpLessThanOrEqual:    "<=",
		hclsyntax.OpAdd:                "+",
		hclsyntax.OpSubtract:           "-",
		hclsyntax.OpMultiply:           "*",
		hclsyntax.OpDivide:             "/",
		hclsyntax.OpModulo:             "%",
	}
	operator, ok := operators[operation]
	return operator, ok
}
