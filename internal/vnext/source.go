package vnext

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	scenery "scenery.sh"
)

type Source struct {
	ID       string
	Path     string
	Relative string
	Bytes    []byte
	File     *hcl.File
	CST      *ConcreteSyntaxTree
	Blocks   []*Block
	External bool
}

type Block struct {
	Type            string
	Labels          []string
	Attributes      map[string]Expression
	AttributeRanges map[string]Range
	Blocks          []*Block
	Range           Range
}

type Expression struct {
	Kind      string
	Raw       string
	Value     any
	Traversal string
	Range     Range
	Static    bool
}

func parseSource(root, path string) (*Source, []Diagnostic) {
	if err := rejectPathSymlinks(root, path); err != nil {
		return nil, []Diagnostic{{Code: "SCN1001", Severity: "error", Message: err.Error()}}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	return parseSourceLogical(path, rel)
}

func parseSourceLogical(path, relative string) (*Source, []Diagnostic) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN1001", Severity: "error", Message: err.Error()}}
	}
	rel := filepath.ToSlash(relative)
	id := sourceID(rel)
	file, diags := hclsyntax.ParseConfig(b, rel, hcl.Pos{Line: 1, Column: 1})
	source := &Source{ID: id, Path: path, Relative: rel, Bytes: b, File: file}
	resultDiags := diagnosticsFromHCL(id, diags)
	var cstDiagnostics []Diagnostic
	source.CST, cstDiagnostics = buildConcreteSyntaxTree(id, rel, b, file, diags.HasErrors())
	resultDiags = append(resultDiags, cstDiagnostics...)
	if file == nil {
		return source, resultDiags
	}
	body, ok := file.Body.(*hclsyntax.Body)
	if !ok {
		return source, append(resultDiags, Diagnostic{Code: "SCN9001", Severity: "error", Message: "parser returned an unsupported body"})
	}
	for _, block := range body.Blocks {
		source.Blocks = append(source.Blocks, convertBlock(id, b, block))
	}
	return source, resultDiags
}

func convertBlock(sourceID string, source []byte, block *hclsyntax.Block) *Block {
	converted := &Block{
		Type:            block.Type,
		Labels:          append([]string(nil), block.Labels...),
		Attributes:      make(map[string]Expression, len(block.Body.Attributes)),
		AttributeRanges: make(map[string]Range, len(block.Body.Attributes)),
		Range:           convertRange(sourceID, block.Range()),
	}
	for name, attribute := range block.Body.Attributes {
		converted.Attributes[name] = convertExpression(sourceID, source, attribute.Expr)
		converted.AttributeRanges[name] = convertRange(sourceID, attribute.Range())
	}
	for _, child := range block.Body.Blocks {
		converted.Blocks = append(converted.Blocks, convertBlock(sourceID, source, child))
	}
	return converted
}

func convertExpression(sourceID string, source []byte, expr hclsyntax.Expression) Expression {
	rng := expr.Range()
	raw := ""
	if rng.Start.Byte >= 0 && rng.End.Byte <= len(source) && rng.Start.Byte <= rng.End.Byte {
		raw = string(source[rng.Start.Byte:rng.End.Byte])
	}
	converted := Expression{Kind: "expression", Raw: raw, Range: convertRange(sourceID, rng), Static: staticExpressionAllowed(expr)}
	if value, diags := expr.Value(nil); !diags.HasErrors() && value.IsWhollyKnown() {
		converted.Kind = "literal"
		converted.Value = ctyValue(value)
		converted.Static = true
		return converted
	}
	if traversal, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() {
		converted.Kind = "reference"
		converted.Traversal = traversalString(traversal)
		converted.Static = true
		return converted
	}
	if value, ok := evaluatePrimitiveConstructor(expr); ok {
		converted.Kind = "literal"
		converted.Value = value
		converted.Static = true
		return converted
	}
	if value, ok := staticCompositeValue(expr); ok {
		converted.Kind = "literal"
		converted.Value = value
		converted.Static = true
		return converted
	}
	return converted
}

