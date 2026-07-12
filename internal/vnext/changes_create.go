package vnext

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

func createResourceBlock(root string, base *Result, operation SemanticOperation) error {
	parts := strings.Split(operation.Address, "/")
	if len(parts) < 3 || !validSemanticName(parts[len(parts)-1]) {
		return fmt.Errorf("resource.create requires a canonical address")
	}
	module := strings.Join(parts[:len(parts)-2], "/")
	blockType := strings.ReplaceAll(parts[len(parts)-2], "-", "_")
	if module == "" || parts[len(parts)-2] != blockType || !validSemanticName(blockType) {
		return fmt.Errorf("resource.create requires a canonical address")
	}
	for _, part := range strings.Split(module, "/") {
		if !validSemanticName(part) {
			return fmt.Errorf("resource.create requires a canonical address")
		}
	}
	schema, ok := authoredResourceSourceSchema(blockType)
	if !ok {
		return fmt.Errorf("resource.create does not support resource kind %q", blockType)
	}
	kind := kindForBlock(blockType)
	if !resourceCreateKindSupported(kind) {
		return fmt.Errorf("capability_unavailable: resource.create schema metadata is incomplete for %s", kind)
	}
	for _, resource := range base.Manifest.Resources {
		if resource.Address == operation.Address {
			return fmt.Errorf("failed_precondition: resource already exists")
		}
	}
	spec, ok := operation.Value.(map[string]any)
	if !ok {
		return fmt.Errorf("resource.create value must be a spec object")
	}
	relative, err := resourceCreateSource(base, module)
	if err != nil {
		return err
	}
	path, err := confinedPath(root, relative)
	if err != nil {
		return fmt.Errorf("resource.create destination: %w", err)
	}
	if err := rejectPathSymlinks(root, path); err != nil {
		return fmt.Errorf("resource.create destination: %w", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, diagnostics := hclwrite.ParseConfig(b, relative, hcl.InitialPos)
	if diagnostics.HasErrors() {
		return fmt.Errorf("parse writable source: %s", diagnostics.Error())
	}
	block, err := renderAuthoredResourceBlock(blockType, []string{parts[len(parts)-1]}, spec, schema, module)
	if err != nil {
		return err
	}
	file.Body().AppendNewline()
	file.Body().AppendBlock(block)
	file.Body().AppendNewline()
	return atomicWrite(path, hclwrite.Format(file.Bytes()))
}

func resourceCreateSource(base *Result, module string) (string, error) {
	if module == "app" {
		return "scenery.scn", nil
	}
	for _, resource := range base.Manifest.Resources {
		if resource.Address != moduleResourceAddress(module) || resource.Kind != "scenery.module/v1" {
			continue
		}
		root := strings.TrimSpace(stringValue(resource.Spec["workspace_package_root"]))
		if root == "" {
			return "", fmt.Errorf("resource.create cannot write registry module %q", module)
		}
		return filepath.ToSlash(filepath.Join(root, "scenery.package.scn")), nil
	}
	return "", fmt.Errorf("resource.create module %q is not installed", module)
}

func renderAuthoredResourceBlock(blockType string, labels []string, spec map[string]any, schema *authoredBlockSchema, module string) (*hclwrite.Block, error) {
	if schema == nil {
		return nil, fmt.Errorf("resource.create schema is unavailable for %s", blockType)
	}
	if len(labels) != schema.Labels {
		return nil, fmt.Errorf("%s requires exactly %d labels", blockType, schema.Labels)
	}
	block := hclwrite.NewBlock(blockType, labels)
	keys := make([]string, 0, len(spec))
	for key := range spec {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		value := spec[key]
		if child, ok := schema.Children[key]; ok {
			children, err := authoredChildValues(key, value, child)
			if err != nil {
				return nil, err
			}
			for _, value := range children {
				childLabels, childSpec, err := authoredChildBlockValue(key, value, child.Schema)
				if err != nil {
					return nil, err
				}
				rendered, err := renderAuthoredResourceBlock(key, childLabels, childSpec, child.Schema, module)
				if err != nil {
					return nil, err
				}
				block.Body().AppendBlock(rendered)
				block.Body().AppendNewline()
			}
			continue
		}
		attribute, allowed := schema.Attributes[key]
		if !allowed && !schema.AllowUnknownAttributes {
			return nil, fmt.Errorf("unknown field %q for schema %s", key, schema.Revision)
		}
		if allowed && attribute.UnsupportedDraft != "" {
			return nil, fmt.Errorf("capability_unavailable: field %q requires unsupported draft capability %s", key, attribute.UnsupportedDraft)
		}
		if allowed {
			if err := validateAuthoredMutationValue(attribute, value); err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
		}
		if schema.AllowUnknownAttributes && !validSemanticName(key) {
			return nil, fmt.Errorf("dynamic field %q must be lower_snake_case", key)
		}
		tokens, err := changeTokensForModule(value, module)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		block.Body().SetAttributeRaw(key, tokens)
	}
	return block, nil
}

func validateAuthoredMutationValue(attribute authoredAttributeSchema, value any) error {
	collection, _ := attribute.Type["collection"].(string)
	if collection != "list" && collection != "set" {
		return nil
	}
	values, ok := value.([]any)
	if !ok {
		return fmt.Errorf("must be a %s", collection)
	}
	if minimum, ok := integerValue(attribute.Constraints["min_items"]); ok && len(values) < minimum {
		return fmt.Errorf("must contain at least %d item(s)", minimum)
	}
	items, _ := attribute.Type["items"].(map[string]any)
	if items["typed_reference"] == nil {
		return nil
	}
	for index, value := range values {
		reference, ok := value.(map[string]any)
		if !ok || (strings.TrimSpace(stringValue(reference["$ref"])) == "" && strings.TrimSpace(stringValue(reference["$expression"])) == "") {
			return fmt.Errorf("item %d must be a typed reference", index)
		}
		if attribute.Constraints["reference_shape"] == "direct_input_field" {
			if _, ok := inputKeyFieldName(reference); !ok {
				return fmt.Errorf("item %d must reference a direct input field", index)
			}
		}
	}
	return nil
}

func authoredChildValues(name string, value any, child authoredChildSchema) ([]map[string]any, error) {
	var values []map[string]any
	switch typed := value.(type) {
	case map[string]any:
		values = []map[string]any{typed}
	case []any:
		for _, item := range typed {
			object, ok := item.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("%s must contain block objects", name)
			}
			values = append(values, object)
		}
	default:
		return nil, fmt.Errorf("%s must be a block object or array", name)
	}
	if !child.Repeatable && len(values) != 1 {
		return nil, fmt.Errorf("%s is a singleton block", name)
	}
	if child.Repeatable && !child.Ordered {
		sort.SliceStable(values, func(i, j int) bool {
			if child.Schema.Labels > 0 {
				left, _ := values[i]["name"].(string)
				right, _ := values[j]["name"].(string)
				if left != right {
					return left < right
				}
			}
			left, _ := MarshalCanonical(values[i])
			right, _ := MarshalCanonical(values[j])
			return string(left) < string(right)
		})
	}
	return values, nil
}

func authoredChildBlockValue(name string, value map[string]any, schema *authoredBlockSchema) ([]string, map[string]any, error) {
	spec := cloneMapValue(value)
	if schema.Labels == 0 {
		return nil, spec, nil
	}
	if schema.Labels != 1 {
		return nil, nil, fmt.Errorf("%s uses unsupported label arity %d", name, schema.Labels)
	}
	label, ok := spec["name"].(string)
	if !ok || !validAuthoredLabel(schema, label) {
		return nil, nil, fmt.Errorf("%s block label %q violates %s policy", name, label, schema.LabelPolicy)
	}
	delete(spec, "name")
	return []string{label}, spec, nil
}

func changeTokensForModule(value any, module string) (hclwrite.Tokens, error) {
	if object, ok := value.(map[string]any); ok {
		if reference, ok := object["$ref"].(string); ok {
			var err error
			reference, err = authoredReference(reference, module)
			if err != nil {
				return nil, err
			}
			traversal, diagnostics := hclsyntax.ParseTraversalAbs([]byte(reference), "change", hcl.InitialPos)
			if diagnostics.HasErrors() {
				return nil, fmt.Errorf("invalid reference %q", reference)
			}
			return hclwrite.TokensForTraversal(traversal), nil
		}
		if scalar, ok := object["$scalar"].(string); ok {
			return exactScalarTokens(scalar, object)
		}
		if expression, ok := object["$expression"].(string); ok {
			return authoredExpressionTokens(expression)
		}
		keys := make([]string, 0, len(object))
		for key := range object {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		attributes := make([]hclwrite.ObjectAttrTokens, 0, len(keys))
		for _, key := range keys {
			tokens, err := changeTokensForModule(object[key], module)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", key, err)
			}
			name := hclwrite.TokensForValue(cty.StringVal(key))
			if hclsyntax.ValidIdentifier(key) {
				name = hclwrite.TokensForIdentifier(key)
			}
			attributes = append(attributes, hclwrite.ObjectAttrTokens{Name: name, Value: tokens})
		}
		return hclwrite.TokensForObject(attributes), nil
	}
	if values, ok := value.([]any); ok {
		elements := make([]hclwrite.Tokens, 0, len(values))
		for index, value := range values {
			tokens, err := changeTokensForModule(value, module)
			if err != nil {
				return nil, fmt.Errorf("item %d: %w", index, err)
			}
			elements = append(elements, tokens)
		}
		return hclwrite.TokensForTuple(elements), nil
	}
	converted, err := changeValue(value)
	if err != nil {
		return nil, err
	}
	return hclwrite.TokensForValue(converted), nil
}

func authoredExpressionTokens(expression string) (hclwrite.Tokens, error) {
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("expression is empty")
	}
	if _, diagnostics := hclsyntax.ParseExpression([]byte(expression), "change", hcl.InitialPos); diagnostics.HasErrors() {
		return nil, fmt.Errorf("invalid expression %q", expression)
	}
	file, diagnostics := hclwrite.ParseConfig([]byte("value = "+expression+"\n"), "change", hcl.InitialPos)
	if diagnostics.HasErrors() {
		return nil, fmt.Errorf("invalid expression %q", expression)
	}
	attribute := file.Body().GetAttribute("value")
	if attribute == nil {
		return nil, fmt.Errorf("invalid expression %q", expression)
	}
	return attribute.Expr().BuildTokens(nil), nil
}

