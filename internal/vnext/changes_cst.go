package vnext

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type semanticSourceTarget struct {
	attribute *hclsyntax.Attribute
	body      *hclsyntax.Body
	object    []string
	insert    hcl.Pos
}

func mutateResourceValue(root string, base *Result, resource Resource, operation SemanticOperation) error {
	parts := pointerParts(operation.Path)
	if len(parts) < 2 || parts[0] != "spec" {
		return fmt.Errorf("semantic value path must begin with /spec")
	}
	source := sourceByID(base.Sources, resource.Origin.SourceID)
	if source == nil {
		return fmt.Errorf("source for %s not found", resource.Address)
	}
	path := filepath.Join(root, filepath.FromSlash(source.Relative))
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	file, diagnostics := hclsyntax.ParseConfig(data, source.Relative, hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() || file == nil {
		return fmt.Errorf("parse writable source: %s", diagnostics.Error())
	}
	body := file.Body.(*hclsyntax.Body)
	resourceBlock := syntaxResourceBlock(body, resource)
	if resourceBlock == nil {
		return fmt.Errorf("source block for %s not found", resource.Address)
	}
	target, found := locateSemanticSourceTarget(resourceBlock.Body, parts[1:], resourceBlock.CloseBraceRange.Start)
	if !found {
		return fmt.Errorf("semantic path %s is not writable", operation.Path)
	}
	switch {
	case target.attribute == nil:
		if operation.Op != "value.set" || len(target.object) != 1 {
			return fmt.Errorf("semantic path %s does not exist", operation.Path)
		}
		if blockValue, ok := operation.Value.(map[string]any); ok && semanticSingularBlockField(resource.Kind, target.object[0]) {
			blockBytes, err := semanticBlockBytes(target.object[0], blockValue)
			if err != nil {
				return err
			}
			data, err = insertBodyBlock(data, target.insert, blockBytes)
			if err != nil {
				return err
			}
			break
		}
		tokens, err := changeTokens(operation.Value)
		if err != nil {
			return err
		}
		data, err = insertBodyAttribute(data, target.insert, target.object[0], tokens.Bytes())
		if err != nil {
			return err
		}
	case len(target.object) == 0:
		if operation.Op == "value.unset" {
			data = removeSourceRangeLine(data, target.attribute.Range())
		} else {
			tokens, err := changeTokens(operation.Value)
			if err != nil {
				return err
			}
			rng := target.attribute.Expr.Range()
			data = replaceSourceRange(data, rng, tokens.Bytes())
		}
	default:
		data, err = mutateObjectExpression(data, target.attribute.Expr, target.object, operation)
		if err != nil {
			return err
		}
	}
	formatted, err := canonicalFormatSource(data, source.Relative)
	if err != nil {
		return err
	}
	return atomicWrite(path, formatted)
}

func semanticSingularBlockField(kind, name string) bool {
	fields := map[string]map[string]bool{
		"scenery.service/v1":          {"implementation": true, "config": true, "lifecycle": true},
		"scenery.operation/v1":        {"handler": true, "idempotency": true},
		"scenery.execution/v1":        {"retry": true, "concurrency": true, "retention": true, "deduplication": true},
		"scenery.binding/v1":          {"http": true, "internal": true, "cli": true, "event": true},
		"scenery.schedule/v1":         {"trigger": true, "invoke": true, "catchup": true},
		"scenery.data-source/v1":      {"config": true},
		"scenery.execution-engine/v1": {"config": true},
		"scenery.event-bus/v1":        {"config": true},
		"scenery.secret-store/v1":     {"config": true},
		"scenery.authentication/v1":   {"config": true},
		"scenery.renderer/v1":         {"config": true},
		"scenery.view/v1":             {"input": true, "result": true, "implementation": true},
		"scenery.page/v1":             {"load": true},
	}
	return fields[kind][name]
}

func semanticBlockBytes(name string, value map[string]any) ([]byte, error) {
	block := hclwrite.NewBlock(name, nil)
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		tokens, err := changeTokens(value[key])
		if err != nil {
			return nil, err
		}
		block.Body().SetAttributeRaw(key, tokens)
	}
	return block.BuildTokens(nil).Bytes(), nil
}

func syntaxResourceBlock(body *hclsyntax.Body, resource Resource) *hclsyntax.Block {
	blockType := strings.ReplaceAll(strings.TrimPrefix(strings.TrimSuffix(resource.Kind, "/v1"), "scenery."), "-", "_")
	for _, block := range body.Blocks {
		if block.Type == blockType && len(block.Labels) == 1 && block.Labels[0] == resource.Name {
			return block
		}
	}
	return nil
}

func locateSemanticSourceTarget(body *hclsyntax.Body, parts []string, insert hcl.Pos) (semanticSourceTarget, bool) {
	if len(parts) == 0 {
		return semanticSourceTarget{}, false
	}
	if attribute := body.Attributes[parts[0]]; attribute != nil {
		return semanticSourceTarget{attribute: attribute, body: body, object: append([]string(nil), parts[1:]...), insert: insert}, true
	}
	var candidates []*hclsyntax.Block
	for _, block := range body.Blocks {
		if block.Type == parts[0] {
			candidates = append(candidates, block)
		}
	}
	if len(candidates) == 0 {
		if len(parts) == 1 {
			return semanticSourceTarget{body: body, object: append([]string(nil), parts...), insert: insert}, true
		}
		return semanticSourceTarget{}, false
	}
	consumed := 1
	selected := candidates[0]
	if len(parts) > 1 {
		for _, candidate := range candidates {
			if len(candidate.Labels) > 0 && candidate.Labels[0] == parts[1] {
				selected, consumed = candidate, 2
				break
			}
		}
	}
	if len(candidates) > 1 && consumed == 1 {
		return semanticSourceTarget{}, false
	}
	return locateSemanticSourceTarget(selected.Body, parts[consumed:], selected.CloseBraceRange.Start)
}

