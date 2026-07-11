package vnext

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	scenery "scenery.sh"
)

type HTTPMultipartPart struct {
	Name           string
	Kind           string
	MediaTypes     []string
	MaxBytes       int64
	Multiple       bool
	RetainFilename bool
}

type HTTPMultipartValue struct {
	Bytes     []byte
	Filename  string
	MediaType string
}

var httpDurationPattern = regexp.MustCompile(`^(-)?P(?:([0-9]+)D)?(?:T(?:([0-9]+)H)?(?:([0-9]+)M)?(?:([0-9]+(?:\.[0-9]{1,9})?)S)?)?$`)

func DecodeHTTPPathSegment(encoded string) (string, error) {
	decoded, err := url.PathUnescape(encoded)
	if err != nil {
		return "", err
	}
	if !utf8.ValidString(decoded) || decoded == "." || decoded == ".." || strings.ContainsAny(decoded, "/\\") {
		return "", fmt.Errorf("invalid decoded path segment")
	}
	for _, char := range decoded {
		if char < 0x20 || char == 0x7f {
			return "", fmt.Errorf("control character in path")
		}
	}
	return decoded, nil
}

func DecodeHTTPQuery(values []string, typeExpression string) (any, error) {
	typeExpression = strings.TrimSpace(typeExpression)
	for _, wrapper := range []string{"list", "set"} {
		prefix := wrapper + "("
		if strings.HasPrefix(typeExpression, prefix) && strings.HasSuffix(typeExpression, ")") {
			inner := strings.TrimSpace(typeExpression[len(prefix) : len(typeExpression)-1])
			decoded := make([]any, 0, len(values))
			seen := map[string]bool{}
			for _, value := range values {
				item, err := DecodeHTTPScalar(inner, value)
				if err != nil {
					return nil, err
				}
				canonical, err := MarshalCanonical(item)
				if err != nil {
					return nil, err
				}
				key := string(canonical)
				if wrapper == "set" && seen[key] {
					return nil, fmt.Errorf("duplicate set element")
				}
				seen[key] = true
				decoded = append(decoded, item)
			}
			if wrapper == "set" {
				sort.Slice(decoded, func(i, j int) bool {
					a, _ := MarshalCanonical(decoded[i])
					b, _ := MarshalCanonical(decoded[j])
					return string(a) < string(b)
				})
			}
			return decoded, nil
		}
	}
	if len(values) != 1 {
		return nil, fmt.Errorf("scalar query parameter requires exactly one value")
	}
	return DecodeHTTPScalar(typeExpression, values[0])
}

func DecodeRawHTTPQuery(rawQuery, wireName, typeExpression, encoding string) (any, error) {
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
	if encoding != "" && encoding != "repeated" && encoding != "comma" && encoding != "json" {
		return nil, fmt.Errorf("unsupported query encoding %q", encoding)
	}
	if encoding == "json" {
		if len(values) != 1 {
			return nil, fmt.Errorf("JSON query parameter requires exactly one value")
		}
		return DecodeHTTPJSON([]byte(values[0]))
	}
	return DecodeHTTPQuery(values, typeExpression)
}

func DecodeHTTPHeader(values []string, typeExpression string) (any, error) {
	trimmed := make([]string, len(values))
	for index, value := range values {
		trimmed[index] = strings.Trim(value, " \t")
	}
	return DecodeHTTPQuery(trimmed, typeExpression)
}

func DecodeHTTPCookie(value, typeExpression string) (any, error) {
	decoded, err := url.PathUnescape(value)
	if err != nil || !utf8.ValidString(decoded) {
		return nil, fmt.Errorf("invalid cookie value")
	}
	return DecodeHTTPScalar(typeExpression, decoded)
}

func DecodeHTTPJSON(input []byte) (any, error) {
	if len(input) >= 3 && bytes.Equal(input[:3], []byte{0xef, 0xbb, 0xbf}) {
		return nil, fmt.Errorf("JSON byte-order mark is forbidden")
	}
	if !utf8.Valid(input) || !validJSONSurrogates(input) {
		return nil, fmt.Errorf("invalid JSON Unicode")
	}
	decoder := json.NewDecoder(bytes.NewReader(input))
	decoder.UseNumber()
	value, err := decodeUniqueJSONValue(decoder)
	if err != nil {
		return nil, err
	}
	if token, err := decoder.Token(); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("trailing JSON token %v", token)
		}
		return nil, err
	}
	return value, nil
}

