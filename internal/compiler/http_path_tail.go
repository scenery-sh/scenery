package compiler

import (
	"fmt"
	"net/url"
	"path"
	"strings"
)

type httpTemplateSegmentKind uint8

const (
	httpTemplateLiteral httpTemplateSegmentKind = iota
	httpTemplateParameter
	httpTemplateTail
)

type httpTemplateSegment struct {
	kind httpTemplateSegmentKind
	name string
}

func parseHTTPPathTemplate(value string) ([]httpTemplateSegment, error) {
	if value == "" || !strings.HasPrefix(value, "/") || strings.Contains(value, "//") || path.Clean(value) != value {
		return nil, fmt.Errorf("path is not absolute and normalized")
	}
	decoded, err := url.PathUnescape(value)
	if err != nil || strings.Contains(decoded, "//") || path.Clean(decoded) != decoded {
		return nil, fmt.Errorf("decoded path is not normalized")
	}
	if value == "/" {
		return nil, nil
	}
	raw := strings.Split(strings.TrimPrefix(value, "/"), "/")
	segments := make([]httpTemplateSegment, 0, len(raw))
	tails := 0
	for index, segment := range raw {
		if match := httpPathParameterPattern.FindStringSubmatch(segment); len(match) == 2 && match[0] == segment {
			segments = append(segments, httpTemplateSegment{kind: httpTemplateParameter, name: match[1]})
			continue
		}
		if match := httpPathTailPattern.FindStringSubmatch(segment); len(match) == 2 && match[0] == segment {
			tails++
			if tails != 1 || index != len(raw)-1 {
				return nil, fmt.Errorf("path tail must be the only tail and occupy the final segment")
			}
			segments = append(segments, httpTemplateSegment{kind: httpTemplateTail, name: match[1]})
			continue
		}
		if strings.ContainsAny(segment, "{}*") {
			return nil, fmt.Errorf("invalid parameter or wildcard segment")
		}
		segments = append(segments, httpTemplateSegment{kind: httpTemplateLiteral})
	}
	return segments, nil
}

func httpSpecUsesPathTail(httpSpec map[string]any) bool {
	return len(namedChildren(httpSpec, "path_tail")) > 0
}

func bindingUsesHTTPPathTail(binding Resource) bool {
	httpSpec, _ := binding.Spec["http"].(map[string]any)
	return httpSpecUsesPathTail(httpSpec)
}

func bindingsUseHTTPPathTail(bindings []Resource) bool {
	for _, binding := range bindings {
		if bindingUsesHTTPPathTail(binding) {
			return true
		}
	}
	return false
}

func applyHTTPPathTailEffective(binding *Resource, httpSpec map[string]any, resources map[string]*Resource) {
	tails := provenanceNamedChildren(httpSpec, "path_tail", "/spec/http")
	if len(tails) == 0 {
		return
	}
	operation := resources[resolveResourceRef(*binding, refString(binding.Spec["operation"]), "operation")]
	if operation == nil {
		return
	}
	shape := resolveOperationInputShape(pointerResourcesByAddress(resources), *operation)
	for _, tail := range tails {
		field, whole, ok := resolveOperationInputTarget(*operation, shape, refOrString(tail.Value["to"]))
		if !ok || whole {
			continue
		}
		targetType := strings.TrimSpace(typeExpression(field.Type))
		emptyCapture := "invalid_request"
		switch targetType {
		case "string":
			emptyCapture = "empty_string"
		case "optional(relative_path)":
			emptyCapture = "absent"
		}
		defaults := map[string]any{
			"minimum_segments": exactNumericScalar("0"),
			"target_type":      targetType,
			"empty_capture":    emptyCapture,
			"decoding":         "segment_rfc3986_once",
			"guarantee":        "framework_enforced",
		}
		for name, value := range defaults {
			tail.Value[name] = value
			setFieldProvenance(&binding.Origin, provenanceChildPath(tail.Path, name), value, httpPathTailDefaultField())
		}
	}
}

func pointerResourcesByAddress(resources map[string]*Resource) map[string]Resource {
	result := make(map[string]Resource, len(resources))
	for address, resource := range resources {
		result[address] = *resource
	}
	return result
}

func httpPathTailDefaultField() FieldProvenance {
	return FieldProvenance{Kind: "default", ProvidedBy: "spec", Transformations: []string{"spec_default"}}
}
