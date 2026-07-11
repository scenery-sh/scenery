package runtime

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

func negotiateContractEncoding(header string, supported []string) (string, error) {
	allowed := map[string]bool{"identity": true}
	for _, encoding := range supported {
		encoding = strings.ToLower(strings.TrimSpace(encoding))
		if encoding != "gzip" {
			return "", fmt.Errorf("unsupported configured response encoding %q", encoding)
		}
		allowed[encoding] = true
	}
	if strings.TrimSpace(header) == "" {
		return "identity", nil
	}
	type candidate struct {
		name    string
		quality float64
		order   int
	}
	quality := map[string]float64{}
	identityExplicit := false
	wildcard, wildcardSet := 0.0, false
	for _, raw := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(raw), ";")
		name := strings.ToLower(strings.TrimSpace(parts[0]))
		q := 1.0
		for _, parameter := range parts[1:] {
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
		if name == "*" {
			wildcard, wildcardSet = q, true
		} else {
			quality[name] = q
			if name == "identity" {
				identityExplicit = true
			}
		}
	}
	var choices []candidate
	order := 0
	for _, name := range supported {
		name = strings.ToLower(strings.TrimSpace(name))
		q, explicit := quality[name]
		if !explicit && wildcardSet {
			q = wildcard
		}
		if allowed[name] && q > 0 {
			choices = append(choices, candidate{name: name, quality: q, order: order})
		}
		order++
	}
	identityQuality := 1.0
	if identityExplicit {
		identityQuality = quality["identity"]
	} else if wildcardSet {
		identityQuality = wildcard
	}
	if identityQuality > 0 {
		choices = append(choices, candidate{name: "identity", quality: identityQuality, order: order})
	}
	if len(choices) == 0 {
		return "", fmt.Errorf("no acceptable response content encoding")
	}
	sort.Slice(choices, func(i, j int) bool {
		if choices[i].quality != choices[j].quality {
			return choices[i].quality > choices[j].quality
		}
		if choices[i].order != choices[j].order {
			return choices[i].order < choices[j].order
		}
		return choices[i].name < choices[j].name
	})
	return choices[0].name, nil
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
	type candidate struct {
		media       string
		quality     float64
		specificity int
		order       int
	}
	var best *candidate
	for _, rawRange := range strings.Split(accept, ",") {
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
		parts := strings.Split(parsedMedia, "/")
		if len(parts) != 2 || parts[0] == "*" && parts[1] != "*" {
			return "", fmt.Errorf("invalid Accept media range")
		}
		specificity := 2
		if parts[0] == "*" {
			specificity = 0
		} else if parts[1] == "*" {
			specificity = 1
		}
		for order, mediaValue := range produced {
			media, producedParams, err := mime.ParseMediaType(mediaValue)
			if err != nil {
				return "", fmt.Errorf("invalid produced media type")
			}
			mediaParts := strings.Split(media, "/")
			if len(mediaParts) != 2 || parts[0] != "*" && parts[0] != mediaParts[0] || parts[1] != "*" && parts[1] != mediaParts[1] || !contractAcceptedMediaParametersMatch(params, producedParams) {
				continue
			}
			current := &candidate{media: mediaValue, quality: quality, specificity: specificity, order: order}
			if best == nil || current.quality > best.quality || current.quality == best.quality && current.specificity > best.specificity || current.quality == best.quality && current.specificity == best.specificity && current.order < best.order || current.quality == best.quality && current.specificity == best.specificity && current.order == best.order && current.media < best.media {
				best = current
			}
		}
	}
	if best == nil {
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