func decodeUniqueJSONValue(decoder *json.Decoder) (any, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	delim, isDelim := token.(json.Delim)
	if !isDelim {
		if number, ok := token.(json.Number); ok {
			return normalizeJSONNumber(number.String())
		}
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
			value, err := decodeUniqueJSONValue(decoder)
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
			value, err := decodeUniqueJSONValue(decoder)
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

func normalizeJSONNumber(value string) (map[string]any, error) {
	negative := strings.HasPrefix(value, "-")
	unsigned := strings.TrimPrefix(value, "-")
	exponent := int64(0)
	if index := strings.IndexAny(unsigned, "eE"); index >= 0 {
		parsed, err := strconv.ParseInt(unsigned[index+1:], 10, 64)
		if err != nil {
			return nil, fmt.Errorf("JSON number exponent out of range")
		}
		exponent, unsigned = parsed, unsigned[:index]
	}
	scale := int64(0)
	if index := strings.IndexByte(unsigned, '.'); index >= 0 {
		scale = int64(len(unsigned) - index - 1)
		unsigned = unsigned[:index] + unsigned[index+1:]
	}
	unsigned = strings.TrimLeft(unsigned, "0")
	if unsigned == "" {
		return map[string]any{"coefficient": "0", "scale": int64(0)}, nil
	}
	if exponent > 0 && scale < math.MinInt64+exponent || exponent < 0 && scale > math.MaxInt64+exponent {
		return nil, fmt.Errorf("JSON number scale out of range")
	}
	scale -= exponent
	for len(unsigned) > 1 && strings.HasSuffix(unsigned, "0") {
		unsigned = strings.TrimSuffix(unsigned, "0")
		scale--
	}
	if negative {
		unsigned = "-" + unsigned
	}
	return map[string]any{"coefficient": unsigned, "scale": scale}, nil
}

func validJSONSurrogates(input []byte) bool {
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

func NegotiateHTTPMedia(accept string, produced []string) (string, error) {
	if strings.TrimSpace(accept) == "" {
		if len(produced) == 0 {
			return "", fmt.Errorf("no response media types")
		}
		return produced[0], nil
	}
	type choice struct {
		media       string
		quality     float64
		specificity int
		order       int
	}
	var best *choice
	for _, rawRange := range splitHTTPList(accept) {
		mediaRange, params, err := mime.ParseMediaType(strings.TrimSpace(rawRange))
		if err != nil {
			return "", fmt.Errorf("invalid Accept header: %w", err)
		}
		quality := 1.0
		if rawQuality, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(rawQuality, 64)
			if err != nil || quality < 0 || quality > 1 {
				return "", fmt.Errorf("invalid Accept quality")
			}
		}
		if quality == 0 {
			continue
		}
		parts := strings.Split(mediaRange, "/")
		if len(parts) != 2 {
			return "", fmt.Errorf("invalid Accept media range")
		}
		specificity := 2
		if parts[0] == "*" {
			specificity = 0
		} else if parts[1] == "*" {
			specificity = 1
		}
		for order, media := range produced {
			producedType, _, err := mime.ParseMediaType(media)
			if err != nil {
				return "", fmt.Errorf("invalid produced media type: %w", err)
			}
			producedParts := strings.Split(producedType, "/")
			if len(producedParts) != 2 || parts[0] != "*" && parts[0] != producedParts[0] || parts[1] != "*" && parts[1] != producedParts[1] {
				continue
			}
			candidate := choice{media: media, quality: quality, specificity: specificity, order: order}
			if best == nil || candidate.quality > best.quality || candidate.quality == best.quality && candidate.specificity > best.specificity || candidate.quality == best.quality && candidate.specificity == best.specificity && candidate.order < best.order || candidate.quality == best.quality && candidate.specificity == best.specificity && candidate.order == best.order && candidate.media < best.media {
				best = &candidate
			}
		}
	}
	if best == nil {
		return "", fmt.Errorf("no acceptable response media type")
	}
	return best.media, nil
}

func splitHTTPList(value string) []string {
	var result []string
	start, quoted, escaped := 0, false, false
	for index, char := range value {
		switch {
		case escaped:
			escaped = false
		case char == '\\' && quoted:
			escaped = true
		case char == '"':
			quoted = !quoted
		case char == ',' && !quoted:
			result = append(result, value[start:index])
			start = index + 1
		}
	}
	return append(result, value[start:])
}

func ReadHTTPBody(reader io.Reader, contentEncoding string, compressedLimit, decompressedLimit int64, supported []string) ([]byte, error) {
	if compressedLimit < 0 || decompressedLimit < 0 {
		return nil, fmt.Errorf("HTTP body limits must be non-negative")
	}
	compressed, err := readLimitedHTTPBytes(reader, compressedLimit)
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
		if strings.EqualFold(candidate, encoding) {
			allowed = true
			break
		}
	}
	if !allowed || encoding != "gzip" {
		return nil, fmt.Errorf("unsupported content encoding %q", contentEncoding)
	}
	compressedReader := bytes.NewReader(compressed)
	gzipReader, err := gzip.NewReader(compressedReader)
	if err != nil {
		return nil, fmt.Errorf("invalid gzip request body: %w", err)
	}
	gzipReader.Multistream(false)
	decompressed, readErr := readLimitedHTTPBytes(gzipReader, decompressedLimit)
	closeErr := gzipReader.Close()
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

func readLimitedHTTPBytes(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("exceeds %d bytes", limit)
	}
	return data, nil
}

func DecodeHTTPMultipart(body []byte, contentType string, specs []HTTPMultipartPart, maxParts int) (map[string][]HTTPMultipartValue, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || !strings.EqualFold(mediaType, "multipart/form-data") || params["boundary"] == "" {
		return nil, fmt.Errorf("invalid multipart content type")
	}
	byName := make(map[string]HTTPMultipartPart, len(specs))
	for _, spec := range specs {
		if spec.Name == "" || byName[spec.Name].Name != "" {
			return nil, fmt.Errorf("invalid duplicate multipart schema part %q", spec.Name)
		}
		byName[spec.Name] = spec
	}
	reader := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	values := map[string][]HTTPMultipartValue{}
	for count := 0; ; count++ {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if count >= maxParts {
			_ = part.Close()
			return nil, fmt.Errorf("multipart body exceeds %d parts", maxParts)
		}
		name := part.FormName()
		spec, ok := byName[name]
		if !ok {
			_ = part.Close()
			return nil, fmt.Errorf("undeclared multipart part %q", name)
		}
		if !spec.Multiple && len(values[name]) > 0 {
			_ = part.Close()
			return nil, fmt.Errorf("multipart part %q may not repeat", name)
		}
		filename := part.FileName()
		if spec.Kind == "file" && filename == "" || spec.Kind != "file" && filename != "" {
			_ = part.Close()
			return nil, fmt.Errorf("multipart part %q has the wrong kind", name)
		}
		partMedia, _, mediaErr := mime.ParseMediaType(part.Header.Get("Content-Type"))
		if partMedia == "" && mediaErr == nil {
			partMedia = "text/plain"
		}
		if mediaErr != nil || !httpMediaAllowed(partMedia, spec.MediaTypes) {
			_ = part.Close()
			return nil, fmt.Errorf("multipart part %q has unsupported media type", name)
		}
		data, readErr := readLimitedHTTPBytes(part, spec.MaxBytes)
		closeErr := part.Close()
		if readErr != nil {
			return nil, fmt.Errorf("multipart part %q: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if !spec.RetainFilename {
			filename = ""
		}
		values[name] = append(values[name], HTTPMultipartValue{Bytes: data, Filename: filename, MediaType: strings.ToLower(partMedia)})
	}
	return values, nil
}

func httpMediaAllowed(value string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	valueParts := strings.Split(strings.ToLower(value), "/")
	for _, candidate := range allowed {
		candidateType, _, err := mime.ParseMediaType(candidate)
		if err != nil {
			continue
		}
		parts := strings.Split(strings.ToLower(candidateType), "/")
		if len(parts) == 2 && len(valueParts) == 2 && (parts[0] == "*" || parts[0] == valueParts[0]) && (parts[1] == "*" || parts[1] == valueParts[1]) {
			return true
		}
	}
	return false
}

func DecodeHTTPScalar(kind, value string) (any, error) {
	switch kind {
	case "string":
		if !utf8.ValidString(value) {
			return nil, fmt.Errorf("invalid UTF-8 string")
		}
		return value, nil
	case "bool":
		if value == "true" {
			return true, nil
		}
		if value == "false" {
			return false, nil
		}
		return nil, fmt.Errorf("invalid bool")
	case "int":
		return scenery.ParseInt(value)
	case "int32":
		if !canonicalSignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical int32")
		}
		parsed, err := strconv.ParseInt(value, 10, 32)
		return int32(parsed), err
	case "int64":
		if !canonicalSignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical int64")
		}
		return strconv.ParseInt(value, 10, 64)
	case "uint32":
		if !canonicalUnsignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical uint32")
		}
		parsed, err := strconv.ParseUint(value, 10, 32)
		return uint32(parsed), err
	case "uint64":
		if !canonicalUnsignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical uint64")
		}
		return strconv.ParseUint(value, 10, 64)
	case "decimal":
		parsed, err := scenery.ParseDecimal(value)
		if err != nil || parsed.String() != value {
			return nil, fmt.Errorf("invalid canonical decimal")
		}
		return parsed, nil
	case "float32":
		parsed, err := strconv.ParseFloat(value, 32)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || math.Signbit(parsed) && parsed == 0 || strconv.FormatFloat(parsed, 'g', -1, 32) != value {
			return nil, fmt.Errorf("invalid finite float32")
		}
		return float32(parsed), nil
	case "float64":
		parsed, err := strconv.ParseFloat(value, 64)
		if err != nil || math.IsInf(parsed, 0) || math.IsNaN(parsed) || math.Signbit(parsed) && parsed == 0 || strconv.FormatFloat(parsed, 'g', -1, 64) != value {
			return nil, fmt.Errorf("invalid finite float64")
		}
		return parsed, nil
	case "bytes":
		parsed, err := base64.StdEncoding.DecodeString(value)
		if err != nil || base64.StdEncoding.EncodeToString(parsed) != value {
			return nil, fmt.Errorf("invalid canonical base64")
		}
		return parsed, nil
	case "uuid":
		return scenery.ParseUUID(value)
	case "date":
		return scenery.ParseDate(value)
	case "datetime":
		parsed, err := scenery.ParseDateTime(value)
		if err != nil || parsed.String() != value {
			return nil, fmt.Errorf("invalid canonical datetime")
		}
		return parsed, nil
	case "duration":
		parsed, err := decodeHTTPDuration(value)
		if err != nil || parsed.String() != value {
			return nil, fmt.Errorf("invalid canonical duration")
		}
		return parsed, nil
	case "size":
		if !canonicalUnsignedInteger(value) {
			return nil, fmt.Errorf("invalid canonical size")
		}
		return scenery.ParseSize(value + "B")
	case "url":
		parsed, err := scenery.ParseURL(value)
		if err != nil || parsed.String() != value {
			return nil, fmt.Errorf("invalid canonical URL")
		}
		return parsed, nil
	case "relative_path":
		return scenery.ParseRelativePath(value)
	default:
		return nil, fmt.Errorf("unsupported HTTP scalar %q", kind)
	}
}

