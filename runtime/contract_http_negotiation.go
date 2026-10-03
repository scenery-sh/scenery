package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strconv"
	"strings"
)

func negotiateContractEncoding(header string, supported []string) (string, error) {
	gzipAllowed := false
	for _, encoding := range supported {
		encoding = strings.ToLower(strings.TrimSpace(encoding))
		if encoding != "gzip" {
			return "", fmt.Errorf("unsupported configured response encoding %q", encoding)
		}
		gzipAllowed = true
	}
	if strings.TrimSpace(header) == "" {
		return "identity", nil
	}
	gzipQuality, identityQuality := 0.0, 1.0
	gzipExplicit, identityExplicit := false, false
	wildcard, wildcardSet := 0.0, false
	for raw := range strings.SplitSeq(header, ",") {
		name, parameters, hasParameters := strings.Cut(strings.TrimSpace(raw), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		q := 1.0
		if hasParameters {
			for parameter := range strings.SplitSeq(parameters, ";") {
				key, value, ok := strings.Cut(strings.TrimSpace(parameter), "=")
				if key != "q" || !ok {
					return "", fmt.Errorf("invalid Accept-Encoding parameter")
				}
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					return "", fmt.Errorf("invalid Accept-Encoding quality")
				}
				q = parsed
			}
		}
		switch name {
		case "*":
			wildcard, wildcardSet = q, true
		case "gzip":
			gzipQuality, gzipExplicit = q, true
		case "identity":
			identityQuality, identityExplicit = q, true
		}
	}
	if !gzipExplicit && wildcardSet {
		gzipQuality = wildcard
	}
	if !identityExplicit && wildcardSet {
		identityQuality = wildcard
	}
	// Gzip is the only configured response encoding. On a quality tie it precedes
	// the implicit identity fallback in binding order, so no candidate sort is needed.
	if gzipAllowed && gzipQuality > 0 && (!(identityQuality > 0) || gzipQuality >= identityQuality) {
		return "gzip", nil
	}
	if identityQuality > 0 {
		return "identity", nil
	}
	return "", fmt.Errorf("no acceptable response content encoding")
}

func encodeContractJSON(status int, value any, mediaType string, maxBytes int64) (ContractHTTPResponse, error) {
	body, err := json.Marshal(value)
	if err != nil {
		return ContractHTTPResponse{}, err
	}
	if maxBytes > 0 && int64(len(body)) > maxBytes {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "system.internal", Status: http.StatusInternalServerError, Message: "response exceeds binding limit"}
	}
	return ContractHTTPResponse{Status: status, Headers: http.Header{"Content-Type": []string{mediaType}}, Body: body}, nil
}

func negotiateContractMedia(accept string, produced []string) (string, error) {
	if len(produced) == 0 {
		produced = []string{"application/json"}
	}
	if strings.TrimSpace(accept) == "" {
		return produced[0], nil
	}
	return negotiateContractMediaRanges(accept, produced)
}

func negotiateContractMediaRanges(accept string, produced []string) (string, error) {
	type candidate struct {
		media       string
		quality     float64
		specificity int
		order       int
	}
	var best candidate
	found := false
	for rawRange := range strings.SplitSeq(accept, ",") {
		parsedMedia, params, err := mime.ParseMediaType(strings.TrimSpace(rawRange))
		if err != nil {
			return "", fmt.Errorf("invalid Accept header")
		}
		quality := 1.0
		if rawQuality, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(rawQuality, 64)
			if err != nil || quality < 0 || quality > 1 {
				return "", fmt.Errorf("invalid Accept quality")
			}
		}
		delete(params, "q")
		if quality == 0 {
			continue
		}
		major, minor, validRange := strings.Cut(parsedMedia, "/")
		if !validRange || strings.Contains(minor, "/") || major == "*" && minor != "*" {
			return "", fmt.Errorf("invalid Accept media range")
		}
		specificity := 2
		if major == "*" {
			specificity = 0
		} else if minor == "*" {
			specificity = 1
		}
		for order, mediaValue := range produced {
			media, producedParams, err := mime.ParseMediaType(mediaValue)
			if err != nil {
				return "", fmt.Errorf("invalid produced media type")
			}
			producedMajor, producedMinor, validMedia := strings.Cut(media, "/")
			if !validMedia || strings.Contains(producedMinor, "/") || major != "*" && major != producedMajor || minor != "*" && minor != producedMinor || !contractAcceptedMediaParametersMatch(params, producedParams) {
				continue
			}
			current := candidate{media: mediaValue, quality: quality, specificity: specificity, order: order}
			if !found || current.quality > best.quality || current.quality == best.quality && current.specificity > best.specificity || current.quality == best.quality && current.specificity == best.specificity && current.order < best.order || current.quality == best.quality && current.specificity == best.specificity && current.order == best.order && current.media < best.media {
				best, found = current, true
			}
		}
	}
	if !found {
		return "", fmt.Errorf("no acceptable response media type")
	}
	return best.media, nil
}

