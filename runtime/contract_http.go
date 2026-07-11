package runtime

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultContractRequestBytes             int64 = 8 << 20
	defaultContractDecompressedRequestBytes int64 = 16 << 20
)

const (
	ContractSourcePath   = "path"
	ContractSourceQuery  = "query"
	ContractSourceHeader = "header"
	ContractSourceCookie = "cookie"
)

type ContractInputMapping struct {
	Source     string
	Name       string
	Target     string
	Type       string
	Encoding   string
	Optional   bool
	EnumValues []string
}

type ContractBodyMapping struct {
	Codec                    string
	Target                   string
	Type                     string
	DecodeValue              func([]byte, any) error
	Include                  []string
	Except                   []string
	AcceptedMediaTypes       []string
	SupportedContentEncoding []string
	MaxCompressedBytes       int64
	MaxDecompressedBytes     int64
	Fields                   []ContractInputMapping
	MultipartParts           []ContractMultipartPart
	MaxMultipartParts        int
}

type ContractRequestSchema struct {
	Mappings          []ContractInputMapping
	ContextMappings   []ContractContextMapping
	Body              *ContractBodyMapping
	TransportStatuses map[string]int
}

type ContractTransportError struct {
	Outcome string
	Status  int
	Message string
	Cause   error
}

func ContractSystemError(err error) error {
	message := "contract implementation failure"
	if err != nil {
		message = err.Error()
	}
	return &ContractTransportError{Outcome: "system.internal", Status: http.StatusInternalServerError, Message: message, Cause: err}
}

func (e *ContractTransportError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return e.Message
	}
	return e.Outcome
}

func (e *ContractTransportError) Unwrap() error { return e.Cause }

func DecodeContractJSON[T any](request *http.Request) (T, error) {
	return DecodeContractInput[T](request, nil, ContractRequestSchema{Body: &ContractBodyMapping{Codec: "json"}})
}

func DecodeContractInput[T any](request *http.Request, pathValues map[string]string, schema ContractRequestSchema) (T, error) {
	var zero T
	if request == nil {
		return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "request is required", nil)
	}
	object := map[string]json.RawMessage{}
	for _, mapping := range schema.Mappings {
		values, err := contractMappingValues(request, pathValues, mapping)
		if err != nil {
			return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, err.Error(), err)
		}
		if len(values) == 0 {
			if mapping.Optional {
				continue
			}
			return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("missing %s %q", mapping.Source, mapping.Name), nil)
		}
		raw, err := contractMappedJSON(values, mapping)
		if err != nil {
			return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid %s %q: %v", mapping.Source, mapping.Name, err), err)
		}
		if mapping.Target == "" {
			return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "non-body mappings require a field target", nil)
		}
		if _, exists := object[mapping.Target]; exists {
			return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", mapping.Target), nil)
		}
		object[mapping.Target] = raw
	}

	if schema.Body != nil {
		raw, err := decodeContractBody(request, *schema.Body, schema)
		if err != nil {
			return zero, err
		}
		if schema.Body.Target != "" {
			if _, exists := object[schema.Body.Target]; exists {
				return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", schema.Body.Target), nil)
			}
			object[schema.Body.Target] = raw
		} else if len(schema.Body.Include) == 0 && len(schema.Body.Except) == 0 && len(object) == 0 {
			return unmarshalContractInput[T](raw, schema)
		} else {
			bodyObject, err := decodeContractJSONObject(raw)
			if err != nil {
				return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "request body must be an object", err)
			}
			include := stringSet(schema.Body.Include)
			except := stringSet(schema.Body.Except)
			for name, value := range bodyObject {
				if len(include) > 0 && !include[name] {
					return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is not accepted from the request body", name), nil)
				}
				if except[name] {
					return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is excluded from the request body", name), nil)
				}
				if _, exists := object[name]; exists {
					return zero, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("input field %q is populated more than once", name), nil)
				}
				object[name] = value
			}
		}
	}
	for _, mapping := range schema.ContextMappings {
		if _, exists := object[mapping.Target]; exists {
			return zero, contractRequestError(schema, "system.internal", http.StatusInternalServerError, fmt.Sprintf("context field %q is populated more than once", mapping.Target), nil)
		}
		value, err := contractContextValue(mapping.Source)
		if err != nil {
			return zero, contractRequestError(schema, "system.internal", http.StatusInternalServerError, "resolve trusted request context", err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return zero, contractRequestError(schema, "system.internal", http.StatusInternalServerError, "encode trusted request context", err)
		}
		object[mapping.Target] = raw
	}

	raw, err := json.Marshal(object)
	if err != nil {
		return zero, contractRequestError(schema, "system.internal", http.StatusInternalServerError, "assemble request input", err)
	}
	return unmarshalContractInput[T](raw, schema)
}