func canonicalSignedInteger(value string) bool {
	if strings.HasPrefix(value, "-") {
		return len(value) > 1 && value[1] != '0' && canonicalDigits(value[1:])
	}
	return canonicalUnsignedInteger(value)
}

func canonicalUnsignedInteger(value string) bool {
	return value == "0" || len(value) > 0 && value[0] != '0' && canonicalDigits(value)
}

func canonicalDigits(value string) bool {
	for _, char := range value {
		if char < '0' || char > '9' {
			return false
		}
	}
	return value != ""
}

func decodeHTTPDuration(value string) (scenery.Duration, error) {
	matches := httpDurationPattern.FindStringSubmatch(value)
	if matches == nil || (matches[2] == "" && matches[3] == "" && matches[4] == "" && matches[5] == "") || strings.Contains(value, "T") && matches[3] == "" && matches[4] == "" && matches[5] == "" {
		return scenery.Duration{}, fmt.Errorf("invalid HTTP duration")
	}
	var source strings.Builder
	if matches[1] != "" {
		source.WriteByte('-')
	}
	for index, unit := range []string{"d", "h", "m", "s"} {
		if matches[index+2] != "" {
			source.WriteString(matches[index+2])
			source.WriteString(unit)
		}
	}
	return scenery.ParseDuration(source.String())
}
