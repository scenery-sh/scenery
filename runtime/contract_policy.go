package runtime

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
)

type ContractContextMapping struct {
	Source string
	Target string
}

type ContractMultipartPart struct {
	Name           string
	Target         string
	Type           string
	Kind           string
	MediaTypes     []string
	MaxBytes       int64
	Multiple       bool
	Optional       bool
	RetainFilename bool
}

type ContractHTTPPolicy struct {
	BindingAddress              string
	GatewayAddress              string
	CORS                        string
	AllowedOrigins              []string
	Forwarded                   string
	TrustedProxyPrefixes        []string
	MaxRequestHeaderBytes       int64
	MaxResponseBytes            int64
	CompressionAlgorithms       []string
	CompressionThreshold        int64
	TotalInvocationTimeoutNanos int64
	ReadTimeoutNanos            int64
	WriteTimeoutNanos           int64
	IdleTimeoutNanos            int64
	AuthorizationStrategy       string
	AuthorizationRuleCount      int
	AuthorizationRules          []ContractAuthorizationRule
	PipelineSteps               []string
	FrameworkGuarantee          string
	TransportStatuses           map[string]int
}

type ContractAuthorizationRule struct {
	Name       string
	Effect     string
	Expression string
}

// PopulateContractContextJSON merges runtime-trusted context into a record
// before generated decoding enforces required fields and type constraints.
func PopulateContractContextJSON(input []byte, mappings []ContractContextMapping) ([]byte, error) {
	if len(mappings) == 0 {
		return input, nil
	}
	object, err := decodeContractJSONObject(input)
	if err != nil {
		return nil, fmt.Errorf("contract context input must be an object: %w", err)
	}
	for _, mapping := range mappings {
		if mapping.Target == "" {
			return nil, fmt.Errorf("contract context target is required")
		}
		if _, exists := object[mapping.Target]; exists {
			return nil, fmt.Errorf("context field %q cannot be supplied by the caller", mapping.Target)
		}
		value, err := contractContextValue(mapping.Source)
		if err != nil {
			return nil, fmt.Errorf("resolve trusted contract context: %w", err)
		}
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode trusted contract context: %w", err)
		}
		object[mapping.Target] = raw
	}
	return json.Marshal(object)
}

func PopulateContractContext(input any, mappings []ContractContextMapping) error {
	if len(mappings) == 0 {
		return nil
	}
	value := reflect.ValueOf(input)
	if value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("contract context target must be a non-nil record pointer")
	}
	for _, mapping := range mappings {
		source, err := contractContextValue(mapping.Source)
		if err != nil {
			return err
		}
		field := value.Elem().FieldByName(contractGoFieldName(mapping.Target))
		if !field.IsValid() || !field.CanSet() {
			return fmt.Errorf("contract context target %q does not exist", mapping.Target)
		}
		if err := assignContractContextValue(field, source); err != nil {
			return fmt.Errorf("populate contract context %s: %w", mapping.Target, err)
		}
	}
	return nil
}

func contractContextValue(source string) (any, error) {
	switch source {
	case "principal.id", "principal.uid":
		if auth := CurrentAuth(); auth != nil {
			return auth.UID, nil
		}
		return "", nil
	case "principal.data":
		if auth := CurrentAuth(); auth != nil {
			return auth.Data, nil
		}
		return nil, nil
	case "context.method":
		return CurrentRequest().Method, nil
	case "context.path":
		return CurrentRequest().Path, nil
	case "context.invocation_id":
		return CurrentRequest().InvocationID, nil
	case "context.trace_id":
		return CurrentRequest().TraceID, nil
	case "context.caller_binding":
		return CurrentRequest().CallerBinding, nil
	case "context.deadline":
		if deadline := CurrentRequest().Deadline; !deadline.IsZero() {
			return deadline.UTC().Format("2006-01-02T15:04:05.999999999Z"), nil
		}
		return nil, nil
	case "context.execution_id":
		return CurrentRequest().ExecutionID, nil
	case "context.deployment":
		return CurrentRequest().Deployment, nil
	case "context.locale":
		return CurrentRequest().Locale, nil
	default:
		if strings.HasPrefix(source, "principal.data.") || strings.HasPrefix(source, "principal.") {
			auth := CurrentAuth()
			if auth == nil {
				return nil, nil
			}
			path := strings.TrimPrefix(source, "principal.")
			path = strings.TrimPrefix(path, "data.")
			return contractNestedValue(auth.Data, path)
		}
		return nil, fmt.Errorf("unsupported contract context source %q", source)
	}
}

func contractNestedValue(value any, path string) (any, error) {
	current := reflect.ValueOf(value)
	for _, segment := range strings.Split(path, ".") {
		for current.IsValid() && (current.Kind() == reflect.Pointer || current.Kind() == reflect.Interface) {
			if current.IsNil() {
				return nil, nil
			}
			current = current.Elem()
		}
		if !current.IsValid() {
			return nil, nil
		}
		switch current.Kind() {
		case reflect.Struct:
			current = current.FieldByName(contractGoFieldName(segment))
		case reflect.Map:
			current = current.MapIndex(reflect.ValueOf(segment))
		default:
			return nil, fmt.Errorf("principal data path %q is not readable", path)
		}
	}
	if !current.IsValid() || !current.CanInterface() {
		return nil, nil
	}
	return current.Interface(), nil
}

