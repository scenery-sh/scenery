package scn

import (
	"bytes"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
)

var IdentifierPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var extensionResourceKindPattern = regexp.MustCompile(`^[a-z][a-z0-9_]*\.[a-z][a-z0-9_]*$`)

var wireLabelBlockTypes = map[string]struct{}{
	"cookie":          {},
	"header":          {},
	"part":            {},
	"query_parameter": {},
}

type ConcreteSyntaxTree struct {
	Tokens      []ConcreteToken   `json:"tokens"`
	Comments    []ConcreteComment `json:"comments"`
	LineEndings string            `json:"line_endings"`
	Recovered   bool              `json:"recovered"`
}

type ConcreteToken struct {
	Kind  string `json:"kind"`
	Bytes []byte `json:"bytes"`
	Range Range  `json:"range"`
}

type ConcreteComment struct {
	Bytes      []byte `json:"bytes"`
	Range      Range  `json:"range"`
	Attachment string `json:"attachment"`
	Target     string `json:"target,omitempty"`
}

func (tree *ConcreteSyntaxTree) Bytes() []byte {
	if tree == nil {
		return nil
	}
	var result []byte
	for _, token := range tree.Tokens {
		result = append(result, token.Bytes...)
	}
	return result
}

func buildConcreteSyntaxTree(sourceID, filename string, source []byte, positions *PositionIndex, file *hcl.File, recovered bool) (*ConcreteSyntaxTree, []Diagnostic) {
	tree := &ConcreteSyntaxTree{LineEndings: detectLineEndings(source), Recovered: recovered}
	var diagnostics []Diagnostic
	if bytes.HasPrefix(source, []byte{0xef, 0xbb, 0xbf}) {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN1011", Severity: "error", Message: "UTF-8 byte-order mark is forbidden"})
	}
	if !utf8.Valid(source) {
		diagnostics = append(diagnostics, Diagnostic{Code: "SCN1011", Severity: "error", Message: "source must be valid UTF-8"})
	}
	tokens, _ := hclsyntax.LexConfig(source, filename, hcl.Pos{Line: 1, Column: 1})
	cursor := 0
	for _, token := range tokens {
		start := token.Range.Start.Byte
		if start < cursor {
			start = cursor
		}
		if start > len(source) {
			start = len(source)
		}
		if start > cursor {
			tree.Tokens = append(tree.Tokens, ConcreteToken{Kind: "trivia", Bytes: append([]byte(nil), source[cursor:start]...), Range: byteRange(sourceID, positions, cursor, start)})
		}
		end := start + len(token.Bytes)
		if end > len(source) {
			end = len(source)
		}
		if len(token.Bytes) > 0 {
			item := ConcreteToken{Kind: concreteTokenKind(token.Type), Bytes: append([]byte(nil), source[start:end]...), Range: ConvertRange(sourceID, positions, token.Range)}
			tree.Tokens = append(tree.Tokens, item)
			if token.Type == hclsyntax.TokenComment {
				comment := ConcreteComment{Bytes: append([]byte(nil), item.Bytes...), Range: item.Range, Attachment: "detached"}
				tree.Comments = append(tree.Comments, comment)
				trimmed := bytes.TrimSpace(item.Bytes)
				if !bytes.HasPrefix(trimmed, []byte("#")) {
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1012", Severity: "error", Message: "comments must use canonical hash syntax", Range: &item.Range})
				}
			}
		}
		cursor = end
	}
	if cursor < len(source) {
		tree.Tokens = append(tree.Tokens, ConcreteToken{Kind: "trivia", Bytes: append([]byte(nil), source[cursor:]...), Range: byteRange(sourceID, positions, cursor, len(source))})
	}
	if file != nil {
		if body, ok := file.Body.(*hclsyntax.Body); ok {
			nodes := concreteSyntaxNodes(body)
			attachConcreteComments(source, tree.Comments, nodes)
			diagnostics = append(diagnostics, validateConcreteIdentifiers(sourceID, positions, body)...)
		}
	}
	return tree, diagnostics
}

type concreteNode struct {
	ID         string
	Start      int
	End        int
	StartLine  int
	EndLine    int
	HeaderEnd  int
	HeaderLine int
}

func concreteSyntaxNodes(body *hclsyntax.Body) []concreteNode {
	var nodes []concreteNode
	var visit func(*hclsyntax.Body)
	visit = func(current *hclsyntax.Body) {
		for name, attribute := range current.Attributes {
			rng := attribute.Range()
			nodes = append(nodes, concreteNode{ID: "attribute:" + name, Start: rng.Start.Byte, End: rng.End.Byte, StartLine: rng.Start.Line, EndLine: rng.End.Line, HeaderEnd: rng.End.Byte, HeaderLine: rng.End.Line})
		}
		for _, block := range current.Blocks {
			rng := block.Range()
			id := "block:" + block.Type
			if len(block.Labels) > 0 {
				id += ":" + strings.Join(block.Labels, ":")
			}
			nodes = append(nodes, concreteNode{ID: id, Start: rng.Start.Byte, End: rng.End.Byte, StartLine: rng.Start.Line, EndLine: rng.End.Line, HeaderEnd: block.OpenBraceRange.End.Byte, HeaderLine: block.OpenBraceRange.End.Line})
			visit(block.Body)
		}
	}
	visit(body)
	sort.Slice(nodes, func(i, j int) bool {
		if nodes[i].Start != nodes[j].Start {
			return nodes[i].Start < nodes[j].Start
		}
		return nodes[i].End < nodes[j].End
	})
	return nodes
}

