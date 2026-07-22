package uireport

import (
	"bytes"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

type importOrigin uint8

const (
	originLocal importOrigin = iota
	originDesignSystem
	originCatalog
	originLibrary
)

type jsToken struct {
	kind  byte
	value string
	start int
	end   int
}

var (
	colorLiteralPattern   = regexp.MustCompile(`(?i)#[0-9a-f]{3,8}\b|\b(?:rgba?|hsla?|oklch)\s*\(`)
	sizeLiteralPattern    = regexp.MustCompile(`(?i)(?:^|[^A-Za-z0-9_.])(?:\d+(?:\.\d+)?|\.\d+)(?:px|rem|em)\b`)
	styleAttributePattern = regexp.MustCompile(`(?:^|[^A-Za-z0-9_$])style\s*=\s*\{`)
	stylexCreatePattern   = regexp.MustCompile(`\bstylex\s*\.\s*create\s*\(\s*\{`)
)

var svgTags = map[string]struct{}{
	"svg": {}, "path": {}, "g": {}, "circle": {}, "rect": {}, "line": {},
	"polyline": {}, "polygon": {}, "defs": {}, "clipPath": {}, "mask": {},
	"use": {}, "ellipse": {}, "linearGradient": {}, "radialGradient": {},
	"stop": {}, "title": {}, "desc": {}, "symbol": {}, "marker": {},
	"pattern": {}, "filter": {}, "feGaussianBlur": {}, "feColorMatrix": {},
	"foreignObject": {}, "text": {}, "tspan": {},
}

// Scan classifies source files without accessing the filesystem.
func Scan(files []SourceFile) []FileReport {
	reports := make([]FileReport, 0, len(files))
	tokenPatterns := map[string]*regexp.Regexp{}
	for _, file := range files {
		reports = append(reports, scanFile(file, tokenPatterns))
	}
	sortFiles(reports)
	return reports
}

func scanFile(file SourceFile, tokenPatterns map[string]*regexp.Regexp) FileReport {
	source := string(file.Content)
	tokens := lexJavaScript(source)
	origins, tokenIdentifiers, importCount := parseImports(tokens)
	code := maskNonCode(source)

	report := FileReport{
		Path:                file.Path,
		Lines:               lineCount(file.Content),
		designSystemImports: importCount,
	}
	scanJSX(code, origins, &report)
	scanStyleX(source, code, tokenIdentifiers, tokenPatterns, &report.Style)
	finalizeFile(&report)
	return report
}

func lineCount(content []byte) int {
	if len(content) == 0 {
		return 0
	}
	return bytes.Count(content, []byte{'\n'}) + 1
}

func parseImports(tokens []jsToken) (map[string]importOrigin, map[string]struct{}, int) {
	origins := map[string]importOrigin{}
	tokenIdentifiers := map[string]struct{}{}
	designSystemImports := 0

	for i := 0; i < len(tokens); i++ {
		if tokens[i].kind != 'i' || tokens[i].value != "import" {
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].value == "(" {
			continue
		}
		from := -1
		moduleIndex := -1
		for j := i + 1; j < len(tokens); j++ {
			if tokens[j].value == ";" {
				break
			}
			if tokens[j].kind == 'i' && tokens[j].value == "from" && j+1 < len(tokens) && tokens[j+1].kind == 's' {
				from, moduleIndex = j, j+1
				break
			}
			if j == i+1 && tokens[j].kind == 's' {
				moduleIndex = j
				break
			}
		}
		if moduleIndex < 0 {
			continue
		}
		module := tokens[moduleIndex].value
		origin := classifyModule(module)
		isTokenModule := isStyleXTokenModule(module)
		if origin == originDesignSystem || origin == originCatalog || isTokenModule {
			designSystemImports++
		}
		if from < 0 {
			i = moduleIndex
			continue
		}

		for _, name := range importBindings(tokens[i+1 : from]) {
			origins[name] = origin
			if isTokenModule {
				tokenIdentifiers[name] = struct{}{}
			}
		}
		i = moduleIndex
	}
	return origins, tokenIdentifiers, designSystemImports
}

func importBindings(tokens []jsToken) []string {
	var bindings []string
	depth := 0
	for i := 0; i < len(tokens); i++ {
		token := tokens[i]
		switch token.value {
		case "{":
			depth++
			continue
		case "}":
			depth--
			continue
		case "*":
			if i+2 < len(tokens) && tokens[i+1].value == "as" && tokens[i+2].kind == 'i' {
				bindings = append(bindings, tokens[i+2].value)
				i += 2
			}
			continue
		}
		if token.kind != 'i' || token.value == "type" {
			continue
		}
		if depth == 0 {
			if i == 0 || tokens[i-1].value == "," {
				bindings = append(bindings, token.value)
			}
			continue
		}
		if i+1 < len(tokens) && tokens[i+1].value == "as" {
			if i+2 < len(tokens) && tokens[i+2].kind == 'i' {
				bindings = append(bindings, tokens[i+2].value)
				i += 2
			}
			continue
		}
		if i == 0 || tokens[i-1].value == "{" || tokens[i-1].value == "," || tokens[i-1].value == "type" {
			bindings = append(bindings, token.value)
		}
	}
	return bindings
}

func classifyModule(module string) importOrigin {
	switch {
	case strings.HasPrefix(module, "@astryxdesign/") || strings.HasPrefix(module, "astryx"):
		return originDesignSystem
	case module == "@scenery/ui" || strings.HasPrefix(module, "@scenery/ui/"):
		return originCatalog
	case strings.HasPrefix(module, ".") || strings.HasPrefix(module, "@/"):
		return originLocal
	default:
		return originLibrary
	}
}

func isStyleXTokenModule(module string) bool {
	base := module
	if query, _, ok := strings.Cut(base, "?"); ok {
		base = query
	}
	return strings.HasSuffix(base, ".stylex") ||
		strings.HasSuffix(base, ".stylex.js") ||
		strings.HasSuffix(base, ".stylex.jsx") ||
		strings.HasSuffix(base, ".stylex.ts") ||
		strings.HasSuffix(base, ".stylex.tsx")
}

func scanJSX(code string, origins map[string]importOrigin, report *FileReport) {
	for i := 0; i < len(code); i++ {
		if code[i] != '<' || i+1 >= len(code) || code[i+1] == '/' || code[i+1] == '>' {
			continue
		}
		start := i + 1
		if !isJSXNameStart(code[start]) {
			continue
		}
		end := start + 1
		for end < len(code) && isJSXNamePart(code[end]) {
			end++
		}
		if end < len(code) && code[end] != ' ' && code[end] != '\t' && code[end] != '\r' &&
			code[end] != '\n' && code[end] != '/' && code[end] != '>' {
			continue
		}
		tag := code[start:end]
		tagEnd := findJSXTagEnd(code, end)
		if tagEnd < 0 {
			continue
		}
		classifyJSXTag(tag, origins, &report.Markup)
		report.Style.InlineStyleProps += len(styleAttributePattern.FindAllStringIndex(code[end:tagEnd], -1))
		i = tagEnd
	}
}

func classifyJSXTag(tag string, origins map[string]importOrigin, markup *MarkupReport) {
	if _, ok := svgTags[tag]; ok {
		markup.SVG++
		return
	}
	r, _ := utf8.DecodeRuneInString(tag)
	if unicode.IsLower(r) {
		markup.Raw++
		return
	}
	head := tag
	if dot := strings.IndexByte(head, '.'); dot >= 0 {
		head = head[:dot]
	}
	switch origins[head] {
	case originDesignSystem:
		markup.DesignSystem++
	case originCatalog:
		markup.Catalog++
	case originLibrary:
		markup.Lib++
	default:
		markup.Local++
	}
}

func findJSXTagEnd(code string, start int) int {
	braceDepth := 0
	for i := start; i < len(code); i++ {
		switch code[i] {
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		case '>':
			if braceDepth == 0 {
				return i
			}
		}
	}
	return -1
}

func scanStyleX(source, code string, tokenIdentifiers map[string]struct{}, tokenPatterns map[string]*regexp.Regexp, style *StyleReport) {
	for _, match := range stylexCreatePattern.FindAllStringIndex(code, -1) {
		open := match[1] - 1
		close := matchingBrace(code, open)
		if close < 0 {
			continue
		}
		bodyCode := code[open+1 : close]
		bodySource := stripComments(source[open+1 : close])
		for identifier := range tokenIdentifiers {
			pattern, ok := tokenPatterns[identifier]
			if !ok {
				pattern = regexp.MustCompile(`\b` + regexp.QuoteMeta(identifier) + `\s*(?:\.|\[)`)
				tokenPatterns[identifier] = pattern
			}
			style.TokenRefs += len(pattern.FindAllStringIndex(bodyCode, -1))
		}
		style.RawColors += len(colorLiteralPattern.FindAllStringIndex(bodySource, -1))
		style.RawSizes += len(sizeLiteralPattern.FindAllStringIndex(bodySource, -1))
	}
}

func matchingBrace(code string, open int) int {
	if open < 0 || open >= len(code) || code[open] != '{' {
		return -1
	}
	depth := 1
	for i := open + 1; i < len(code); i++ {
		switch code[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func isJSXNameStart(b byte) bool {
	return b == '_' || b == '$' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func isJSXNamePart(b byte) bool {
	return isJSXNameStart(b) || b >= '0' && b <= '9' || b == '.' || b == ':' || b == '-'
}

func lexJavaScript(source string) []jsToken {
	var tokens []jsToken
	for i := 0; i < len(source); {
		switch {
		case isSpace(source[i]):
			i++
		case i+1 < len(source) && source[i:i+2] == "//":
			i = skipLineComment(source, i+2)
		case i+1 < len(source) && source[i:i+2] == "/*":
			i = skipBlockComment(source, i+2)
		case source[i] == '\'' || source[i] == '"':
			end := skipQuoted(source, i, source[i])
			value := ""
			if end > i+1 {
				value = source[i+1 : end-1]
			}
			tokens = append(tokens, jsToken{kind: 's', value: value, start: i, end: end})
			i = end
		case source[i] == '`':
			i = skipTemplate(source, i)
		case isIdentifierStart(source[i]):
			start := i
			i++
			for i < len(source) && isIdentifierPart(source[i]) {
				i++
			}
			tokens = append(tokens, jsToken{kind: 'i', value: source[start:i], start: start, end: i})
		default:
			tokens = append(tokens, jsToken{kind: 'p', value: source[i : i+1], start: i, end: i + 1})
			i++
		}
	}
	return tokens
}

func maskNonCode(source string) string {
	out := []byte(source)
	for i := range out {
		if out[i] != '\n' && out[i] != '\r' {
			out[i] = ' '
		}
	}
	maskNormal(source, out, 0, false)
	return string(out)
}

func maskNormal(source string, out []byte, start int, stopAtBrace bool) int {
	braceDepth := 0
	for i := start; i < len(source); {
		switch {
		case i+1 < len(source) && source[i:i+2] == "//":
			i = skipLineComment(source, i+2)
		case i+1 < len(source) && source[i:i+2] == "/*":
			i = skipBlockComment(source, i+2)
		case source[i] == '\'' || source[i] == '"':
			i = skipQuoted(source, i, source[i])
		case source[i] == '`':
			i = maskTemplate(source, out, i+1)
		case stopAtBrace && source[i] == '{':
			out[i] = source[i]
			braceDepth++
			i++
		case stopAtBrace && source[i] == '}':
			out[i] = source[i]
			if braceDepth == 0 {
				return i + 1
			}
			braceDepth--
			i++
		default:
			out[i] = source[i]
			i++
		}
	}
	return len(source)
}

func maskTemplate(source string, out []byte, start int) int {
	for i := start; i < len(source); {
		if source[i] == '\\' {
			i += 2
			continue
		}
		if source[i] == '`' {
			return i + 1
		}
		if i+1 < len(source) && source[i:i+2] == "${" {
			out[i], out[i+1] = '$', '{'
			i = maskNormal(source, out, i+2, true)
			continue
		}
		i++
	}
	return len(source)
}

func stripComments(source string) string {
	out := []byte(source)
	for i := 0; i < len(source); {
		switch {
		case i+1 < len(source) && source[i:i+2] == "//":
			end := skipLineComment(source, i+2)
			blankRange(out, i, end)
			i = end
		case i+1 < len(source) && source[i:i+2] == "/*":
			end := skipBlockComment(source, i+2)
			blankRange(out, i, end)
			i = end
		case source[i] == '\'' || source[i] == '"':
			i = skipQuoted(source, i, source[i])
		case source[i] == '`':
			i = skipTemplate(source, i)
		default:
			i++
		}
	}
	return string(out)
}

func blankRange(out []byte, start, end int) {
	for i := start; i < end && i < len(out); i++ {
		if out[i] != '\n' && out[i] != '\r' {
			out[i] = ' '
		}
	}
}

func skipLineComment(source string, i int) int {
	for i < len(source) && source[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(source string, i int) int {
	for i+1 < len(source) {
		if source[i:i+2] == "*/" {
			return i + 2
		}
		i++
	}
	return len(source)
}

func skipQuoted(source string, start int, quote byte) int {
	for i := start + 1; i < len(source); i++ {
		if source[i] == '\\' {
			i++
			continue
		}
		if source[i] == quote {
			return i + 1
		}
	}
	return len(source)
}

func skipTemplate(source string, start int) int {
	for i := start + 1; i < len(source); i++ {
		if source[i] == '\\' {
			i++
			continue
		}
		if source[i] == '`' {
			return i + 1
		}
	}
	return len(source)
}

func isSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n'
}

func isIdentifierStart(b byte) bool {
	return b == '_' || b == '$' || b >= 'A' && b <= 'Z' || b >= 'a' && b <= 'z'
}

func isIdentifierPart(b byte) bool {
	return isIdentifierStart(b) || b >= '0' && b <= '9'
}
