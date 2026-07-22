package compiler

import (
	"github.com/hashicorp/hcl/v2"

	"scenery.sh/internal/scn"
)

// Syntax types live in internal/scn. These aliases keep the compiler graph
// model source-compatible while its callers migrate to the syntax boundary.
type Source = scn.Source
type Block = scn.Block
type Expression = scn.Expression
type ConcreteSyntaxTree = scn.ConcreteSyntaxTree
type ConcreteToken = scn.ConcreteToken
type ConcreteComment = scn.ConcreteComment
type FormatResult = scn.FormatResult
type sourcePositionIndex = scn.PositionIndex

const (
	appFilename     = scn.AppFilename
	packageFilename = scn.PackageFilename
	appLockFilename = scn.AppLockFilename
)

var (
	newSourcePositionIndex   = scn.NewPositionIndex
	convertBlock             = scn.ConvertBlock
	convertExpression        = scn.ConvertExpression
	convertRange             = scn.ConvertRange
	canonicalFormatSource    = scn.CanonicalFormat
	exactNumericScalar       = scn.ExactNumericScalar
	Format                   = scn.Format
	FormatPaths              = scn.FormatPaths
	pathExists               = scn.PathExists
	pathWithin               = scn.PathWithin
	rejectPathSymlinks       = scn.RejectPathSymlinks
	sourceFiles              = scn.SourceFiles
	literalString            = scn.LiteralString
	requireLiteralString     = scn.RequireLiteralString
	sceneryIdentifierPattern = scn.IdentifierPattern
	sourceID                 = scn.SourceID
	traversalString          = scn.TraversalString
)

func diagnosticsFromHCL(sourceID string, positions *sourcePositionIndex, diagnostics hcl.Diagnostics) []Diagnostic {
	return compilerSyntaxDiagnostics(scn.DiagnosticsFromHCL(sourceID, positions, diagnostics))
}

func parseSource(root, path string) (*Source, []Diagnostic) {
	source, diagnostics := scn.Parse(root, path)
	return source, compilerSyntaxDiagnostics(diagnostics)
}

func parseSourceLogical(path, relative string) (*Source, []Diagnostic) {
	source, diagnostics := scn.ParseLogical(path, relative)
	return source, compilerSyntaxDiagnostics(diagnostics)
}

func compilerSyntaxDiagnostics(diagnostics []scn.Diagnostic) []Diagnostic {
	converted := make([]Diagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		if len(diagnostic.Code) >= 4 && diagnostic.Code[:4] == "SCN9" {
			item := internalDiagnostic(diagnostic.Code, diagnostic.Message)
			item.Range = diagnostic.Range
			converted = append(converted, item)
			continue
		}
		converted = append(converted, Diagnostic{
			Code:     diagnostic.Code,
			Severity: diagnostic.Severity,
			Message:  diagnostic.Message,
			Range:    diagnostic.Range,
		})
	}
	return converted
}