func attachConcreteComments(source []byte, comments []ConcreteComment, nodes []concreteNode) {
	if len(comments) == 0 {
		return
	}
	for index := range comments {
		comment := &comments[index]
		best := concreteNode{HeaderEnd: -1}
		for _, node := range nodes {
			if node.HeaderLine == comment.Range.Start.Line+1 && node.HeaderEnd <= comment.Range.Start.ByteOffset && node.HeaderEnd > best.HeaderEnd {
				best = node
			}
		}
		if best.HeaderEnd >= 0 {
			comment.Attachment, comment.Target = "trailing", best.ID
		}
	}
	for start := 0; start < len(comments); {
		end := start
		for end+1 < len(comments) && commentGapIsContiguous(source, comments[end].Range.End.ByteOffset, comments[end+1].Range.Start.ByteOffset) {
			end++
		}
		if comments[start].Attachment == "detached" {
			lastEnd := comments[end].Range.End.ByteOffset
			for _, node := range nodes {
				if node.Start < lastEnd || !commentGapIsContiguous(source, lastEnd, node.Start) {
					continue
				}
				for index := start; index <= end; index++ {
					if comments[index].Attachment == "detached" {
						comments[index].Attachment, comments[index].Target = "leading", node.ID
					}
				}
				break
			}
		}
		start = end + 1
	}
}

func commentGapIsContiguous(source []byte, start, end int) bool {
	if start < 0 || end < start || end > len(source) {
		return false
	}
	gap := source[start:end]
	for _, character := range gap {
		if character != ' ' && character != '\t' && character != '\r' && character != '\n' {
			return false
		}
	}
	normalized := bytes.ReplaceAll(gap, []byte("\r\n"), []byte("\n"))
	return bytes.Count(normalized, []byte("\n")) <= 1
}

func validateConcreteIdentifiers(sourceID string, positions *PositionIndex, body *hclsyntax.Body) []Diagnostic {
	var diagnostics []Diagnostic
	var visit func(*hclsyntax.Body)
	visit = func(current *hclsyntax.Body) {
		for name, attribute := range current.Attributes {
			if !IdentifierPattern.MatchString(name) {
				rng := ConvertRange(sourceID, positions, attribute.NameRange)
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1013", Severity: "error", Message: "attribute names must use lower_snake_case ASCII", Range: &rng})
			}
		}
		for _, block := range current.Blocks {
			if !IdentifierPattern.MatchString(block.Type) {
				rng := ConvertRange(sourceID, positions, block.TypeRange)
				diagnostics = append(diagnostics, Diagnostic{Code: "SCN1013", Severity: "error", Message: "block names must use lower_snake_case ASCII", Range: &rng})
			}
			for index, label := range block.Labels {
				if _, wireLabel := wireLabelBlockTypes[block.Type]; index == 0 && wireLabel && label != "" {
					continue
				}
				if block.Type == "resource" && index == 0 && extensionResourceKindPattern.MatchString(label) {
					continue
				}
				if !IdentifierPattern.MatchString(label) {
					rng := ConvertRange(sourceID, positions, block.LabelRanges[index])
					diagnostics = append(diagnostics, Diagnostic{Code: "SCN1013", Severity: "error", Message: "resource labels must use lower_snake_case ASCII", Range: &rng})
				}
			}
			visit(block.Body)
		}
	}
	visit(body)
	return diagnostics
}

func concreteTokenKind(token hclsyntax.TokenType) string {
	switch token {
	case hclsyntax.TokenComment:
		return "comment"
	case hclsyntax.TokenNewline:
		return "newline"
	case hclsyntax.TokenIdent:
		return "identifier"
	case hclsyntax.TokenQuotedLit, hclsyntax.TokenStringLit:
		return "string"
	case hclsyntax.TokenNumberLit:
		return "number"
	default:
		return fmt.Sprintf("token:%U", rune(token))
	}
}

func detectLineEndings(source []byte) string {
	crlf := bytes.Count(source, []byte("\r\n"))
	lf := bytes.Count(source, []byte("\n")) - crlf
	switch {
	case crlf > 0 && lf > 0:
		return "mixed"
	case crlf > 0:
		return "crlf"
	case lf > 0:
		return "lf"
	default:
		return "none"
	}
}

func byteRange(sourceID string, positions *PositionIndex, start, end int) Range {
	return Range{SourceID: sourceID, Start: positions.position(start), End: positions.position(end)}
}