func unmarshalContractInput[T any](raw []byte, schema ContractRequestSchema) (T, error) {
	var value T
	if schema.Body != nil && schema.Body.Target == "" && schema.Body.DecodeValue != nil {
		if err := schema.Body.DecodeValue(raw, &value); err != nil {
			return value, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err), err)
		}
		return value, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return value, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, fmt.Sprintf("invalid request body: %v", err), err)
	}
	if _, err := decoder.Token(); err != io.EOF {
		return value, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "trailing request body value", err)
	}
	return value, nil
}

func contractMappingValues(request *http.Request, pathValues map[string]string, mapping ContractInputMapping) ([]string, error) {
	switch mapping.Source {
	case ContractSourcePath:
		raw, exists := pathValues[mapping.Name]
		if !exists {
			return nil, nil
		}
		decoded, err := decodeContractPathSegment(raw)
		if err != nil {
			return nil, err
		}
		return []string{decoded}, nil
	case ContractSourceQuery:
		return contractRawQueryValues(request.URL.RawQuery, mapping.Name, mapping.Encoding)
	case ContractSourceHeader:
		values := request.Header.Values(mapping.Name)
		if mapping.Encoding == "comma" {
			var split []string
			for _, value := range values {
				for _, item := range strings.Split(value, ",") {
					split = append(split, strings.Trim(item, " \t"))
				}
			}
			return split, nil
		}
		for index := range values {
			values[index] = strings.Trim(values[index], " \t")
		}
		return values, nil
	case ContractSourceCookie:
		return contractCookieValues(request.Header.Values("Cookie"), mapping.Name)
	default:
		return nil, fmt.Errorf("unsupported input source %q", mapping.Source)
	}
}

func contractRawQueryValues(rawQuery, wireName, encoding string) ([]string, error) {
	var encodedValues []string
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		name, value, _ := strings.Cut(pair, "=")
		decodedName, err := url.PathUnescape(name)
		if err != nil || !utf8.ValidString(decodedName) {
			return nil, fmt.Errorf("invalid query name")
		}
		if decodedName != wireName {
			continue
		}
		if encoding == "comma" {
			encodedValues = append(encodedValues, strings.Split(value, ",")...)
		} else {
			encodedValues = append(encodedValues, value)
		}
	}
	values := make([]string, len(encodedValues))
	for index, encoded := range encodedValues {
		decoded, err := url.PathUnescape(encoded)
		if err != nil || !utf8.ValidString(decoded) {
			return nil, fmt.Errorf("invalid query value")
		}
		values[index] = decoded
	}
	return values, nil
}

func contractCookieValues(headers []string, wireName string) ([]string, error) {
	var values []string
	for _, header := range headers {
		for _, part := range strings.Split(header, ";") {
			name, value, found := strings.Cut(strings.TrimSpace(part), "=")
			if !found || name != wireName {
				continue
			}
			decoded, err := url.PathUnescape(value)
			if err != nil || !utf8.ValidString(decoded) {
				return nil, fmt.Errorf("invalid cookie value")
			}
			values = append(values, decoded)
		}
	}
	return values, nil
}

func decodeContractPathSegment(encoded string) (string, error) {
	decoded, err := url.PathUnescape(encoded)
	if err != nil || !utf8.ValidString(decoded) {
		return "", fmt.Errorf("invalid path percent encoding")
	}
	if decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") || strings.ContainsRune(decoded, 0) {
		return "", fmt.Errorf("invalid decoded path segment")
	}
	return decoded, nil
}