func mutateObjectExpression(data []byte, expression hclsyntax.Expression, parts []string, operation SemanticOperation) ([]byte, error) {
	object := unwrapObjectExpression(expression)
	if object == nil {
		return nil, fmt.Errorf("semantic path traverses a non-object expression")
	}
	for _, item := range object.Items {
		key, diagnostics := item.KeyExpr.Value(nil)
		if diagnostics.HasErrors() || key.Type() != cty.String || key.AsString() != parts[0] {
			continue
		}
		if len(parts) > 1 {
			return mutateObjectExpression(data, item.ValueExpr, parts[1:], operation)
		}
		if operation.Op == "value.unset" {
			start := item.KeyExpr.Range().Start.Byte
			end := item.ValueExpr.Range().End.Byte
			return removeSourceOffsetsLine(data, start, end), nil
		}
		tokens, err := changeTokens(operation.Value)
		if err != nil {
			return nil, err
		}
		return replaceSourceRange(data, item.ValueExpr.Range(), tokens.Bytes()), nil
	}
	if len(parts) != 1 || operation.Op != "value.set" {
		return nil, fmt.Errorf("semantic object path does not exist")
	}
	tokens, err := changeTokens(operation.Value)
	if err != nil {
		return nil, err
	}
	return insertObjectItem(data, object, parts[0], tokens.Bytes())
}

func unwrapObjectExpression(expression hclsyntax.Expression) *hclsyntax.ObjectConsExpr {
	for {
		switch typed := expression.(type) {
		case *hclsyntax.ObjectConsExpr:
			return typed
		case *hclsyntax.ParenthesesExpr:
			expression = typed.Expression
		default:
			return nil
		}
	}
}

func replaceSourceRange(data []byte, rng hcl.Range, value []byte) []byte {
	return append(append(append([]byte(nil), data[:rng.Start.Byte]...), value...), data[rng.End.Byte:]...)
}

func removeSourceRangeLine(data []byte, rng hcl.Range) []byte {
	return removeSourceOffsetsLine(data, rng.Start.Byte, rng.End.Byte)
}

func removeSourceOffsetsLine(data []byte, start, end int) []byte {
	lineStart := start
	for lineStart > 0 && data[lineStart-1] != '\n' {
		lineStart--
	}
	if strings.TrimSpace(string(data[lineStart:start])) == "" {
		start = lineStart
	}
	for end < len(data) && data[end] != '\n' {
		end++
	}
	if end < len(data) {
		end++
	}
	return append(append([]byte(nil), data[:start]...), data[end:]...)
}

func insertBodyAttribute(data []byte, insert hcl.Pos, name string, value []byte) ([]byte, error) {
	offset := insert.Byte
	if offset < 0 || offset > len(data) {
		return nil, fmt.Errorf("body insertion range is invalid")
	}
	indent := strings.Repeat(" ", max(0, insert.Column-1)+2)
	insertion := []byte(indent + name + " = " + string(value) + "\n")
	if offset > 0 && data[offset-1] != '\n' {
		insertion = append([]byte("\n"), insertion...)
	}
	return append(append(append([]byte(nil), data[:offset]...), insertion...), data[offset:]...), nil
}

func insertBodyBlock(data []byte, insert hcl.Pos, block []byte) ([]byte, error) {
	offset := insert.Byte
	if offset < 0 || offset > len(data) {
		return nil, fmt.Errorf("body insertion range is invalid")
	}
	indent := strings.Repeat(" ", max(0, insert.Column-1)+2)
	lines := strings.Split(strings.TrimSuffix(string(block), "\n"), "\n")
	for index := range lines {
		lines[index] = indent + lines[index]
	}
	insertion := []byte(strings.Join(lines, "\n") + "\n")
	if offset > 0 && data[offset-1] != '\n' {
		insertion = append([]byte("\n"), insertion...)
	}
	return append(append(append([]byte(nil), data[:offset]...), insertion...), data[offset:]...), nil
}

func insertObjectItem(data []byte, object *hclsyntax.ObjectConsExpr, name string, value []byte) ([]byte, error) {
	rng := object.Range()
	offset := rng.End.Byte - 1
	if offset < rng.Start.Byte || offset > len(data) {
		return nil, fmt.Errorf("object insertion range is invalid")
	}
	indent := strings.Repeat(" ", max(0, rng.Start.Column-1)+2)
	insertion := []byte(indent + name + " = " + string(value) + "\n")
	if offset > 0 && data[offset-1] != '\n' {
		insertion = append([]byte("\n"), insertion...)
	}
	return append(append(append([]byte(nil), data[:offset]...), insertion...), data[offset:]...), nil
}
