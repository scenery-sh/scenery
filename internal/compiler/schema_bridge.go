package compiler

import (
	"regexp"
	"strings"

	"scenery.sh/internal/spec"
)

type authoredBlockSchema = spec.SourceBlockSchema
type authoredAttributeSchema = spec.SourceAttributeSchema
type authoredChildSchema = spec.SourceChildSchema

type AuthoredBlockSchema = spec.SourceBlockSchema
type AuthoredAttributeSchema = spec.SourceAttributeSchema
type AuthoredChildSchema = spec.SourceChildSchema
type resourceSchema = spec.ResourceSchema
type resourceConditionalRequirement = spec.ConditionalRequirement
type dynamicRevisionDomain = spec.DynamicRevisionDomain
type authoredFieldKey = spec.AuthoredFieldKey
type authoredFieldOverride = spec.AuthoredFieldOverride

var (
	resourceSchemas                  = spec.ResourceSchemas()
	resourceConditionalRequirements  = spec.ConditionalRequirements()
	authoredConditionalRequirements  = spec.AuthoredConditionalRequirements()
	dynamicResourceRevisionDomains   = spec.DynamicResourceRevisionDomains()
	authoredStructuralSchemas        = spec.StructuralSourceSchemas()
	authoredResourceSchemas          = spec.ResourceSourceSchemas()
	authoredResourceChildren         = spec.ResourceSourceChildren()
	deploymentListenerSourceSchema   = spec.NamedSourceSchemas()["deployment_listener"]
	httpSourceSchema                 = spec.NamedSourceSchemas()["http"]
	httpCookieSourceSchema           = spec.NamedSourceSchemas()["http_cookie"]
	httpHeaderSourceSchema           = spec.NamedSourceSchemas()["http_header"]
	httpMultipartPartSourceSchema    = spec.NamedSourceSchemas()["http_multipart_part"]
	httpPathParameterSourceSchema    = spec.NamedSourceSchemas()["http_path_parameter"]
	httpPathTailSourceSchema         = spec.NamedSourceSchemas()["http_path_tail"]
	httpQueryParameterSourceSchema   = spec.NamedSourceSchemas()["http_query_parameter"]
	httpResponseCookieSourceSchema   = spec.NamedSourceSchemas()["http_response_cookie"]
	httpResponseHeaderSourceSchema   = spec.NamedSourceSchemas()["http_response_header"]
	operationIdempotencySourceSchema = spec.NamedSourceSchemas()["operation_idempotency"]
	authoredFieldOverrides           = spec.AuthoredFieldOverrides()
)

func CoreSchema(kind string) (map[string]any, bool) {
	return spec.CoreSchema(kind)
}

func blockTypeForKind(kind string) string {
	return strings.ReplaceAll(strings.TrimPrefix(kind, "scenery."), "-", "_")
}

func resourceSchemaAllowedFields(kind string) []string {
	return spec.ResourceSchemaAllowedFields(kind)
}

func ResourceCreateKindSupported(kind string) bool { return spec.ResourceCreateKindSupported(kind) }

func authoredResourceSourceSchema(blockType string) (*authoredBlockSchema, bool) {
	schema, ok := authoredResourceSchemas[blockType]
	return schema, ok
}

// AuthoredResourceSourceSchema returns the canonical authored-source shape for
// one resource block. Evolution uses it to render source mutations without
// duplicating compiler schema policy. The result is shared read-only compiler
// metadata; callers must not mutate it.
func AuthoredResourceSourceSchema(blockType string) (*AuthoredBlockSchema, bool) {
	return authoredResourceSourceSchema(blockType)
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
	matched, err := regexp.MatchString(schema.LabelPattern, label)
	return err == nil && matched
}

func ValidAuthoredLabel(schema *AuthoredBlockSchema, label string) bool {
	return validAuthoredLabel(schema, label)
}

func authoredEnumAllows(field authoredAttributeSchema, value string) bool {
	return spec.AuthoredEnumAllows(field, value)
}

func unsupportedDraftCapability(revision, name string) string {
	return spec.UnsupportedDraftCapability(revision, name)
}

func authoredAttributeDefinition(revision, name string) authoredAttributeSchema {
	return spec.AuthoredAttributeDefinition(revision, name)
}

func authoredRevisionDomain(revision, name string) string {
	return spec.AuthoredRevisionDomain(revision, name)
}