func contractMappedJSON(values []string, mapping ContractInputMapping) (json.RawMessage, error) {
	if mapping.Encoding == "json" {
		if len(values) != 1 {
			return nil, fmt.Errorf("JSON mapping requires exactly one value")
		}
		return strictContractJSON([]byte(values[0]))
	}
	typeExpression := strings.TrimSpace(mapping.Type)
	for _, wrapper := range []string{"optional", "nullable"} {
		for strings.HasPrefix(typeExpression, wrapper+"(") && strings.HasSuffix(typeExpression, ")") {
			typeExpression = strings.TrimSpace(typeExpression[len(wrapper)+1 : len(typeExpression)-1])
		}
	}
	collection := ""
	for _, wrapper := range []string{"list", "set"} {
		if strings.HasPrefix(typeExpression, wrapper+"(") && strings.HasSuffix(typeExpression, ")") {
			collection = wrapper
			typeExpression = strings.TrimSpace(typeExpression[len(wrapper)+1 : len(typeExpression)-1])
			break
		}
	}
	if collection == "" {
		if len(values) != 1 {
			return nil, fmt.Errorf("scalar mapping requires exactly one value")
		}
		return contractScalarJSON(typeExpression, values[0], mapping.EnumValues)
	}
	items := make([]json.RawMessage, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		raw, err := contractScalarJSON(typeExpression, value, mapping.EnumValues)
		if err != nil {
			return nil, err
		}
		key := string(raw)
		if collection == "set" && seen[key] {
			return nil, fmt.Errorf("duplicate set element")
		}
		seen[key] = true
		items = append(items, raw)
	}
	if collection == "set" {
		sort.Slice(items, func(i, j int) bool { return bytes.Compare(items[i], items[j]) < 0 })
	}
	return json.Marshal(items)
}

func contractScalarJSON(kind, value string, enumValues []string) (json.RawMessage, error) {
	if len(enumValues) > 0 {
		matched := false
		for _, allowed := range enumValues {
			matched = matched || value == allowed
		}
		if !matched {
			return nil, fmt.Errorf("unknown closed enum value %q", value)
		}
	}
	switch kind {
	case "string", "bytes", "uuid", "date", "datetime", "duration", "url", "relative_path", "int", "decimal", "size":
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("invalid UTF-8 value")
		}
		if kind == "bytes" {
			decoded, err := base64.StdEncoding.DecodeString(value)
			if err != nil || base64.StdEncoding.EncodeToString(decoded) != value {
				return nil, fmt.Errorf("invalid canonical base64")
			}
		}
		switch kind {
		case "int":
			if !contractSignedInteger(value) {
				return nil, fmt.Errorf("invalid canonical int")
			}
		case "decimal":
			if !canonicalContractDecimalText(value) {
				return nil, fmt.Errorf("invalid canonical decimal")
			}
		case "size":
			if !contractUnsignedInteger(value) {
				return nil, fmt.Errorf("invalid canonical size")
			}
		}
		return json.Marshal(value)
	case "bool":
		if value != "true" && value != "false" {
			return nil, fmt.Errorf("invalid bool")
		}
		return json.RawMessage(value), nil
	case "int32", "int64":
		if !contractSignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical %s", kind)
		}
		bits := 64
		if kind == "int32" {
			bits = 32
		}
		if _, err := strconv.ParseInt(value, 10, bits); err != nil {
			return nil, err
		}
		if kind == "int64" {
			return json.Marshal(value)
		}
		return json.RawMessage(value), nil
	case "uint32", "uint64":
		if !contractUnsignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical %s", kind)
		}
		bits := 64
		if kind == "uint32" {
			bits = 32
		}
		if _, err := strconv.ParseUint(value, 10, bits); err != nil {
			return nil, err
		}
		if kind == "uint64" {
			return json.Marshal(value)
		}
		return json.RawMessage(value), nil
	case "float32", "float64":
		bits := 64
		if kind == "float32" {
			bits = 32
		}
		parsed, err := strconv.ParseFloat(value, bits)
		if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) || parsed == 0 && math.Signbit(parsed) || strconv.FormatFloat(parsed, 'g', -1, bits) != value {
			return nil, fmt.Errorf("invalid finite %s", kind)
		}
		return json.RawMessage(value), nil
	default:
		return nil, fmt.Errorf("unsupported mapped scalar %q", kind)
	}
}

