package scn

import (
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

type FormatResult struct {
	Changed []string `json:"changed"`
}

func Format(root string, check bool) (FormatResult, error) {
	return FormatPaths(root, nil, check)
}

func FormatPaths(root string, selected []string, check bool) (FormatResult, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return FormatResult{}, err
	}
	paths, err := formattingPaths(absRoot, selected)
	if err != nil {
		return FormatResult{}, err
	}
	if len(selected) > 0 {
		return formatSourcePaths(absRoot, paths, check)
	}
	packagePaths, packageErr := localModuleFormattingPaths(absRoot, paths)
	if packageErr != nil {
		return FormatResult{}, packageErr
	}
	paths = append(paths, packagePaths...)
	return formatSourcePaths(absRoot, paths, check)
}

func formattingPaths(root string, selected []string) ([]string, error) {
	if len(selected) == 0 {
		paths, err := SourceFiles(root, true)
		if err != nil {
			return nil, err
		}
		return paths, nil
	}
	var paths []string
	for _, item := range selected {
		path := item
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		path = filepath.Clean(path)
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("format path %q escapes app root", item)
		}
		if err := RejectPathSymlinks(root, path); err != nil {
			return nil, fmt.Errorf("format path %q: %w", item, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			directoryPaths, err := SourceFiles(path, true)
			if err != nil {
				return nil, err
			}
			paths = append(paths, directoryPaths...)
			continue
		}
		if filepath.Ext(path) != ".scn" {
			return nil, fmt.Errorf("format path %q is not Scenery source", item)
		}
		if legacyErr := legacyFilenameError(path); legacyErr != nil {
			return nil, legacyErr
		}
		paths = append(paths, path)
	}
	return paths, nil
}

func formatSourcePaths(root string, paths []string, check bool) (FormatResult, error) {
	seen := map[string]bool{}
	result := FormatResult{Changed: []string{}}
	for _, path := range paths {
		if seen[path] {
			continue
		}
		seen[path] = true
		if err := RejectPathSymlinks(root, path); err != nil {
			return result, fmt.Errorf("format source: %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return result, err
		}
		before, err := os.ReadFile(path)
		if err != nil {
			return result, err
		}
		after, err := CanonicalFormat(before, filepath.ToSlash(path))
		if err != nil {
			return result, err
		}
		if string(before) == string(after) {
			continue
		}
		rel, _ := filepath.Rel(root, path)
		result.Changed = append(result.Changed, filepath.ToSlash(rel))
		if check {
			continue
		}
		tmp := path + ".scenery-fmt-tmp"
		if err := os.WriteFile(tmp, after, info.Mode().Perm()); err != nil {
			return result, err
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			return result, err
		}
	}
	if check && len(result.Changed) > 0 {
		return result, fmt.Errorf("%d Scenery source files require formatting", len(result.Changed))
	}
	return result, nil
}

func localModuleFormattingPaths(root string, rootPaths []string) ([]string, error) {
	queue := append([]string(nil), rootPaths...)
	seenDirectories := map[string]bool{filepath.Clean(root): true}
	var result []string
	for len(queue) > 0 {
		path := queue[0]
		queue = queue[1:]
		source, diagnostics := Parse(root, path)
		if syntaxHasErrors(diagnostics) || source == nil {
			continue
		}
		callerDirectory := filepath.Dir(path)
		for _, block := range source.Blocks {
			if block.Type != "module" {
				continue
			}
			moduleSource, ok := LiteralString(block, "source")
			if !ok || filepath.IsAbs(moduleSource) {
				continue
			}
			directory := filepath.Clean(filepath.Join(callerDirectory, filepath.FromSlash(moduleSource)))
			if !PathWithin(root, directory) {
				return nil, fmt.Errorf("module source %q escapes app root", moduleSource)
			}
			if seenDirectories[directory] {
				continue
			}
			info, err := os.Lstat(directory)
			if err != nil {
				continue
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, fmt.Errorf("module source %q contains a symlink", moduleSource)
			}
			if !info.IsDir() {
				continue
			}
			if err := RejectPathSymlinks(root, directory); err != nil {
				return nil, fmt.Errorf("module source %q: %w", moduleSource, err)
			}
			seenDirectories[directory] = true
			paths, err := SourceFiles(directory, false)
			if err != nil {
				return nil, err
			}
			result = append(result, paths...)
			queue = append(queue, paths...)
		}
	}
	return result, nil
}

func syntaxHasErrors(diagnostics []Diagnostic) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			return true
		}
	}
	return false
}

type formatReplacement struct {
	start int
	end   int
	value []byte
}

