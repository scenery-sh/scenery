package runtime

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

func decodeContractForm(payload []byte, body ContractBodyMapping, schema ContractRequestSchema) (json.RawMessage, error) {
	if !utf8.Valid(payload) {
		return nil, contractRequestError(schema, "transport.invalid_request", 400, "form body is not UTF-8", nil)
	}
	values, err := url.ParseQuery(string(payload))
	if err != nil {
		return nil, contractRequestError(schema, "transport.invalid_request", 400, "invalid form body", err)
	}
	declared := map[string]ContractInputMapping{}
	for _, field := range body.Fields {
		declared[field.Name] = field
	}
	for name := range values {
		if _, ok := declared[name]; !ok {
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("undeclared form field %q", name), nil)
		}
	}
	object := map[string]json.RawMessage{}
	for _, field := range body.Fields {
		items := values[field.Name]
		if len(items) == 0 {
			if field.Optional {
				continue
			}
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("missing form field %q", field.Name), nil)
		}
		raw, err := contractMappedJSON(items, field)
		if err != nil {
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("invalid form field %q: %v", field.Name, err), err)
		}
		target := field.Target
		if target == "" {
			target = field.Name
		}
		object[target] = raw
	}
	return json.Marshal(object)
}

func decodeContractMultipart(payload []byte, contentType string, body ContractBodyMapping, schema ContractRequestSchema) (json.RawMessage, error) {
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || parameters["boundary"] == "" {
		return nil, contractRequestError(schema, "transport.invalid_request", 400, "invalid multipart content type", err)
	}
	parts := map[string]ContractMultipartPart{}
	for _, part := range body.MultipartParts {
		if part.Name == "" || parts[part.Name].Name != "" {
			return nil, contractRequestError(schema, "system.internal", 500, "invalid generated multipart schema", nil)
		}
		parts[part.Name] = part
	}
	maximumParts := body.MaxMultipartParts
	if maximumParts <= 0 {
		maximumParts = 128
	}
	reader := multipart.NewReader(bytes.NewReader(payload), parameters["boundary"])
	type multipartResult struct {
		part   ContractMultipartPart
		values []json.RawMessage
	}
	results := map[string]*multipartResult{}
	for count := 0; ; count++ {
		part, readErr := reader.NextPart()
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, contractRequestError(schema, "transport.invalid_request", 400, "invalid multipart body", readErr)
		}
		if count >= maximumParts {
			_ = part.Close()
			return nil, contractRequestError(schema, "transport.invalid_request", 400, "multipart part limit exceeded", nil)
		}
		name := part.FormName()
		spec, ok := parts[name]
		if !ok {
			_ = part.Close()
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("undeclared multipart part %q", name), nil)
		}
		result := results[name]
		if result == nil {
			result = &multipartResult{part: spec}
			results[name] = result
		}
		if !spec.Multiple && len(result.values) > 0 {
			_ = part.Close()
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("multipart part %q may not repeat", name), nil)
		}
		filename := part.FileName()
		if spec.Kind == "file" && filename == "" || spec.Kind != "file" && filename != "" {
			_ = part.Close()
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("multipart part %q has the wrong kind", name), nil)
		}
		partMedia, _, mediaErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if partMedia == "" && mediaErr == nil {
			partMedia = "text/plain"
		}
		if mediaErr != nil || !contractMediaAllowedPattern(partMedia, spec.MediaTypes) {
			_ = part.Close()
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("multipart part %q has unsupported media type", name), mediaErr)
		}
		limit := spec.MaxBytes
		if limit <= 0 {
			limit = 8 << 20
		}
		data, dataErr := readContractLimited(part, limit)
		closeErr := part.Close()
		if dataErr != nil || closeErr != nil {
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("multipart part %q exceeds its limit", name), dataErr)
		}
		var raw json.RawMessage
		switch spec.Kind {
		case "text":
			if !utf8.Valid(data) {
				return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("multipart part %q is not UTF-8", name), nil)
			}
			raw, err = contractScalarJSON(spec.Type, string(data), nil)
		case "bytes":
			raw, err = json.Marshal(base64.StdEncoding.EncodeToString(data))
		case "file":
			if spec.RetainFilename {
				raw, err = json.Marshal(map[string]any{"bytes": base64.StdEncoding.EncodeToString(data), "filename": filename, "media_type": strings.ToLower(partMedia)})
			} else {
				raw, err = json.Marshal(base64.StdEncoding.EncodeToString(data))
			}
		default:
			err = fmt.Errorf("unsupported generated multipart kind %q", spec.Kind)
		}
		if err != nil {
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("invalid multipart part %q", name), err)
		}
		result.values = append(result.values, raw)
	}
	object := map[string]json.RawMessage{}
	for _, spec := range body.MultipartParts {
		result := results[spec.Name]
		if result == nil {
			if spec.Optional {
				continue
			}
			return nil, contractRequestError(schema, "transport.invalid_request", 400, fmt.Sprintf("missing multipart part %q", spec.Name), nil)
		}
		target := spec.Target
		if target == "" {
			target = spec.Name
		}
		if spec.Multiple {
			encoded := make([]json.RawMessage, len(result.values))
			copy(encoded, result.values)
			object[target], err = json.Marshal(encoded)
		} else {
			object[target] = result.values[0]
		}
		if err != nil {
			return nil, contractRequestError(schema, "system.internal", 500, "assemble multipart input", err)
		}
	}
	return json.Marshal(object)
}

func contractMediaAllowedPattern(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	actual := strings.Split(strings.ToLower(value), "/")
	for _, candidate := range allowed {
		mediaType, _, err := mime.ParseMediaType(candidate)
		parts := strings.Split(strings.ToLower(mediaType), "/")
		if err == nil && len(actual) == 2 && len(parts) == 2 && (parts[0] == "*" || parts[0] == actual[0]) && (parts[1] == "*" || parts[1] == actual[1]) {
			return true
		}
	}
	return false
}

func validContractJSONSurrogates(input []byte) bool {
	for index := 0; index < len(input); index++ {
		if input[index] != '"' {
			continue
		}
		for index++; index < len(input) && input[index] != '"'; index++ {
			if input[index] != '\\' {
				continue
			}
			index++
			if index >= len(input) {
				return false
			}
			if input[index] != 'u' || index+4 >= len(input) {
				continue
			}
			first, err := strconv.ParseUint(string(input[index+1:index+5]), 16, 16)
			if err != nil {
				return false
			}
			index += 4
			if first >= 0xd800 && first <= 0xdbff {
				if index+6 >= len(input) || input[index+1] != '\\' || input[index+2] != 'u' {
					return false
				}
				second, err := strconv.ParseUint(string(input[index+3:index+7]), 16, 16)
				if err != nil || second < 0xdc00 || second > 0xdfff {
					return false
				}
				index += 6
			} else if first >= 0xdc00 && first <= 0xdfff {
				return false
			}
		}
	}
	return true
}