func canonicalContractDecimalText(value string) bool {
	if value == "" || strings.HasPrefix(value, "+") || strings.ContainsAny(value, "eE") {
		return false
	}
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	integer, fraction, hasFraction := strings.Cut(unsigned, ".")
	if integer == "" || len(integer) > 1 && integer[0] == '0' || negative && integer == "0" && (!hasFraction || strings.Trim(fraction, "0") == "") {
		return false
	}
	for _, part := range []string{integer, fraction} {
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return !hasFraction || fraction != "" && !strings.HasSuffix(fraction, "0")
}

func decodeContractBody(request *http.Request, body ContractBodyMapping, schema ContractRequestSchema) (json.RawMessage, error) {
	if request.Body == nil {
		return nil, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "request body is required", nil)
	}
	contentType := request.Header.Get("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		return nil, contractRequestError(schema, "transport.unsupported_media_type", http.StatusUnsupportedMediaType, "request content type is required", nil)
	}
	mediaType, parameters, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "invalid request content type", err)
	}
	accepted := body.AcceptedMediaTypes
	if len(accepted) == 0 {
		switch body.Codec {
		case "json":
			accepted = []string{"application/json"}
		case "problem_json":
			accepted = []string{"application/problem+json"}
		case "text":
			accepted = []string{"text/plain"}
		case "bytes":
			accepted = []string{"application/octet-stream"}
		case "form":
			accepted = []string{"application/x-www-form-urlencoded"}
		case "multipart":
			accepted = []string{"multipart/form-data"}
		}
	}
	if charset := strings.ToLower(strings.TrimSpace(parameters["charset"])); charset != "" && charset != "utf-8" {
		return nil, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "unsupported request charset", nil)
	}
	if !contractMediaAllowed(mediaType, parameters, accepted) {
		return nil, contractRequestError(schema, "transport.unsupported_media_type", http.StatusUnsupportedMediaType, "unsupported request content type", nil)
	}
	compressedLimit := body.MaxCompressedBytes
	if compressedLimit <= 0 {
		compressedLimit = defaultContractRequestBytes
	}
	decompressedLimit := body.MaxDecompressedBytes
	if decompressedLimit <= 0 {
		decompressedLimit = defaultContractDecompressedRequestBytes
	}
	payload, err := readContractBody(request.Body, request.Header.Get("Content-Encoding"), compressedLimit, decompressedLimit, body.SupportedContentEncoding)
	if err != nil {
		return nil, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, err.Error(), err)
	}
	switch body.Codec {
	case "json", "problem_json":
		return strictContractJSON(payload)
	case "text":
		if !utf8.Valid(payload) {
			return nil, contractRequestError(schema, "transport.invalid_request", http.StatusBadRequest, "text body is not UTF-8", nil)
		}
		return json.Marshal(string(payload))
	case "bytes":
		return json.Marshal(base64.StdEncoding.EncodeToString(payload))
	case "form":
		return decodeContractForm(payload, body, schema)
	case "multipart":
		return decodeContractMultipart(payload, request.Header.Get("Content-Type"), body, schema)
	default:
		return nil, contractRequestError(schema, "system.internal", http.StatusInternalServerError, fmt.Sprintf("unsupported generated body codec %q", body.Codec), nil)
	}
}