func CanonicalFormat(source []byte, filename string) ([]byte, error) {
	source = canonicalizeCommentTokens(source, filename)
	formatted := hclwrite.Format(source)
	file, diagnostics := hclsyntax.ParseConfig(formatted, filename, hcl.Pos{Line: 1, Column: 1})
	if diagnostics.HasErrors() || file == nil {
		return nil, fmt.Errorf("cannot format invalid Scenery source: %s", diagnostics.Error())
	}
	body := file.Body.(*hclsyntax.Body)
	if err := validateFormatterBlockLabels(body, ""); err != nil {
		return nil, err
	}
	var replacements []formatReplacement
	collectContextualFormatReplacements(formatted, NewPositionIndex(formatted), body, nil, &replacements)
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		if replacement.start < 0 || replacement.end < replacement.start || replacement.end > len(formatted) {
			return nil, fmt.Errorf("formatter replacement is outside source")
		}
		formatted = append(append(append([]byte(nil), formatted[:replacement.start]...), replacement.value...), formatted[replacement.end:]...)
	}
	return hclwrite.Format(formatted), nil
}

func validateFormatterBlockLabels(body *hclsyntax.Body, parent string) error {
	for _, block := range body.Blocks {
		if schema, ok := formatterSchemaForBlock(parent, block.Type); ok && len(block.Labels) == schema.Labels {
			for _, label := range block.Labels {
				if !validFormatterLabel(schema, label) {
					return fmt.Errorf("cannot format %s label %q: violates %s policy", block.Type, label, schema.LabelPolicy)
				}
			}
		}
		if err := validateFormatterBlockLabels(block.Body, block.Type); err != nil {
			return err
		}
	}
	return nil
}

func canonicalizeCommentTokens(source []byte, filename string) []byte {
	tokens, _ := hclsyntax.LexConfig(source, filename, hcl.Pos{Line: 1, Column: 1})
	var replacements []formatReplacement
	for _, token := range tokens {
		if token.Type != hclsyntax.TokenComment {
			continue
		}
		trimmed := strings.TrimSpace(string(token.Bytes))
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		value := canonicalComment(string(token.Bytes), token.Range.Start.Column-1)
		replacements = append(replacements, formatReplacement{start: token.Range.Start.Byte, end: token.Range.Start.Byte + len(token.Bytes), value: []byte(value)})
	}
	sort.Slice(replacements, func(i, j int) bool { return replacements[i].start > replacements[j].start })
	for _, replacement := range replacements {
		source = append(append(append([]byte(nil), source[:replacement.start]...), replacement.value...), source[replacement.end:]...)
	}
	return source
}

func canonicalComment(comment string, indentation int) string {
	lineEnding := ""
	if strings.HasSuffix(comment, "\r\n") {
		lineEnding = "\r\n"
	} else if strings.HasSuffix(comment, "\n") {
		lineEnding = "\n"
	}
	trimmed := strings.TrimSpace(comment)
	if strings.HasPrefix(trimmed, "//") {
		content := strings.TrimSpace(strings.TrimPrefix(trimmed, "//"))
		if content == "" {
			return "#" + lineEnding
		}
		return "# " + content + lineEnding
	}
	trimmed = strings.TrimPrefix(strings.TrimSuffix(trimmed, "*/"), "/*")
	lines := strings.Split(strings.ReplaceAll(trimmed, "\r\n", "\n"), "\n")
	prefix := strings.Repeat(" ", max(0, indentation))
	for index, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "*"))
		if line == "" {
			lines[index] = "#"
		} else {
			lines[index] = "# " + line
		}
		if index > 0 {
			lines[index] = prefix + lines[index]
		}
	}
	return strings.Join(lines, "\n") + lineEnding
}

func collectContextualFormatReplacements(source []byte, positions *PositionIndex, body *hclsyntax.Body, ancestors []string, replacements *[]formatReplacement) {
	for _, block := range body.Blocks {
		path := append(append([]string(nil), ancestors...), block.Type)
		for name, attribute := range block.Body.Attributes {
			expected := formatterExpectedPrimitive(source, positions, block, path, name)
			if expected == "" {
				continue
			}
			expression := ConvertExpression("format", source, positions, attribute.Expr)
			value := expressionValue(expression)
			canonical, ok := canonicalPrimitiveSourceLiteral(value, expected)
			if !ok {
				continue
			}
			rng := attribute.Expr.Range()
			*replacements = append(*replacements, formatReplacement{start: rng.Start.Byte, end: rng.End.Byte, value: canonical})
		}
		collectContextualFormatReplacements(source, positions, block.Body, path, replacements)
	}
}