func authoredReference(reference, module string) (string, error) {
	if !strings.Contains(reference, "/") {
		return reference, nil
	}
	parts := strings.Split(reference, "/")
	moduleParts := strings.Split(module, "/")
	if module == "" || len(parts) < len(moduleParts)+2 {
		return "", fmt.Errorf("invalid canonical reference %q", reference)
	}
	referenceModule := strings.Join(parts[:len(moduleParts)], "/")
	if referenceModule != module {
		return "", fmt.Errorf("canonical reference %q is not source-addressable from module %q; use a typed module input or export", reference, module)
	}
	return strings.Join(parts[len(moduleParts):], "."), nil
}

func exactScalarTokens(kind string, value map[string]any) (hclwrite.Tokens, error) {
	constructor := func(name, field string) (hclwrite.Tokens, error) {
		text, ok := value[field].(string)
		if !ok {
			return nil, fmt.Errorf("%s scalar requires string field %s", kind, field)
		}
		return hclwrite.TokensForFunctionCall(name, hclwrite.TokensForValue(cty.StringVal(text))), nil
	}
	switch kind {
	case "int":
		text, ok := value["value"].(string)
		if !ok {
			return nil, fmt.Errorf("int scalar requires string field value")
		}
		if _, valid := new(big.Int).SetString(text, 10); !valid {
			return nil, fmt.Errorf("invalid int scalar %q", text)
		}
		number, err := cty.ParseNumberVal(text)
		if err != nil {
			return nil, fmt.Errorf("invalid int scalar %q", text)
		}
		return hclwrite.TokensForValue(number), nil
	case "decimal":
		coefficient, coefficientOK := value["coefficient"].(string)
		scaleText, scaleOK := value["scale"].(string)
		scale, err := strconv.Atoi(scaleText)
		if !coefficientOK || !scaleOK || err != nil || scale < 0 {
			return nil, fmt.Errorf("decimal scalar requires canonical coefficient and scale")
		}
		text, err := decimalSourceLiteral(coefficient, scale)
		if err != nil {
			return nil, err
		}
		number, err := cty.ParseNumberVal(text)
		if err != nil {
			return nil, fmt.Errorf("invalid decimal scalar")
		}
		return hclwrite.TokensForValue(number), nil
	case "duration":
		nanoseconds, ok := value["nanoseconds"].(string)
		if !ok {
			return nil, fmt.Errorf("duration scalar requires string field nanoseconds")
		}
		if _, valid := new(big.Int).SetString(nanoseconds, 10); !valid {
			return nil, fmt.Errorf("invalid duration nanoseconds %q", nanoseconds)
		}
		return hclwrite.TokensForFunctionCall("duration", hclwrite.TokensForValue(cty.StringVal(nanoseconds+"ns"))), nil
	case "size":
		bytes, ok := value["bytes"].(string)
		parsed, valid := new(big.Int).SetString(bytes, 10)
		if !ok || !valid || parsed.Sign() < 0 {
			return nil, fmt.Errorf("size scalar requires non-negative string field bytes")
		}
		return hclwrite.TokensForFunctionCall("size", hclwrite.TokensForValue(cty.StringVal(bytes+"B"))), nil
	case "bytes":
		return constructor("bytes_base64url", "base64url")
	case "uuid", "date", "datetime", "url", "relative_path":
		return constructor(kind, "value")
	case "json":
		converted, err := changeValue(value["value"])
		if err != nil {
			return nil, err
		}
		return hclwrite.TokensForValue(converted), nil
	default:
		return nil, fmt.Errorf("unsupported exact scalar %q", kind)
	}
}

func decimalSourceLiteral(coefficient string, scale int) (string, error) {
	integer, ok := new(big.Int).SetString(coefficient, 10)
	if !ok {
		return "", fmt.Errorf("invalid decimal coefficient %q", coefficient)
	}
	negative := integer.Sign() < 0
	digits := new(big.Int).Abs(integer).String()
	if scale == 0 {
		if negative {
			return "-" + digits, nil
		}
		return digits, nil
	}
	if len(digits) <= scale {
		digits = strings.Repeat("0", scale-len(digits)+1) + digits
	}
	text := digits[:len(digits)-scale] + "." + digits[len(digits)-scale:]
	if negative {
		text = "-" + text
	}
	return text, nil
}