func readContractBody(reader io.Reader, contentEncoding string, compressedLimit, decompressedLimit int64, supported []string) ([]byte, error) {
	compressed, err := readContractLimited(reader, compressedLimit)
	if err != nil {
		return nil, fmt.Errorf("compressed request body: %w", err)
	}
	encoding := strings.ToLower(strings.TrimSpace(contentEncoding))
	if encoding == "" || encoding == "identity" {
		if int64(len(compressed)) > decompressedLimit {
			return nil, fmt.Errorf("decompressed request body exceeds %d bytes", decompressedLimit)
		}
		return compressed, nil
	}
	allowed := false
	for _, candidate := range supported {
		allowed = allowed || strings.EqualFold(candidate, encoding)
	}
	if !allowed || encoding != "gzip" {
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}
	compressedReader := bytes.NewReader(compressed)
	readerGZIP, err := gzip.NewReader(compressedReader)
	if err != nil {
		return nil, fmt.Errorf("invalid gzip request body: %w", err)
	}
	readerGZIP.Multistream(false)
	decompressed, readErr := readContractLimited(readerGZIP, decompressedLimit)
	closeErr := readerGZIP.Close()
	if readErr != nil {
		return nil, fmt.Errorf("decompressed request body: %w", readErr)
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if compressedReader.Len() != 0 {
		return nil, fmt.Errorf("gzip request body has trailing data")
	}
	return decompressed, nil
}

func readContractLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("exceeds %d bytes", limit)
	}
	return data, nil
}

func strictContractJSON(input []byte) (json.RawMessage, error) {
	if len(input) >= 3 && bytes.Equal(input[:3], []byte{0xef, 0xbb, 0xbf}) || !utf8.Valid(input) || !validContractJSONSurrogates(input) {
		return nil, fmt.Errorf("invalid JSON Unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueContractJSON(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON value")
		}
		return nil, err
	}
	return json.Marshal(value)
}

func decodeUniqueContractJSON(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, composite := token.(json.Delim)
	if !composite {
		return token, nil
	}
	switch delim {
	case '{':
		object := map[string]any{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return nil, err
			}
			key, ok := keyToken.(string)
			if !ok {
				return nil, fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := object[key]; exists {
				return nil, fmt.Errorf("duplicate JSON object member %q", key)
			}
			value, err := decodeUniqueContractJSON(decoder)
			if err != nil {
				return nil, err
			}
			object[key] = value
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim('}') {
			return nil, fmt.Errorf("invalid JSON object")
		}
		return object, nil
	case '[':
		var list []any
		for decoder.More() {
			value, err := decodeUniqueContractJSON(decoder)
			if err != nil {
				return nil, err
			}
			list = append(list, value)
		}
		if close, err := decoder.Token(); err != nil || close != json.Delim(']') {
			return nil, fmt.Errorf("invalid JSON array")
		}
		return list, nil
	default:
		return nil, fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
}

func decodeContractJSONObject(raw []byte) (map[string]json.RawMessage, error) {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return nil, fmt.Errorf("expected JSON object")
	}
	return object, nil
}

func EncodeContractJSON(status int, value any) (ContractHTTPResponse, error) {
	return encodeContractJSON(status, value, "application/json", 0)
}

func EncodeContractJSONForRequest(request *http.Request, status int, value any, produced []string, maxBytes int64) (ContractHTTPResponse, error) {
	return EncodeContractRepresentationForRequest(request, status, value, "json", produced, maxBytes)
}

type ContractResponseOptions struct {
	MaxBytes              int64
	CompressionAlgorithms []string
	CompressionThreshold  int64
	TypeExpression        string
	EncodeValue           func(any) ([]byte, error)
}

type ContractResponseCookie struct {
	Name     string
	Path     string
	Domain   string
	MaxAge   int
	Expires  string
	Secure   bool
	HTTPOnly bool
	SameSite http.SameSite
}

type ContractResponseValueOptions struct {
	Encoding    string
	EncodeValue func(any) ([]byte, error)
}

func AddContractResponseHeader(response *ContractHTTPResponse, name string, value any, configured ...ContractResponseValueOptions) error {
	if response == nil || strings.TrimSpace(name) == "" {
		return fmt.Errorf("contract response header requires a response and name")
	}
	if response.Headers == nil {
		response.Headers = make(http.Header)
	}
	options := ContractResponseValueOptions{Encoding: "repeated"}
	if len(configured) > 1 {
		return fmt.Errorf("contract response header accepts one value-options block")
	}
	if len(configured) == 1 {
		options = configured[0]
		if options.Encoding == "" {
			options.Encoding = "repeated"
		}
	}
	values, err := contractHTTPResponseValues(value, options)
	if err != nil {
		return err
	}
	for _, item := range values {
		if strings.ContainsAny(item, "\r\n") {
			return fmt.Errorf("contract response header contains a line break")
		}
	}
	switch options.Encoding {
	case "repeated":
		for _, item := range values {
			response.Headers.Add(name, item)
		}
	case "comma":
		response.Headers.Set(name, strings.Join(values, ","))
	case "json":
		if len(values) != 1 {
			return fmt.Errorf("JSON response header requires one encoded value")
		}
		response.Headers.Set(name, values[0])
	default:
		return fmt.Errorf("unsupported contract response header encoding %q", options.Encoding)
	}
	return nil
}

func AddContractResponseCookie(response *ContractHTTPResponse, specification ContractResponseCookie, value any, configured ...ContractResponseValueOptions) error {
	if len(configured) > 1 {
		return fmt.Errorf("contract response cookie accepts one value-options block")
	}
	options := ContractResponseValueOptions{Encoding: "repeated"}
	if len(configured) == 1 {
		options = configured[0]
		options.Encoding = "repeated"
	}
	values, err := contractHTTPResponseValues(value, options)
	if err != nil {
		return err
	}
	if len(values) != 1 || strings.TrimSpace(specification.Name) == "" {
		return fmt.Errorf("contract response cookie requires one scalar value and a name")
	}
	if response.Headers == nil {
		response.Headers = make(http.Header)
	}
	path := specification.Path
	if path == "" {
		path = "/"
	}
	expires := time.Time{}
	if specification.Expires != "" {
		var err error
		expires, err = time.Parse(time.RFC3339Nano, specification.Expires)
		if err != nil {
			return fmt.Errorf("contract response cookie has invalid expiry: %w", err)
		}
	}
	cookie := (&http.Cookie{Name: specification.Name, Value: contractCookiePercentEncode(values[0]), Path: path, Domain: specification.Domain, MaxAge: specification.MaxAge, Expires: expires, Secure: specification.Secure, HttpOnly: specification.HTTPOnly, SameSite: specification.SameSite}).String()
	response.Headers.Add("Set-Cookie", cookie)
	return nil
}

func contractHTTPResponseValues(value any, options ContractResponseValueOptions) ([]string, error) {
	if options.EncodeValue == nil {
		return contractHTTPTextValues(value)
	}
	encoded, err := options.EncodeValue(value)
	if err != nil {
		return nil, err
	}
	if options.Encoding == "json" {
		canonical, err := strictContractJSON(encoded)
		if err != nil {
			return nil, err
		}
		return []string{string(canonical)}, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	decoded, err := decodeUniqueContractJSON(decoder)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing response metadata value")
		}
		return nil, err
	}
	return contractHTTPJSONTextValues(decoded)
}