func formatterExpectedPrimitive(source []byte, positions *PositionIndex, block *hclsyntax.Block, path []string, attribute string) string {
	if attribute == "default" && (block.Type == "field" || block.Type == "input" || block.Type == "config_schema") {
		if typeAttribute := block.Body.Attributes["type"]; typeAttribute != nil {
			expression := ConvertExpression("format", source, positions, typeAttribute.Expr)
			typeName := typeExpressionText(expressionValue(expression))
			for _, wrapper := range []string{"optional", "nullable"} {
				prefix := wrapper + "("
				if strings.HasPrefix(typeName, prefix) && strings.HasSuffix(typeName, ")") {
					typeName = strings.TrimSpace(typeName[len(prefix) : len(typeName)-1])
				}
			}
			if contextualPrimitiveTypes[typeName] {
				return typeName
			}
		}
	}
	parent := ""
	if len(path) > 1 {
		parent = path[len(path)-2]
	}
	switch {
	case block.Type == "execution" && (attribute == "timeout" || attribute == "lease"):
		return "duration"
	case block.Type == "retry" && (attribute == "initial" || attribute == "maximum"):
		return "duration"
	case block.Type == "retention" && (attribute == "success" || attribute == "failure"):
		return "duration"
	case block.Type == "deduplication" && attribute == "retention":
		return "duration"
	case block.Type == "trigger" && attribute == "every":
		return "duration"
	case block.Type == "trigger" && attribute == "at":
		return "datetime"
	case block.Type == "catchup" && attribute == "maximum_age":
		return "duration"
	case block.Type == "timeouts" && (attribute == "read" || attribute == "write" || attribute == "idle" || attribute == "total_invocation"):
		return "duration"
	case block.Type == "typescript_client" && attribute == "output_root":
		return "relative_path"
	case block.Type == "implementation" && parent == "view" && attribute == "file":
		return "relative_path"
	case block.Type == "renderer" && attribute == "module":
		return "relative_path"
	}
	return ""
}

func expressionValue(expression Expression) any {
	switch expression.Kind {
	case "reference":
		return map[string]any{"$ref": expression.Traversal}
	case "literal":
		return expression.Value
	default:
		return map[string]any{"$expression": strings.TrimSpace(expression.Raw)}
	}
}

func typeExpressionText(value any) string {
	if expression, ok := value.(map[string]any); ok {
		if reference, _ := expression["$ref"].(string); reference != "" {
			return reference
		}
		text, _ := expression["$expression"].(string)
		return strings.TrimSpace(text)
	}
	return stringValue(value)
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func canonicalPrimitiveSourceLiteral(value any, expected string) ([]byte, bool) {
	if text, ok := value.(string); ok {
		converted, err := ContextualizePrimitive(text, expected)
		if err != nil {
			return nil, false
		}
		value = converted
	}
	scalar, ok := value.(map[string]any)
	if !ok || stringValue(scalar["$scalar"]) != expected {
		return nil, false
	}
	text := stringValue(scalar["value"])
	switch expected {
	case "duration":
		nanoseconds, ok := new(big.Int).SetString(stringValue(scalar["nanoseconds"]), 10)
		if !ok {
			return nil, false
		}
		text = formatDurationSource(nanoseconds)
	case "size":
		text = stringValue(scalar["bytes"]) + "B"
	}
	if text == "" && expected != "bytes" {
		return nil, false
	}
	return hclwrite.TokensForValue(cty.StringVal(text)).Bytes(), true
}

func formatDurationSource(nanoseconds *big.Int) string {
	if nanoseconds == nil || nanoseconds.Sign() == 0 {
		return "0s"
	}
	remaining := new(big.Int).Set(nanoseconds)
	negative := remaining.Sign() < 0
	remaining.Abs(remaining)
	units := []struct {
		suffix string
		value  int64
	}{{"w", int64(7 * 24 * time.Hour)}, {"d", int64(24 * time.Hour)}, {"h", int64(time.Hour)}, {"m", int64(time.Minute)}, {"s", int64(time.Second)}, {"ms", int64(time.Millisecond)}, {"us", int64(time.Microsecond)}, {"ns", 1}}
	var result strings.Builder
	if negative {
		result.WriteByte('-')
	}
	for _, unit := range units {
		divisor := big.NewInt(unit.value)
		if remaining.Cmp(divisor) < 0 {
			continue
		}
		count, remainder := new(big.Int), new(big.Int)
		count.QuoRem(remaining, divisor, remainder)
		remaining = remainder
		result.WriteString(count.String())
		result.WriteString(unit.suffix)
	}
	return result.String()
}
