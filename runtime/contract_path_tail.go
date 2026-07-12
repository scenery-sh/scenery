package runtime

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
)

const (
	contractHTTPPathTailProfile        = "scenery.http-path-tail/v1"
	contractRuntimeHTTPPathTailProfile = "scenery.runtime-http-path-tail/v1"
)

func validateContractPathTail(endpoint *Endpoint) error {
	tail := endpoint.ContractPathTail
	if tail == nil {
		return nil
	}
	segments := splitRoutePath(endpoint.Path)
	if !validContractPathTailName(tail.Name) || len(segments) == 0 || segments[len(segments)-1] != "*"+tail.Name {
		return fmt.Errorf("runtime path must end in *%s", tail.Name)
	}
	for _, segment := range segments[:len(segments)-1] {
		if strings.HasPrefix(segment, "*") {
			return fmt.Errorf("path tail must be the only terminal wildcard")
		}
	}
	canonicalSuffix := "{" + tail.Name + "...}"
	expectedTemplate := strings.TrimSuffix(endpoint.Path, "*"+tail.Name) + canonicalSuffix
	if tail.CanonicalTemplate != expectedTemplate || tail.Target == "" {
		return fmt.Errorf("canonical template and target do not match the runtime route")
	}
	if tail.MinimumSegments != 0 || tail.Decoding != "segment_rfc3986_once" || tail.Guarantee != "framework_enforced" {
		return fmt.Errorf("unsupported path-tail cardinality, decoding, or guarantee")
	}
	if !slices.Equal(tail.Precedence, []string{"literal", "parameter", "exact_end", "path_tail"}) {
		return fmt.Errorf("unsupported path-tail precedence")
	}
	validEmpty := map[string]string{"string": "empty_string", "relative_path": "invalid_request", "optional(relative_path)": "absent"}
	if validEmpty[tail.Type] == "" || tail.EmptyCapture != validEmpty[tail.Type] {
		return fmt.Errorf("unsupported target type or empty-capture behavior")
	}
	if !slices.Contains(tail.RequiredProfiles, contractHTTPPathTailProfile) || !slices.Contains(tail.RequiredProfiles, contractRuntimeHTTPPathTailProfile) {
		return fmt.Errorf("required path-tail profiles are missing")
	}
	return nil
}

func validContractPathTailName(value string) bool {
	for index, char := range value {
		if index == 0 && (char < 'a' || char > 'z') || index > 0 && !((char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '_') {
			return false
		}
	}
	return value != ""
}

func contractRouteConflict(left, right *Endpoint) bool {
	if left == nil || right == nil || left.ContractPathTail == nil && right.ContractPathTail == nil || (left.Access == Private) != (right.Access == Private) || !contractMethodsOverlap(left.Methods, right.Methods) {
		return false
	}
	leftPattern, rightPattern := parseRoutePattern(left.Path), parseRoutePattern(right.Path)
	if len(leftPattern.segments) != len(rightPattern.segments) {
		return false
	}
	for index := range leftPattern.segments {
		leftSegment, rightSegment := leftPattern.segments[index], rightPattern.segments[index]
		if leftSegment.kind != rightSegment.kind || leftSegment.kind == routeLiteral && leftSegment.value != rightSegment.value {
			return false
		}
	}
	return true
}

func contractMethodsOverlap(left, right []string) bool {
	leftSet := contractEffectiveMethods(left)
	for method := range contractEffectiveMethods(right) {
		if leftSet[method] {
			return true
		}
	}
	return false
}

func contractEffectiveMethods(methods []string) map[string]bool {
	result := map[string]bool{}
	for _, method := range expandMethods(methods) {
		result[method] = true
		if method == http.MethodGet {
			result[http.MethodHead] = true
		}
	}
	return result
}