func contractHTTPJSONTextValues(value any) ([]string, error) {
	switch typed := value.(type) {
	case string:
		return []string{typed}, nil
	case bool:
		return []string{strconv.FormatBool(typed)}, nil
	case json.Number:
		return []string{typed.String()}, nil
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			encoded, err := contractHTTPJSONTextValues(item)
			if err != nil || len(encoded) != 1 {
				return nil, fmt.Errorf("contract response metadata collection contains a non-scalar value")
			}
			values = append(values, encoded[0])
		}
		return values, nil
	default:
		return nil, fmt.Errorf("contract response metadata value is not a scalar or scalar collection")
	}
}

func contractHTTPTextValues(value any) ([]string, error) {
	reflected := reflect.ValueOf(value)
	if !reflected.IsValid() {
		return nil, fmt.Errorf("contract response metadata value is nil")
	}
	for reflected.Kind() == reflect.Pointer || reflected.Kind() == reflect.Interface {
		if reflected.IsNil() {
			return nil, fmt.Errorf("contract response metadata value is nil")
		}
		reflected = reflected.Elem()
	}
	if reflected.Kind() == reflect.Slice || reflected.Kind() == reflect.Array {
		if reflected.Type().Elem().Kind() == reflect.Uint8 {
			return []string{base64.StdEncoding.EncodeToString(reflected.Bytes())}, nil
		}
		values := make([]string, 0, reflected.Len())
		for index := 0; index < reflected.Len(); index++ {
			item, err := contractHTTPTextValues(reflected.Index(index).Interface())
			if err != nil || len(item) != 1 {
				return nil, fmt.Errorf("contract response metadata collection contains a non-scalar value")
			}
			values = append(values, item[0])
		}
		return values, nil
	}
	switch reflected.Kind() {
	case reflect.String:
		return []string{reflected.String()}, nil
	case reflect.Bool:
		return []string{strconv.FormatBool(reflected.Bool())}, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return []string{strconv.FormatInt(reflected.Int(), 10)}, nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return []string{strconv.FormatUint(reflected.Uint(), 10)}, nil
	case reflect.Float32, reflect.Float64:
		return []string{strconv.FormatFloat(reflected.Float(), 'g', -1, reflected.Type().Bits())}, nil
	default:
		if stringer, ok := value.(fmt.Stringer); ok {
			return []string{stringer.String()}, nil
		}
		return nil, fmt.Errorf("contract response metadata value has unsupported type %T", value)
	}
}

