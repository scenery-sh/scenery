package vnext

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

type Source struct {
	ID       string
	Path     string
	Relative string
	Bytes    []byte
	File     *hcl.File
	Blocks   []*Block
}

type Block struct {
	Type       string
	Labels     []string
	Attributes map[string]Expression
	Blocks     []*Block
	Range      Range
}

type Expression struct {
	Kind      string
	Raw       string
	Value     any
	Traversal string
	Range     Range
}

func parseSource(root, path string) (*Source, []Diagnostic) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, []Diagnostic{{Code: "SCN1001", Severity: "error", Message: err.Error()}}
	}
	rel, err := filepath.Rel(root, path)
	if err != nil {
		rel = filepath.Base(path)
	}
	rel = filepath.ToSlash(rel)
	id := sourceID(rel)
	file, diags := hclsyntax.ParseConfig(b, rel, hcl.Pos{Line: 1, Column: 1})
	source := &Source{ID: id, Path: path, Relative: rel, Bytes: b, File: file}
	resultDiags := diagnosticsFromHCL(id, diags)
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
		Type:       block.Type,
		Labels:     append([]string(nil), block.Labels...),
		Attributes: make(map[string]Expression, len(block.Body.Attributes)),
		Range:      convertRange(sourceID, block.Range()),
	}
	for name, attribute := range block.Body.Attributes {
		converted.Attributes[name] = convertExpression(sourceID, source, attribute.Expr)
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
	converted := Expression{Kind: "expression", Raw: raw, Range: convertRange(sourceID, rng)}
	if traversal, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() {
		converted.Kind = "reference"
		converted.Traversal = traversalString(traversal)
		return converted
	}
	if value, diags := expr.Value(nil); !diags.HasErrors() && value.IsWhollyKnown() {
		converted.Kind = "literal"
		converted.Value = ctyValue(value)
	}
	return converted
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
		return value.AsBigFloat().Text('f', -1)
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