func staticCompositeValue(expression hclsyntax.Expression) (any, bool) {
	if value, diagnostics := expression.Value(nil); !diagnostics.HasErrors() && value.IsWhollyKnown() {
		return ctyValue(value), true
	}
	if traversal, diagnostics := hcl.AbsTraversalForExpr(expression); !diagnostics.HasErrors() {
		return map[string]any{"$ref": traversalString(traversal)}, true
	}
	if value, ok := evaluatePrimitiveConstructor(expression); ok {
		return value, true
	}
	switch typed := expression.(type) {
	case *hclsyntax.ParenthesesExpr:
		return staticCompositeValue(typed.Expression)
	case *hclsyntax.TupleConsExpr:
		values := make([]any, 0, len(typed.Exprs))
		for _, item := range typed.Exprs {
			value, ok := staticCompositeValue(item)
			if !ok {
				if evaluated, diagnostics := item.Value(nil); !diagnostics.HasErrors() && evaluated.IsWhollyKnown() {
					value, ok = ctyValue(evaluated), true
				}
			}
			if !ok {
				return nil, false
			}
			values = append(values, value)
		}
		return values, true
	case *hclsyntax.ObjectConsExpr:
		values := make(map[string]any, len(typed.Items))
		for _, item := range typed.Items {
			keyValue, diagnostics := item.KeyExpr.Value(nil)
			if diagnostics.HasErrors() || keyValue.Type() != cty.String || !keyValue.IsKnown() {
				return nil, false
			}
			key := keyValue.AsString()
			if _, exists := values[key]; exists {
				return nil, false
			}
			value, ok := staticCompositeValue(item.ValueExpr)
			if !ok {
				if evaluated, diagnostics := item.ValueExpr.Value(nil); !diagnostics.HasErrors() && evaluated.IsWhollyKnown() {
					value, ok = ctyValue(evaluated), true
				}
			}
			if !ok {
				return nil, false
			}
			values[key] = value
		}
		return values, true
	default:
		return nil, false
	}
}

func evaluatePrimitiveConstructor(expression hclsyntax.Expression) (any, bool) {
	call, ok := expression.(*hclsyntax.FunctionCallExpr)
	if !ok || len(call.Args) != 1 {
		return nil, false
	}
	argument, diagnostics := call.Args[0].Value(nil)
	if diagnostics.HasErrors() || argument.Type() != cty.String || !argument.IsKnown() {
		return nil, false
	}
	text := argument.AsString()
	scalar := func(kind, value string) (any, bool) { return map[string]any{"$scalar": kind, "value": value}, true }
	switch call.Name {
	case "bytes_base64url":
		decoded, err := base64.RawURLEncoding.DecodeString(text)
		if err != nil {
			return nil, false
		}
		return scalar("bytes", base64.RawURLEncoding.EncodeToString(decoded))
	case "uuid":
		value, err := scenery.ParseUUID(text)
		if err != nil {
			return nil, false
		}
		return scalar("uuid", string(value))
	case "date":
		value, err := scenery.ParseDate(text)
		if err != nil {
			return nil, false
		}
		return scalar("date", string(value))
	case "datetime":
		value, err := scenery.ParseDateTime(text)
		if err != nil {
			return nil, false
		}
		return scalar("datetime", value.String())
	case "duration":
		value, err := scenery.ParseDuration(text)
		if err != nil {
			return nil, false
		}
		return map[string]any{"$scalar": "duration", "nanoseconds": value.Nanoseconds().String()}, true
	case "size":
		value, err := scenery.ParseSize(text)
		if err != nil {
			return nil, false
		}
		return map[string]any{"$scalar": "size", "bytes": value.Bytes().String()}, true
	case "url":
		value, err := scenery.ParseURL(text)
		if err != nil {
			return nil, false
		}
		return scalar("url", value.String())
	case "relative_path":
		value, err := scenery.ParseRelativePath(text)
		if err != nil {
			return nil, false
		}
		return scalar("relative_path", string(value))
	default:
		return nil, false
	}
}