func assignContractContextValue(target reflect.Value, source any) error {
	if source == nil {
		return nil
	}
	sourceValue := reflect.ValueOf(source)
	if sourceValue.Type().AssignableTo(target.Type()) {
		target.Set(sourceValue)
		return nil
	}
	if sourceValue.Type().ConvertibleTo(target.Type()) {
		target.Set(sourceValue.Convert(target.Type()))
		return nil
	}
	return fmt.Errorf("%s is not assignable to %s", sourceValue.Type(), target.Type())
}

func contractGoFieldName(value string) string {
	var result strings.Builder
	for _, part := range strings.FieldsFunc(value, func(char rune) bool { return char == '_' || char == '-' }) {
		if part == "" {
			continue
		}
		result.WriteString(strings.ToUpper(part[:1]))
		result.WriteString(part[1:])
	}
	return result.String()
}

func applyContractForwardedPolicy(request *http.Request, policy *ContractHTTPPolicy) {
	if request == nil || policy == nil {
		return
	}
	// Forwarding metadata is authoritative only when both policies and the
	// immediate peer allow it. The raw fields remain ordinary request headers
	// either way so explicit header bindings can still observe them.
	if policy.Forwarded == "" || policy.Forwarded == "reject" || !contractRemoteTrusted(request.RemoteAddr, policy.TrustedProxyPrefixes) {
		return
	}
	client, host, scheme := contractForwardedValues(request.Header)
	if client != "" {
		request.RemoteAddr = net.JoinHostPort(client, "0")
	}
	if host != "" {
		request.Host = host
	}
	if scheme != "" && request.URL != nil {
		request.URL.Scheme = scheme
	}
}

func contractForwardedValues(headers http.Header) (client, host, scheme string) {
	if element := strings.TrimSpace(strings.Split(headers.Get("Forwarded"), ",")[0]); element != "" {
		for _, parameter := range strings.Split(element, ";") {
			name, value, ok := strings.Cut(parameter, "=")
			if !ok {
				continue
			}
			value = strings.Trim(strings.TrimSpace(value), `"`)
			switch strings.ToLower(strings.TrimSpace(name)) {
			case "for":
				client = contractForwardedAddress(value)
			case "host":
				if contractForwardedHostValid(value) {
					host = value
				}
			case "proto":
				value = strings.ToLower(value)
				if value == "http" || value == "https" {
					scheme = value
				}
			}
		}
	}
	if client == "" {
		client = contractForwardedAddress(strings.TrimSpace(strings.Split(headers.Get("X-Forwarded-For"), ",")[0]))
	}
	if host == "" {
		value := strings.TrimSpace(strings.Split(headers.Get("X-Forwarded-Host"), ",")[0])
		if contractForwardedHostValid(value) {
			host = value
		}
	}
	if scheme == "" {
		value := strings.ToLower(strings.TrimSpace(strings.Split(headers.Get("X-Forwarded-Proto"), ",")[0]))
		if value == "http" || value == "https" {
			scheme = value
		}
	}
	return client, host, scheme
}

func contractForwardedAddress(value string) string {
	value = strings.Trim(strings.TrimSpace(value), `"`)
	if strings.HasPrefix(value, "[") {
		if end := strings.IndexByte(value, ']'); end > 0 {
			value = value[1:end]
		}
	} else if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	address, err := netip.ParseAddr(strings.Trim(value, "[]"))
	if err != nil {
		return ""
	}
	return address.String()
}

func contractForwardedHostValid(value string) bool {
	if value == "" || strings.ContainsAny(value, " \\/?#\r\n\t") {
		return false
	}
	if strings.HasPrefix(value, "[") {
		end := strings.IndexByte(value, ']')
		if end <= 0 {
			return false
		}
		_, err := netip.ParseAddr(value[1:end])
		return err == nil && (end == len(value)-1 || value[end+1] == ':')
	}
	host := value
	if parsedHost, _, err := net.SplitHostPort(value); err == nil {
		host = parsedHost
	}
	return host != "" && !strings.HasPrefix(host, ".") && !strings.HasSuffix(host, ".") && !strings.Contains(host, "..")
}

func contractRemoteTrusted(remote string, prefixes []string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		host = remote
	}
	address, err := netip.ParseAddr(strings.Trim(host, "[]"))
	if err != nil {
		return false
	}
	for _, value := range prefixes {
		prefix, err := netip.ParsePrefix(value)
		if err == nil && prefix.Contains(address) {
			return true
		}
	}
	return false
}

func applyContractCORSHeaders(headers http.Header, request *http.Request, policy *ContractHTTPPolicy) {
	if request == nil || policy == nil {
		return
	}
	headers.Del("Access-Control-Allow-Origin")
	headers.Del("Access-Control-Allow-Credentials")
	if policy.CORS == "" || policy.CORS == "none" {
		return
	}
	origin := strings.TrimSpace(request.Header.Get("Origin"))
	if origin == "" || !contractOriginAllowed(origin, policy) {
		return
	}
	headers.Set("Access-Control-Allow-Origin", origin)
	headers.Set("Access-Control-Allow-Credentials", "true")
	addVary(headers, "Origin", "Authorization")
}

func contractOriginAllowed(origin string, policy *ContractHTTPPolicy) bool {
	if policy.CORS == "application" {
		return corsOriginAllowed(origin)
	}
	for _, allowed := range policy.AllowedOrigins {
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func contractRequestHeaderBytes(request *http.Request) int64 {
	if request == nil {
		return 0
	}
	var total int64
	for name, values := range request.Header {
		for _, value := range values {
			total += int64(len(name) + len(value) + 4)
		}
	}
	return total
}
