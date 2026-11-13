package compiler

import (
	"regexp"
	"strings"
	"sync"

	"scenery.sh/internal/spec"
)

// labelPatternCache compiles each schema label pattern once per process. The
// patterns are schema constants, so validating one block per resource would
// otherwise recompile the same handful of expressions for every label in the
// workspace.
var labelPatternCache sync.Map // pattern string -> *regexp.Regexp (nil when invalid)

func labelPatternMatches(pattern, label string) bool {
	cached, ok := labelPatternCache.Load(pattern)
	if !ok {
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			compiled = nil
		}
		cached, _ = labelPatternCache.LoadOrStore(pattern, compiled)
	}
	expression, _ := cached.(*regexp.Regexp)
	if expression == nil {
		// Mirror regexp.MatchString's error contract: an invalid pattern never
		// matches.
		return false
	}
	return expression.MatchString(label)
}

type authoredBlockSchema = spec.SourceBlockSchema
type authoredAttributeSchema = spec.SourceAttributeSchema

type authoredFieldKey = spec.AuthoredFieldKey

var (
	resourceSchemas = spec.ResourceSchemas()

	authoredConditionalRequirements = spec.AuthoredConditionalRequirements()
	dynamicResourceRevisionDomains  = spec.DynamicResourceRevisionDomains()
	authoredStructuralSchemas       = spec.StructuralSourceSchemas()
	authoredResourceSchemas         = spec.ResourceSourceSchemas()
	authoredResourceChildren        = spec.ResourceSourceChildren()

	httpCookieSourceSchema         = spec.NamedSourceSchemas()["http_cookie"]
	httpHeaderSourceSchema         = spec.NamedSourceSchemas()["http_header"]
	httpMultipartPartSourceSchema  = spec.NamedSourceSchemas()["http_multipart_part"]
	httpPathParameterSourceSchema  = spec.NamedSourceSchemas()["http_path_parameter"]
	httpPathTailSourceSchema       = spec.NamedSourceSchemas()["http_path_tail"]
	httpQueryParameterSourceSchema = spec.NamedSourceSchemas()["http_query_parameter"]
	httpResponseCookieSourceSchema = spec.NamedSourceSchemas()["http_response_cookie"]
	httpResponseHeaderSourceSchema = spec.NamedSourceSchemas()["http_response_header"]

	authoredFieldOverrides = spec.AuthoredFieldOverrides()
)

func blockTypeForKind(kind string) string {
	return strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
}

func authoredResourceSourceSchema(blockType string) (*authoredBlockSchema, bool) {
	schema, ok := authoredResourceSchemas[blockType]
	return schema, ok
}

func validAuthoredLabel(schema *authoredBlockSchema, label string) bool {
	if schema == nil {
		return false
	}
	if schema.Labels == 0 {
		return label == ""
	}
	if label == "" {
		return false
	}
	if schema.LabelPattern == "" {
		return true
	}
	return labelPatternMatches(schema.LabelPattern, label)
}

func authoredEnumAllows(field authoredAttributeSchema, value string) bool {
	return spec.AuthoredEnumAllows(field, value)
}