func contractCookiePercentEncode(value string) string {
	const hexadecimal = "0123456789ABCDEF"
	var encoded strings.Builder
	for index := 0; index < len(value); index++ {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || strings.ContainsRune("-._~", rune(character)) {
			encoded.WriteByte(character)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hexadecimal[character>>4])
		encoded.WriteByte(hexadecimal[character&15])
	}
	return encoded.String()
}

func EncodeContractRepresentationForRequest(request *http.Request, status int, value any, codec string, produced []string, maxBytes int64) (ContractHTTPResponse, error) {
	return EncodeContractRepresentationWithOptions(request, status, value, codec, produced, ContractResponseOptions{MaxBytes: maxBytes})
}

func EncodeContractRepresentationWithOptions(request *http.Request, status int, value any, codec string, produced []string, options ContractResponseOptions) (ContractHTTPResponse, error) {
	accept, acceptEncoding := "", ""
	if request != nil {
		accept = request.Header.Get("Accept")
		acceptEncoding = request.Header.Get("Accept-Encoding")
	}
	mediaType, err := negotiateContractMedia(accept, produced)
	if err != nil {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: err.Error(), Cause: err}
	}
	var body []byte
	switch codec {
	case "json", "problem_json":
		if options.EncodeValue == nil {
			body, err = json.Marshal(value)
		} else {
			body, err = options.EncodeValue(value)
		}
	case "text":
		switch typed := value.(type) {
		case string:
			body = []byte(typed)
		case fmt.Stringer:
			body = []byte(typed.String())
		default:
			err = fmt.Errorf("text response requires a string value, got %T", value)
		}
	case "bytes":
		var ok bool
		body, ok = value.([]byte)
		if !ok {
			err = fmt.Errorf("bytes response requires []byte, got %T", value)
		}
	default:
		err = fmt.Errorf("unsupported generated response codec %q", codec)
	}
	if err != nil {
		return ContractHTTPResponse{}, err
	}
	if options.MaxBytes > 0 && int64(len(body)) > options.MaxBytes {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "system.internal", Status: http.StatusInternalServerError, Message: "response exceeds binding limit"}
	}
	headers := http.Header{"Content-Type": []string{mediaType}, "X-Scenery-Contract-Compression": []string{"handled"}}
	encoding, compressionErr := negotiateContractEncoding(acceptEncoding, options.CompressionAlgorithms)
	if int64(len(body)) < options.CompressionThreshold {
		if identity, identityErr := negotiateContractEncoding(acceptEncoding, nil); identityErr == nil {
			encoding = identity
		}
	}
	if compressionErr != nil {
		return ContractHTTPResponse{}, &ContractTransportError{Outcome: "transport.not_acceptable", Status: http.StatusNotAcceptable, Message: compressionErr.Error(), Cause: compressionErr}
	}
	if encoding == "gzip" {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(body); err != nil {
			return ContractHTTPResponse{}, err
		}
		if err := writer.Close(); err != nil {
			return ContractHTTPResponse{}, err
		}
		body = compressed.Bytes()
		headers.Set("Content-Encoding", "gzip")
	}
	if len(options.CompressionAlgorithms) > 0 {
		headers.Set("Vary", "Accept-Encoding")
	}
	return ContractHTTPResponse{Status: status, Headers: headers, Body: body}, nil
}