func staticExpressionAllowed(expression hclsyntax.Expression) bool {
	switch typed := expression.(type) {
	case *hclsyntax.LiteralValueExpr, *hclsyntax.ScopeTraversalExpr:
		return true
	case *hclsyntax.ParenthesesExpr:
		return staticExpressionAllowed(typed.Expression)
	case *hclsyntax.RelativeTraversalExpr:
		return staticExpressionAllowed(typed.Source)
	case *hclsyntax.TupleConsExpr:
		for _, item := range typed.Exprs {
			if !staticExpressionAllowed(item) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsExpr:
		for _, item := range typed.Items {
			if !staticExpressionAllowed(item.KeyExpr) || !staticExpressionAllowed(item.ValueExpr) {
				return false
			}
		}
		return true
	case *hclsyntax.ObjectConsKeyExpr:
		return !typed.ForceNonLiteral
	case *hclsyntax.TemplateExpr:
		return len(typed.Parts) == 1 && staticExpressionAllowed(typed.Parts[0])
	case *hclsyntax.TemplateWrapExpr:
		return false
	case *hclsyntax.FunctionCallExpr:
		allowed := map[string]bool{"optional": true, "nullable": true, "list": true, "set": true, "map": true, "tuple": true, "resource_ref": true, "bytes_base64url": true, "uuid": true, "date": true, "datetime": true, "duration": true, "size": true, "url": true, "relative_path": true}
		if !allowed[typed.Name] {
			return false
		}
		for _, argument := range typed.Args {
			if !staticExpressionAllowed(argument) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func ctyValue(value cty.Value) any {
	if !value.IsKnown() || value.IsNull() {
		return nil
	}
	t := value.Type()
	switch {
	case t == cty.String:
		return value.AsString()
	case t == cty.Bool:
		return value.True()
	case t == cty.Number:
		return exactNumericScalar(value.AsBigFloat().Text('f', -1))
	case t.IsTupleType() || t.IsListType() || t.IsSetType():
		out := make([]any, 0, value.LengthInt())
		it := value.ElementIterator()
		for it.Next() {
			_, item := it.Element()
			out = append(out, ctyValue(item))
		}
		return out
	case t.IsObjectType() || t.IsMapType():
		out := map[string]any{}
		it := value.ElementIterator()
		for it.Next() {
			key, item := it.Element()
			out[key.AsString()] = ctyValue(item)
		}
		return out
	default:
		return value.GoString()
	}
}

func exactNumericScalar(text string) map[string]any {
	text = strings.TrimSpace(text)
	if text == "" || text == "-0" || text == "+0" {
		text = "0"
	}
	if !strings.Contains(text, ".") {
		negative := strings.HasPrefix(text, "-")
		digits := strings.TrimLeft(strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+"), "0")
		if digits == "" {
			return map[string]any{"$scalar": "int", "value": "0"}
		}
		if negative {
			digits = "-" + digits
		}
		return map[string]any{"$scalar": "int", "value": digits}
	}
	negative := strings.HasPrefix(text, "-")
	unsigned := strings.TrimPrefix(strings.TrimPrefix(text, "-"), "+")
	parts := strings.SplitN(unsigned, ".", 2)
	whole, fraction := parts[0], strings.TrimRight(parts[1], "0")
	if fraction == "" {
		return exactNumericScalar(func() string {
			if negative {
				return "-" + whole
			}
			return whole
		}())
	}
	coefficient := strings.TrimLeft(whole+fraction, "0")
	if coefficient == "" {
		return map[string]any{"$scalar": "decimal", "coefficient": "0", "scale": "0"}
	}
	if negative {
		coefficient = "-" + coefficient
	}
	return map[string]any{"$scalar": "decimal", "coefficient": coefficient, "scale": fmt.Sprintf("%d", len(fraction))}
}

func traversalString(traversal hcl.Traversal) string {
	var parts []string
	for _, item := range traversal {
		switch step := item.(type) {
		case hcl.TraverseRoot:
			parts = append(parts, step.Name)
		case hcl.TraverseAttr:
			parts = append(parts, step.Name)
		}
	}
	return strings.Join(parts, ".")
}

func convertRange(sourceID string, rng hcl.Range) Range {
	return Range{
		SourceID: sourceID,
		Start:    Position{Line: rng.Start.Line - 1, Column: rng.Start.Column - 1, ByteOffset: rng.Start.Byte},
		End:      Position{Line: rng.End.Line - 1, Column: rng.End.Column - 1, ByteOffset: rng.End.Byte},
	}
}

func diagnosticsFromHCL(sourceID string, diagnostics hcl.Diagnostics) []Diagnostic {
	result := make([]Diagnostic, 0, len(diagnostics))
	for _, diag := range diagnostics {
		severity := "warning"
		if diag.Severity == hcl.DiagError {
			severity = "error"
		}
		item := Diagnostic{Code: "SCN1000", Severity: severity, Message: diag.Summary + ": " + diag.Detail}
		if diag.Subject != nil {
			rng := convertRange(sourceID, *diag.Subject)
			item.Range = &rng
		}
		result = append(result, item)
	}
	return result
}

func sourceID(relative string) string {
	clean := strings.TrimSpace(filepath.ToSlash(relative))
	clean = strings.NewReplacer("/", "_", ".", "_", "-", "_").Replace(clean)
	return "src_" + clean
}

func sourceFiles(dir string, root bool) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".scn") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return nil, infoErr
		}
		if entry.Type()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("source file must be a regular non-symlink file: %s", filepath.Join(dir, entry.Name()))
		}
		if entry.Name() == "scenery.lock.scn" || entry.Name() == "scenery.migration.scn" {
			continue
		}
		if root || entry.Name() == "scenery.package.scn" || entry.Name() != "scenery.scn" {
			paths = append(paths, filepath.Join(dir, entry.Name()))
		}
	}
	sort.Strings(paths)
	return paths, nil
}

func literalString(block *Block, name string) (string, bool) {
	expr, ok := block.Attributes[name]
	if !ok || expr.Kind != "literal" {
		return "", false
	}
	value, ok := expr.Value.(string)
	return value, ok
}

func requireLiteralString(block *Block, name string) (string, error) {
	value, ok := literalString(block, name)
	if !ok || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s requires quoted %s", block.Type, name)
	}
	return value, nil
}