func contractMediaAllowed(mediaType string, parameters map[string]string, allowed []string) bool {
	mediaType = strings.ToLower(mediaType)
	for _, candidate := range allowed {
		parsed, expected, err := mime.ParseMediaType(candidate)
		if err == nil && strings.EqualFold(parsed, mediaType) && contractRequestMediaParametersMatch(mediaType, parameters, expected) {
			return true
		}
	}
	return false
}

func contractRequestMediaParametersMatch(mediaType string, actual, expected map[string]string) bool {
	actual = normalizedContractMediaParameters(actual)
	expected = normalizedContractMediaParameters(expected)
	deleteImplicitUTF8(actual)
	deleteImplicitUTF8(expected)
	if strings.EqualFold(mediaType, "multipart/form-data") {
		delete(actual, "boundary")
		delete(expected, "boundary")
	}
	return contractMediaParameterMapsEqual(actual, expected)
}

func contractAcceptedMediaParametersMatch(accepted, produced map[string]string) bool {
	if len(accepted) == 0 {
		return true
	}
	accepted = normalizedContractMediaParameters(accepted)
	produced = normalizedContractMediaParameters(produced)
	deleteImplicitUTF8(accepted)
	deleteImplicitUTF8(produced)
	for name, value := range accepted {
		if produced[name] != value {
			return false
		}
	}
	return true
}

func normalizedContractMediaParameters(parameters map[string]string) map[string]string {
	normalized := make(map[string]string, len(parameters))
	for name, value := range parameters {
		name = strings.ToLower(strings.TrimSpace(name))
		value = strings.TrimSpace(value)
		if name == "charset" {
			value = strings.ToLower(value)
		}
		normalized[name] = value
	}
	return normalized
}

func deleteImplicitUTF8(parameters map[string]string) {
	if charset := parameters["charset"]; charset == "" || charset == "utf-8" {
		delete(parameters, "charset")
	}
}

func contractMediaParameterMapsEqual(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, value := range left {
		if right[name] != value {
			return false
		}
	}
	return true
}

func contractRequestError(schema ContractRequestSchema, outcome string, fallback int, message string, cause error) error {
	status := fallback
	if configured := schema.TransportStatuses[outcome]; configured != 0 {
		status = configured
	}
	return &ContractTransportError{Outcome: outcome, Status: status, Message: message, Cause: cause}
}

func writeContractTransportError(writer http.ResponseWriter, err error) bool {
	var transport *ContractTransportError
	if !errors.As(err, &transport) {
		return false
	}
	status := transport.Status
	if status == 0 {
		status = http.StatusBadRequest
	}
	payload := struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}{Code: transport.Outcome, Message: transport.Error()}
	writer.Header().Set("Content-Type", "application/problem+json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(payload)
	return true
}

func contractTransportHTTPStatus(err error) (int, bool) {
	var transport *ContractTransportError
	if !errors.As(err, &transport) {
		return 0, false
	}
	if transport.Status == 0 {
		return http.StatusBadRequest, true
	}
	return transport.Status, true
}

func contractSignedInteger(value string) bool {
	if strings.HasPrefix(value, "-") {
		return len(value) > 1 && value[1] != '0' && contractDigits(value[1:])
	}
	return contractUnsignedInteger(value)
}

func contractUnsignedInteger(value string) bool {
	return value == "0" || len(value) > 0 && value[0] != '0' && contractDigits(value)
}

func contractDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func stringSet(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}
